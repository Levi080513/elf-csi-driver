// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/openlyinc/pointy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/smartxworks/cloudtower-go-sdk/v2/client/cluster"
	clientlabel "github.com/smartxworks/cloudtower-go-sdk/v2/client/label"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/task"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/vm"
	vmdisk "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_disk"
	vmvolume "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_volume"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"

	"github.com/smartxworks/elf-csi-driver/pkg/feature"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	GB            = 1 << 30
	StoragePolicy = "storagePolicy"

	defaultVolumeSize = 1 * GB

	defaultClusterLabelKey = "elf-csi.k8s-cluster-id"

	defaultSystemClusterLabelKey = "system.cloudtower/elf-csi.k8s-cluster-id"

	// If Volume has label 'system.cloudtower/keep-after-delete-vm=true',
	// Tower will keep volume after VM which volume mounted has deleted.
	defaultKeepAfterVMDeleteLabelKey = "system.cloudtower/keep-after-delete-vm"
)

// maxAllowedVolumesForSupportBusMap is the map of ELF CSI support attached VM Bus to max allowed volumes mount for the bus.
var maxAllowedVolumesForSupportBusMap = map[models.Bus]int{
	models.BusVIRTIO: 32,
	models.BusSCSI:   32,
}

type controllerServer struct {
	config   *DriverConfig
	keyMutex utils.KeyMutex

	// volumesToBeAttachedMap is the map of VM name to volumes which will be attached to this VM.
	volumesToBeAttachedMap map[string][]string

	// volumesToBeDetachedMap is the map of VM name to volumes which will be detached from this VM.
	volumesToBeDetachedMap map[string][]string

	batchLock sync.Mutex
}

func newControllerServer(config *DriverConfig) *controllerServer {
	return &controllerServer{
		config:                 config,
		keyMutex:               utils.NewKeyMutex(),
		volumesToBeAttachedMap: map[string][]string{},
		volumesToBeDetachedMap: map[string][]string{},
	}
}

// TODO(tower): support create volume from snapshot or clone
func (c *controllerServer) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	volumeName := strings.ToLower(req.GetName())
	if volumeName == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is empty")
	}

	size, err := getVolumeSize(req.GetCapacityRange())
	if err != nil {
		return nil, err
	}

	params := req.GetParameters()
	clusterIdOrLocalId := params["elfCluster"]
	sp := getStoragePolicy(params)

	sharing, err := checkNeedSharing(req.GetVolumeCapabilities())
	if err != nil {
		return nil, err
	}

	vmVolume, err := c.createVmVolume(clusterIdOrLocalId, volumeName, *sp, size, sharing)
	if err != nil {
		return nil, err
	}

	err = c.reconcileVolumeLabel(*vmVolume)
	if err != nil {
		return nil, err
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      *vmVolume.ID,
			CapacityBytes: *vmVolume.Size,
			ContentSource: req.GetVolumeContentSource(),
			// Store StorageClass config in PV spec.csi.volumeAttributes transfer to other RPC call
			VolumeContext: req.Parameters,
		},
	}, nil
}

func (c *controllerServer) createVmVolume(clusterIdOrLocalId string, name string, storagePolicy models.VMVolumeElfStoragePolicyType,
	size uint64, sharing bool) (*models.VMVolume, error) {
	getParams := vmvolume.NewGetVMVolumesParams()
	getParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			Name: pointy.String(name),
		},
	}

	getRes, err := c.config.TowerClient.VMVolume.GetVMVolumes(getParams)
	if err != nil {
		return nil, err
	}

	if len(getRes.Payload) > 0 {
		// If volume is in creating, return error and wait for next requeue.
		if isVolumeInCreating(getRes.Payload[0]) {
			return nil, fmt.Errorf("volume %s is creating now. wait for next requeue", name)
		}

		return getRes.Payload[0], nil
	}

	getClusterParams := cluster.NewGetClustersParams()
	getClusterParams.RequestBody = &models.GetClustersRequestBody{Where: &models.ClusterWhereInput{
		OR: []*models.ClusterWhereInput{
			{
				LocalID: pointy.String(clusterIdOrLocalId),
			},
			{
				ID: pointy.String(clusterIdOrLocalId),
			},
		},
	}}

	getClusterRes, err := c.config.TowerClient.Cluster.GetClusters(getClusterParams)
	if err != nil {
		return nil, err
	}

	if len(getClusterRes.Payload) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			fmt.Sprintf("failed to get cluster with id or local id: %v", clusterIdOrLocalId))
	}

	createParams := vmvolume.NewCreateVMVolumeParams()
	createParams.RequestBody = []*models.VMVolumeCreationParams{
		{
			ClusterID:        getClusterRes.Payload[0].ID,
			ElfStoragePolicy: storagePolicy.Pointer(),
			Size:             pointy.Int64(int64(size)),
			Name:             pointy.String(name),
			Sharing:          pointy.Bool(sharing),
		},
	}

	createRes, err := c.config.TowerClient.VMVolume.CreateVMVolume(createParams)
	if err != nil {
		return nil, err
	}

	withTaskVMVolume := createRes.Payload[0]
	if err = c.waitTask(withTaskVMVolume.TaskID); err != nil {
		return nil, err
	}

	return withTaskVMVolume.Data, err
}

func (c *controllerServer) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	if req.GetReadonly() {
		return nil, status.Error(codes.InvalidArgument, "not support publish volume read only")
	}

	nodeName := req.GetNodeId()
	if nodeName == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId is empty")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability is empty")
	}

	nodeEntry, err := c.config.NodeMap.Get(nodeName)
	if err != nil {
		return nil, err
	}

	nodeAddr := fmt.Sprintf("%v:%v", nodeEntry.NodeIP, nodeEntry.LivenessPort)

	err = c.canPublishToNode(nodeAddr)
	if err != nil {
		return nil, err
	}

	ok, err := c.isVolumePublishedToVM(volumeID, nodeName)
	if err != nil {
		return nil, err
	}

	// The volume is already published to this VM,
	// remove the volume from volume list which need attach to this VM,
	// skip publish and marking ControllerPublishVolume as successful.
	if ok {
		c.RemoveVolumeFromVolumesToBeAttached(volumeID, nodeName)
		klog.Infof("VM Volume %s is already published in VM %s, skip publish", volumeID, nodeName)

		return &csi.ControllerPublishVolumeResponse{}, nil
	}

	if feature.Gates.Enabled(feature.BatchProcessVolume) {
		// If batch publish/unpublish volume is enabled,
		// regardless of whether the lock can be obtained,
		// add volume to the volume list which need attach to this VM,
		// this way we can publish multiple volumes to this VM at a time.
		c.addVolumeToVolumesToBeAttached(volumeID, nodeName)

		lock := c.keyMutex.TryLockKey(nodeName)
		if !lock {
			return nil, status.Error(codes.Internal, "VM is updating now, record publish request and return")
		}
	} else {
		// If batch publish/unpublish volume is disabled,
		// lock to prevent other volumes that need to be attached from joining attach volume list，
		// make sure only one volume can be attached to the VM at a time.
		c.keyMutex.LockKey(nodeName)
		c.addVolumeToVolumesToBeAttached(volumeID, nodeName)
	}

	defer func() {
		_ = c.keyMutex.UnlockKey(nodeName)
	}()

	publishedVolumes, err := c.publishVolumesToVm(nodeName)
	if err != nil {
		return nil, err
	}

	// Mark ControllerPublishVolume as failed when current volume not in publishedVolumes .
	if !isVolumeInAttachVolumes(volumeID, publishedVolumes) {
		return nil, status.Errorf(codes.Internal, "failed to publish volume %s to VM %s, VM Disk is not found", volumeID, nodeName)
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (c *controllerServer) isVolumePublishedToVM(volumeID, nodeName string) (bool, error) {
	vmDisks, err := c.getVMDisksByVolumeIDs(nodeName, []string{volumeID})
	if err != nil {
		return true, err
	}

	if len(vmDisks) == 0 {
		return false, nil
	}

	return true, nil
}

func (c *controllerServer) canPublishToNode(nodeAddr string) error {
	conn, err := net.Dial("tcp", nodeAddr)
	if err != nil {
		return status.Error(codes.Internal,
			fmt.Sprintf("failed to connect to %v, %v", nodeAddr, err))
	}

	client := rpc.NewClient(conn)

	defer client.Close()

	rsp := &NodeLivenessRsp{}

	err = client.Call("NodeLivenessServer.Liveness", &NodeLivenessReq{}, rsp)
	if err != nil {
		return status.Error(codes.Internal,
			fmt.Sprintf("failed to call %v NodeLiveness.Liveness, %v", nodeAddr, err))
	}

	if !rsp.Health {
		return status.Error(codes.FailedPrecondition,
			fmt.Sprintf("can't publish volume to a unhealth node, %+v", rsp))
	}

	return nil
}

// publishVolumesToVm is the batch func to attach disk to VM,
// return volumes which attached success in this publish.
// Best effort to publish volumes to VM,
// Skipping volume which is in following case:
//  1. Volume is not found in Tower.
//  2. Volume has mounted on this VM.
//  3. Volume has mounted on other VM.
func (c *controllerServer) publishVolumesToVm(nodeName string) ([]string, error) {
	targetVM, err := c.getVMByName(nodeName)
	// Due to Tower problems in some special cases, we may not able to get VM from Tower, but the VM is actually running well in ELF cluster and kubelet is ready.
	// So need to return failure and let it requeue until Tower gets the VM back or the K8s node is deleted.
	if err == ErrVMNotFound {
		klog.Info(fmt.Sprintf(VMNotFoundInTowerErrorMessage, nodeName) + "marking ControllerPublishVolume as failed")
		return nil, fmt.Errorf("failed to attach volume to VM, %s", fmt.Sprintf(VMNotFoundInTowerErrorMessage, nodeName))
	}

	// Task which attach volumes to the VM will be failed when this VM is in updating,
	// return failed early.
	if isVMInUpdating(targetVM) {
		return nil, fmt.Errorf("failed to attach volumes to VM %s, because this VM is being updated", nodeName)
	}

	// get volumes which need to publish to this VM.
	attachVolumes := c.GetVolumesToBeAttachedAndReset(nodeName)

	needAttachVolumes, err := c.filterNeedAttachVolumes(attachVolumes, targetVM)
	if err != nil {
		klog.Errorf("failed to filter volumes %v which need attach to VM %s. error %s", attachVolumes, nodeName, err.Error())
		return nil, err
	}

	if len(needAttachVolumes) == 0 {
		klog.Infof("Skip all volumes %v which need attach to VM %v", attachVolumes, nodeName)
		return nil, nil
	}

	targetBus, freeSeatsOnBus, err := c.GetAvailableBusForVolumes(targetVM)
	if err != nil {
		return nil, err
	}

	// To make sure AddVMDisk task success, we should reduce volumes which need attach to this VM in this publish
	// when freeSeatsOnBus < number of needAttachVolumes.
	// reduced volumes will join attach volume list in the next volume publish.
	if freeSeatsOnBus > 0 && freeSeatsOnBus < len(needAttachVolumes) {
		needAttachVolumes = needAttachVolumes[:freeSeatsOnBus]
	}

	updateParams := vm.NewAddVMDiskParams()
	updateParams.RequestBody = &models.VMAddDiskParams{
		Where: &models.VMWhereInput{
			Name: pointy.String(nodeName),
		},
		Data: &models.VMAddDiskParamsData{
			VMDisks: &models.VMAddDiskParamsDataVMDisks{
				MountDisks: []*models.MountDisksParams{},
			},
		},
	}

	for index, needAttachVolume := range needAttachVolumes {
		mountParam := &models.MountDisksParams{
			Index:      pointy.Int32(int32(index)),
			VMVolumeID: pointy.String(needAttachVolume),
			Boot:       pointy.Int32(int32(len(targetVM.VMDisks) + 1 + index)),
			Bus:        &targetBus,
		}
		updateParams.RequestBody.Data.VMDisks.MountDisks = append(updateParams.RequestBody.Data.VMDisks.MountDisks, mountParam)
	}

	updateRes, err := c.config.TowerClient.VM.AddVMDisk(updateParams)
	if err != nil {
		return nil, err
	}

	return needAttachVolumes, c.waitTask(updateRes.Payload[0].TaskID)
}

func (c *controllerServer) ControllerGetCapabilities(
	ctx context.Context,
	req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	newCap := func(cap csi.ControllerServiceCapability_RPC_Type) *csi.ControllerServiceCapability {
		return &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		}
	}

	var caps []*csi.ControllerServiceCapability
	for _, cap := range []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
	} {
		caps = append(caps, newCap(cap))
	}

	resp := &csi.ControllerGetCapabilitiesResponse{
		Capabilities: caps,
	}

	return resp, nil
}

func (c *controllerServer) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	volume, err := c.getVolume(volumeID)
	if err == ErrVMVolumeNotFound {
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to find volume %v, %v", volumeID, err)
	}

	if volume.Mounting != nil && *volume.Mounting == true {
		return nil, status.Error(codes.FailedPrecondition,
			fmt.Sprintf("volume %v is in use", volumeID))
	}

	deleteParams := vmvolume.NewDeleteVMVolumeFromVMParams()
	deleteParams.RequestBody = &models.VMVolumeDeletionParams{Where: &models.VMVolumeWhereInput{
		ID: pointy.String(volumeID),
	}}

	deleteRes, err := c.config.TowerClient.VMVolume.DeleteVMVolumeFromVM(deleteParams)
	if err != nil {
		return nil, status.Error(codes.Internal,
			fmt.Sprintf("failed to delete volume %v, %v", volumeID, err))
	}

	err = c.waitTask(deleteRes.Payload[0].TaskID)
	if err != nil {
		return nil, status.Error(codes.Internal,
			fmt.Sprintf("delete volume %v task failed, %v", volumeID, err))
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c *controllerServer) ListVolumes(
	ctx context.Context,
	req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

// From k8s 1.20+ version, csi controller server interface add ControllerGetVolume function
func (c *controllerServer) ControllerGetVolume(
	ctx context.Context,
	req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

func (c *controllerServer) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	nodeName := req.GetNodeId()
	if nodeName == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId is empty")
	}

	ok, err := c.isVolumePublishedToVM(volumeID, nodeName)
	if err != nil {
		return nil, err
	}

	// The volume is already unpublished from this VM,
	// remove volume from volume list which need detach from this VM,
	// skip unpublish and marking ControllerUnpublishVolume as successful.
	if !ok {
		c.RemoveVolumeFromVolumesToBeDetached(volumeID, nodeName)
		klog.Infof("VM Volume %s is already unpublished in VM %s, skip unpublish", volumeID, nodeName)

		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	if feature.Gates.Enabled(feature.BatchProcessVolume) {
		// If batch publish/unpublish volume is enabled,
		// regardless of whether the key lock can be obtained,
		// add volume to the volume list which need detach from this VM,
		// this way we can unpublish multiple volumes from this VM at a time.
		c.addVolumeToVolumesToBeDetached(volumeID, nodeName)

		lock := c.keyMutex.TryLockKey(nodeName)
		if !lock {
			return nil, status.Error(codes.Internal, "VM is updating now, record unpublish request and return")
		}
	} else {
		// If batch publish/unpublish volume is disabled,
		// lock to prevent other volumes that need to be detached from joining detach volume list，
		// make sure only one volume can be detached to the VM at a time.
		c.keyMutex.LockKey(nodeName)
		c.addVolumeToVolumesToBeDetached(volumeID, nodeName)
	}

	defer func() {
		_ = c.keyMutex.UnlockKey(nodeName)
	}()

	if err := c.unpublishVolumesFromVm(nodeName); err != nil {
		return nil, err
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// unpublishVolumesFromVm is the batch func to detach disk from VM.
func (c *controllerServer) unpublishVolumesFromVm(nodeName string) error {
	targetVM, err := c.getVMByName(nodeName)
	// Tower will keep volumes which has attached to this VM and created by ELF CSI after this VM delete,
	// so if VM is not present in SMTX OS ELF or moved to the recycle bin,
	// means volume has already detached from this VM,
	// so marking ControllerUnpublishVolume as successful.
	if err == ErrVMNotFound {
		klog.Info(fmt.Sprintf(VMNotFoundInTowerErrorMessage, nodeName) + "marking ControllerUnpublishVolume as successful")
		return nil
	}

	// Task which detach volumes from the VM will be failed when this VM is in updating,
	// return failed early.
	if isVMInUpdating(targetVM) {
		return fmt.Errorf("failed to detach volumes from VM %s, because this VM is being updated", nodeName)
	}

	detachVolumes := c.GetVolumesToBeDetachedAndReset(nodeName)

	if len(detachVolumes) == 0 {
		return nil
	}

	vmDisks, err := c.getVMDisksByVolumeIDs(nodeName, detachVolumes)
	if err != nil {
		return err
	}

	if len(vmDisks) < 1 {
		klog.Infof("unable to get VM disk in VM %v with volumes %v, skip the unpublish process", nodeName, detachVolumes)
		return nil
	}

	removeVMDiskIDs := []string{}

	for _, vmDisk := range vmDisks {
		if vmDisk.ID == nil || *vmDisk.ID == "" {
			return fmt.Errorf("unable to get disk ID from API in VM %v with volume %v", nodeName, vmDisk.ID)
		}

		removeVMDiskIDs = append(removeVMDiskIDs, *vmDisk.ID)
	}

	updateParams := vm.NewRemoveVMDiskParams()
	updateParams.RequestBody = &models.VMRemoveDiskParams{
		Where: &models.VMWhereInput{
			Name: pointy.String(nodeName),
		},
		Data: &models.VMRemoveDiskParamsData{
			DiskIds: removeVMDiskIDs,
		},
	}

	updateRes, err := c.config.TowerClient.VM.RemoveVMDisk(updateParams)
	if err != nil {
		return err
	}

	return c.waitTask(updateRes.Payload[0].TaskID)
}

func (c *controllerServer) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	err := checkVolumeCapabilities(req.GetVolumeCapabilities())
	if err != nil {
		return nil, err
	}

	_, err = c.getVolume(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to find volume %v, %v", volumeID, err)
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

func (c *controllerServer) GetCapacity(
	ctx context.Context,
	req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

// TODO(tower): implement CreateSnapshot by tower sdk
func (c *controllerServer) CreateSnapshot(
	ctx context.Context,
	req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, nil
}

// TODO(tower): implement DeleteSnapshot by tower sdk
func (c *controllerServer) DeleteSnapshot(
	ctx context.Context,
	req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, nil
}

func (c *controllerServer) ListSnapshots(
	ctx context.Context,
	req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

func (c *controllerServer) ControllerExpandVolume(
	ctx context.Context,
	req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is empty")
	}

	if req.GetCapacityRange() == nil {
		return nil, status.Error(codes.InvalidArgument, "capacity range is empty")
	}

	if req.GetVolumeCapability() != nil {
		err := checkVolumeCapabilities([]*csi.VolumeCapability{req.GetVolumeCapability()})
		if err != nil {
			return nil, err
		}
	}

	newSize, err := getVolumeSize(req.GetCapacityRange())
	if err != nil {
		return nil, err
	}

	volume, err := c.getVolume(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to find volume %v, %v", volumeID, err)
	}

	uSize := uint64(*volume.Size)
	if uSize < newSize {
		err = c.expandVolume(volumeID, int64(newSize))
		if err != nil {
			return nil, status.Errorf(codes.Internal,
				"failed to update volume %v size, %v", volumeID, err)
		}
	} else {
		klog.Infof("volume's the new size is not larger than the current size, request ignored, volume ID: %v", volumeID)
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         *volume.Size,
		NodeExpansionRequired: true,
	}, nil
}

// TODO(tower): re-use this function with node driver
func (c *controllerServer) getVolume(volumeID string) (*models.VMVolume, error) {
	getVolumeParams := vmvolume.NewGetVMVolumesParams()
	getVolumeParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			ID: pointer.String(volumeID),
		},
	}

	getVolumeRes, err := c.config.TowerClient.VMVolume.GetVMVolumes(getVolumeParams)
	if err != nil {
		return nil, err
	}

	if len(getVolumeRes.Payload) < 1 {
		return nil, ErrVMVolumeNotFound
	}

	return getVolumeRes.Payload[0], nil
}

// TODO(tower): impl this when sdk updated
func (c *controllerServer) expandVolume(volumeID string, newSize int64) error {
	return nil
}

func checkTaskFinished(task *models.Task) int8 {
	switch *task.Status {
	case models.TaskStatusSUCCESSED:
		return 0
	case models.TaskStatusFAILED:
		return 1
	default:
		return -1
	}
}

// ensure labels are ready before attaching to volumes.
func (c *controllerServer) upsertLabel(key, value string) (*models.Label, error) {
	getLabelParams := clientlabel.NewGetLabelsParams()
	getLabelParams.RequestBody = &models.GetLabelsRequestBody{
		Where: &models.LabelWhereInput{
			Key:   &key,
			Value: &value,
		},
	}

	getLabelResp, err := c.config.TowerClient.Label.GetLabels(getLabelParams)
	if err != nil {
		return nil, err
	}

	if len(getLabelResp.Payload) > 0 {
		return getLabelResp.Payload[0], nil
	}

	createLabelParams := clientlabel.NewCreateLabelParams()
	createLabelParams.RequestBody = []*models.LabelCreationParams{
		{Key: &key, Value: &value},
	}

	createLabelResp, err := c.config.TowerClient.Label.CreateLabel(createLabelParams)
	if err != nil {
		return nil, err
	}

	if len(createLabelResp.Payload) == 0 {
		return nil, fmt.Errorf("create label for key %s value %s failed", key, value)
	}

	return createLabelResp.Payload[0].Data, nil
}

func (c *controllerServer) reconcileVolumeLabel(vmVolume models.VMVolume) error {
	commonClusterIDLabel, err := c.upsertLabel(defaultClusterLabelKey, c.config.ClusterID)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("upsert volume label %s for cluster %s failed", defaultClusterLabelKey, c.config.ClusterID))
	}

	systemClusterIDLabel, err := c.upsertLabel(defaultSystemClusterLabelKey, c.config.ClusterID)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("upsert volume label %s for cluster %s failed", defaultSystemClusterLabelKey, c.config.ClusterID))
	}

	keepAfterVMDeleteLabel, err := c.upsertLabel(defaultKeepAfterVMDeleteLabelKey, "true")
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("upsert volume label %s failed", defaultKeepAfterVMDeleteLabelKey))
	}

	addLabelIDs := []string{}

	vmLabelMap := make(map[string]bool)
	for _, vmVolumeLabel := range vmVolume.Labels {
		vmLabelMap[*vmVolumeLabel.ID] = true
	}

	if _, ok := vmLabelMap[*commonClusterIDLabel.ID]; !ok {
		addLabelIDs = append(addLabelIDs, *commonClusterIDLabel.ID)
	}

	if _, ok := vmLabelMap[*systemClusterIDLabel.ID]; !ok {
		addLabelIDs = append(addLabelIDs, *systemClusterIDLabel.ID)
	}

	if _, ok := vmLabelMap[*keepAfterVMDeleteLabel.ID]; !ok {
		addLabelIDs = append(addLabelIDs, *keepAfterVMDeleteLabel.ID)
	}

	if len(addLabelIDs) == 0 {
		return nil
	}

	err = c.addVolumeLabels(*vmVolume.ID, addLabelIDs)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("add label to volume %s failed", *vmVolume.ID))
	}

	return nil
}

func (c *controllerServer) addVolumeLabels(volumeID string, labels []string) error {
	addLabelsParams := clientlabel.NewAddLabelsToResourcesParams()
	addLabelsParams.RequestBody = &models.AddLabelsToResourcesParams{
		Where: &models.LabelWhereInput{
			IDIn: labels,
		},
		Data: &models.AddLabelsToResourcesParamsData{
			VMVolumes: &models.VMVolumeWhereInput{
				ID: &volumeID,
			},
		},
	}

	addLabelsResp, err := c.config.TowerClient.Label.AddLabelsToResources(addLabelsParams)
	if err != nil {
		return err
	}

	if len(addLabelsResp.Payload) == 0 {
		return fmt.Errorf("add label to volume %s failed", volumeID)
	}

	return c.waitTask(addLabelsResp.Payload[0].TaskID)
}

func (c *controllerServer) waitTask(id *string) error {
	if id == nil {
		return nil
	}

	start := time.Now()
	taskParams := task.NewGetTasksParams()
	taskParams.RequestBody = &models.GetTasksRequestBody{
		Where: &models.TaskWhereInput{
			ID: id,
		},
	}

	for {
		tasks, err := c.config.TowerClient.Task.GetTasks(taskParams)
		if err != nil {
			return err
		}

		switch {
		case !(len(tasks.GetPayload()) > 0):
			time.Sleep(5 * time.Second)

			if time.Since(start) > 1*time.Minute {
				return fmt.Errorf("task %s not found after 1 minute", *id)
			}
		case checkTaskFinished(tasks.Payload[0]) >= 0:
			if *tasks.Payload[0].Status == models.TaskStatusFAILED {
				return fmt.Errorf("task %s failed", *id)
			}

			return nil
		default:
			time.Sleep(5 * time.Second)

			if time.Since(start) > 10*time.Minute {
				return fmt.Errorf("task %s timeout", *id)
			}
		}
	}
}

// filterNeedAttachVolumes is a func to filter attach volumes in following case:
//  1. Volume is not found in Tower.
//  2. Volume has mounted on this VM.
//  3. Volume has mounted on other VM.
func (c *controllerServer) filterNeedAttachVolumes(volumeIDsToBeAttached []string, vm *models.VM) ([]string, error) {
	// Return early when attachVolumes length is 0.
	if len(volumeIDsToBeAttached) == 0 {
		return nil, nil
	}

	// Get all volumes to be attached to this VM by volume IDs in volumesToBeAttached.
	// It will not include the volumes which are not found in Tower.
	var volumesToBeAttached []*models.VMVolume
	getVMVolumeParams := vmvolume.NewGetVMVolumesParams()
	getVMVolumeParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			IDIn: volumeIDsToBeAttached,
		},
	}

	getVMVolumesRes, err := c.config.TowerClient.VMVolume.GetVMVolumes(getVMVolumeParams)
	if err != nil {
		return nil, err
	}

	volumesToBeAttached = getVMVolumesRes.Payload
	if len(volumesToBeAttached) == 0 {
		// Return early when all volumes to be attached to this VM are not found in Tower.
		klog.Infof("All volumes %v to be attached to VM %s are not found in Tower", volumeIDsToBeAttached, vm.Name)
		return nil, nil
	}

	skipReasons := []string{}
	vmVolumeInTowerIDMap := make(map[string]bool)

	for _, vmVolume := range volumesToBeAttached {
		vmVolumeInTowerIDMap[*vmVolume.ID] = true
	}

	// record skip reason for volume is not found in Tower.
	for _, volumeID := range volumeIDsToBeAttached {
		if _, ok := vmVolumeInTowerIDMap[volumeID]; ok {
			continue
		}

		skipReasons = append(skipReasons, fmt.Sprintf("ERROR: volume %s is not found in Tower", volumeID))
	}

	// Get all VMDisks of this VM related to the volumes to be attached.
	vmDisks, err := c.getVMDisksByVolumeIDs(*vm.Name, volumeIDsToBeAttached)
	if err != nil {
		return nil, err
	}
	// attachedVolumeIDsMap is map for Volume ID which has attached to VM.
	attachedVolumeIDsMap := make(map[string]bool)

	for _, vmDisk := range vmDisks {
		if vmDisk.VMVolume == nil {
			continue
		}

		if vmDisk.VMVolume.ID == nil {
			continue
		}

		attachedVolumeIDsMap[*vmDisk.VMVolume.ID] = true
	}

	volumesNeedAttach := []string{}

	for _, volume := range volumesToBeAttached {
		// If volume ID is in attachedVolumesIDMap, it means the volume is already attached on this VM.
		if _, ok := attachedVolumeIDsMap[*volume.ID]; ok {
			skipReasons = append(skipReasons, fmt.Sprintf("volume %s was already attached, corresponding vm disk: %v", *volume.Name, volume.VMDisks))
			continue
		}

		// If volume sharing is false and VMDisks length is not 0, means the volume is not sharing volume but already attached in other vm.
		// This is unexpected due to Tower error. Need to skip attaching this volume, and handle it in future reconcile.
		if !*volume.Sharing && len(volume.VMDisks) != 0 {
			skipReasons = append(skipReasons, fmt.Sprintf("ERROR: volume %s is still attached to another vm, so can not attach it to vm %s", *volume.Name, *vm.Name))
			continue
		}

		volumesNeedAttach = append(volumesNeedAttach, *volume.ID)
	}

	klog.Infof("Skip attaching volumes due to: %s", strings.Join(skipReasons, ","))

	return volumesNeedAttach, nil
}

// GetAvailableBusForVolumes returns the available VM bus to which new volumes can be attached and the free seats on this bus.
func (c *controllerServer) GetAvailableBusForVolumes(vm *models.VM) (models.Bus, int, error) {
	vmDisks, err := c.getVMDisksByVolumeIDs(*vm.Name, []string{})
	if err != nil {
		return "", 0, err
	}

	// vmBusToAttachNumberMap is the map of VM bus to number of volumes which attach to this bus.
	busToAttachedVolumesNumberMap := make(map[models.Bus]int)

	for vmBus := range maxAllowedVolumesForSupportBusMap {
		busToAttachedVolumesNumberMap[vmBus] = 0
	}

	for _, vmDisk := range vmDisks {
		if vmDisk.Bus == nil {
			continue
		}

		// only record attach volume number of VM Bus which ELF CSI support attach volume to.
		if _, ok := busToAttachedVolumesNumberMap[*vmDisk.Bus]; !ok {
			continue
		}

		busToAttachedVolumesNumberMap[*vmDisk.Bus]++
	}

	// first check whether preferred VM Bus can be attached.
	attachedVolumesNumber, ok := busToAttachedVolumesNumberMap[models.Bus(c.config.PreferredVolumeBusType)]
	if ok && attachedVolumesNumber < maxAllowedVolumesForSupportBusMap[models.Bus(c.config.PreferredVolumeBusType)] {
		return models.Bus(c.config.PreferredVolumeBusType), maxAllowedVolumesForSupportBusMap[models.Bus(c.config.PreferredVolumeBusType)] - attachedVolumesNumber, nil
	}

	for vmBus, attachedVolumesNumber := range busToAttachedVolumesNumberMap {
		if attachedVolumesNumber < maxAllowedVolumesForSupportBusMap[vmBus] {
			return vmBus, maxAllowedVolumesForSupportBusMap[vmBus] - attachedVolumesNumber, nil
		}
	}

	return "", 0, fmt.Errorf("failed to find available VM Bus because all VM Buses are full")
}

func (c *controllerServer) getVMByName(vmName string) (*models.VM, error) {
	getVmParams := vm.NewGetVmsParams()
	getVmParams.RequestBody = &models.GetVmsRequestBody{
		Where: &models.VMWhereInput{
			Name: pointy.String(vmName),
		},
	}

	getVMRes, err := c.config.TowerClient.VM.GetVms(getVmParams)
	if err != nil {
		return nil, err
	}

	if len(getVMRes.Payload) == 0 {
		return nil, ErrVMNotFound
	}

	return getVMRes.Payload[0], nil
}

// getVMDisksByVolumeIDs func will return all VM Disk in this VM when volumeIDs length is 0.
func (c *controllerServer) getVMDisksByVolumeIDs(nodeName string, volumeIDs []string) ([]*models.VMDisk, error) {
	getVMDiskParams := vmdisk.NewGetVMDisksParams()
	getVMDiskParams.RequestBody = &models.GetVMDisksRequestBody{
		Where: &models.VMDiskWhereInput{
			VM: &models.VMWhereInput{
				Name: pointy.String(nodeName),
			},
			VMVolume: &models.VMVolumeWhereInput{
				IDIn: volumeIDs,
			},
		},
	}

	getVMDiskRes, err := c.config.TowerClient.VMDisk.GetVMDisks(getVMDiskParams)
	if err != nil {
		return nil, err
	}

	return getVMDiskRes.Payload, nil
}
