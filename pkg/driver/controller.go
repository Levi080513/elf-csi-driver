// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"errors"
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

	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	GB            = 1 << 30
	StoragePolicy = "storagePolicy"

	defaultVolumeSize = 1 * GB

	defaultClusterLabelKey = "elf-csi.k8s-cluster-id"

	defaultSystemClusterLabelKey = "system.cloudtower/elf-csi.k8s-cluster-id"
)

var ErrVMVolumeNotFound = errors.New("volume is not found")

type controllerServer struct {
	config   *DriverConfig
	keyMutex utils.KeyMutex

	volumeAttachList map[string][]string
	volumeDetachList map[string][]string

	batchLock sync.Mutex
}

func newControllerServer(config *DriverConfig) *controllerServer {
	return &controllerServer{
		config:           config,
		keyMutex:         utils.NewKeyMutex(),
		volumeAttachList: map[string][]string{},
		volumeDetachList: map[string][]string{},
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

	// The volume is already published to VM,
	// skip publish and marking ControllerPublishVolume as successful.
	if ok {
		klog.Infof("VM Volume %s is already published in VM %s, skip publish", volumeID, nodeName)
		return &csi.ControllerPublishVolumeResponse{}, nil
	}

	c.addAttachVolume(volumeID, nodeName)

	lock := c.keyMutex.TryLockKey(nodeName)
	if !lock {
		return nil, status.Error(codes.Internal, "VM is updating now, record publish request and return ")
	}

	defer func() {
		_ = c.keyMutex.UnlockKey(nodeName)
	}()

	publishVolumes, err := c.publishVolumesToVm(nodeName)
	if err != nil {
		return nil, err
	}

	// Mark ControllerPublishVolume as failed when current volume not in publishVolumes .
	if !isVolumeInAttachVolumes(volumeID, publishVolumes) {
		return nil, status.Errorf(codes.Internal, "failed to publish volume %s to VM %s, VM Disk is not found", volumeID, nodeName)
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (c *controllerServer) isVolumePublishedToVM(volumeID, nodeName string) (bool, error) {
	getVMDiskParams := vmdisk.NewGetVMDisksParams()
	getVMDiskParams.RequestBody = &models.GetVMDisksRequestBody{
		Where: &models.VMDiskWhereInput{
			VM: &models.VMWhereInput{
				Name: pointy.String(nodeName),
			},
			VMVolume: &models.VMVolumeWhereInput{
				ID: pointy.String(volumeID),
			},
		},
	}

	getVMDiskRes, err := c.config.TowerClient.VMDisk.GetVMDisks(getVMDiskParams)
	if err != nil {
		return true, err
	}

	if len(getVMDiskRes.Payload) == 0 {
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

// publishVolumesToVm is the batch func to attach disk to VM.
// Best effort to publish volumes to VM,
// Skipping volume which is in following case:
//  1. Volume is not found in Tower.
//  2. Volume has mounted on this VM.
//  3. Volume has mounted on other VM.
func (c *controllerServer) publishVolumesToVm(nodeName string) ([]string, error) {
	getVmParams := vm.NewGetVmsParams()
	getVmParams.RequestBody = &models.GetVmsRequestBody{
		Where: &models.VMWhereInput{
			Name: pointy.String(nodeName),
		},
	}

	getVMRes, err := c.config.TowerClient.VM.GetVms(getVmParams)
	if err != nil {
		return nil, err
	}

	// Return failed when the VM which volume will publish to is not found in Tower.
	if len(getVMRes.Payload) < 1 {
		return nil, fmt.Errorf("unable to get VM: %v", nodeName)
	}

	// get volumes which need to publish to this VM.
	attachVolumes := c.GetAttachVolumesAndReset(nodeName)

	needAttachVolumes, err := c.filterNeedAttachVolumes(attachVolumes, getVMRes.Payload[0])
	if err != nil {
		klog.Errorf("failed to filter volumes %v which need attach to VM %s. error %s", attachVolumes, nodeName, err.Error())
		return nil, err
	}

	if len(needAttachVolumes) == 0 {
		klog.Infof("Skip all volumes %v which need attach to VM %v", attachVolumes, nodeName)
		return nil, nil
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
			Boot:       pointy.Int32(int32(len(getVMRes.Payload[0].VMDisks) + 1 + index)),
			Bus:        models.BusVIRTIO.Pointer(),
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

	// The volume is already unpublished from VM,
	// skip unpublish and marking ControllerUnpublishVolume as successful.
	if !ok {
		klog.Infof("VM Volume %s is already unpublished in VM %s, skip unpublish", volumeID, nodeName)
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	c.addDetachVolume(volumeID, nodeName)

	lock := c.keyMutex.TryLockKey(nodeName)
	if !lock {
		return nil, status.Error(codes.Internal, "VM is updating now, record unpublish request and return ")
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
	detachVolumes := c.GetDetachVolumesAndReset(nodeName)

	if len(detachVolumes) == 0 {
		return nil
	}

	getVmDiskParams := vmdisk.NewGetVMDisksParams()
	getVmDiskParams.RequestBody = &models.GetVMDisksRequestBody{
		Where: &models.VMDiskWhereInput{
			VM: &models.VMWhereInput{
				Name: pointy.String(nodeName),
			},
			VMVolume: &models.VMVolumeWhereInput{
				IDIn: detachVolumes,
			},
		},
	}

	getVmDiskRes, err := c.config.TowerClient.VMDisk.GetVMDisks(getVmDiskParams)
	if err != nil {
		return err
	}

	if len(getVmDiskRes.Payload) < 1 {
		klog.Infof("unable to get VM disk in VM %v with volumes %v, skip the unpublish process", nodeName, detachVolumes)
		return nil
	}

	removeVMDiskIDs := []string{}

	for _, vmDisk := range getVmDiskRes.Payload {
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

func (c *controllerServer) addAttachVolume(volumeID, nodeName string) {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()

	_, ok := c.volumeAttachList[nodeName]
	if !ok {
		c.volumeAttachList[nodeName] = []string{}
	}

	for _, attachVolume := range c.volumeAttachList[nodeName] {
		if volumeID == attachVolume {
			return
		}
	}

	c.volumeAttachList[nodeName] = append(c.volumeAttachList[nodeName], volumeID)
}

func (c *controllerServer) GetAttachVolumesAndReset(nodeName string) []string {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()
	volumeList := c.volumeAttachList[nodeName]
	c.volumeAttachList[nodeName] = []string{}

	return volumeList
}

func (c *controllerServer) GetDetachVolumesAndReset(nodeName string) []string {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()
	volumeList := c.volumeDetachList[nodeName]
	c.volumeDetachList[nodeName] = []string{}

	return volumeList
}

func (c *controllerServer) addDetachVolume(volumeID, nodeName string) {
	c.batchLock.Lock()
	defer c.batchLock.Unlock()

	_, ok := c.volumeDetachList[nodeName]
	if !ok {
		c.volumeDetachList[nodeName] = []string{}
	}

	for _, detachVolume := range c.volumeDetachList[nodeName] {
		if volumeID == detachVolume {
			return
		}
	}

	c.volumeDetachList[nodeName] = append(c.volumeDetachList[nodeName], volumeID)
}

// filterNeedAttachVolumes is a func to filter attach volumes in following case:
//  1. Volume is not found in Tower.
//  2. Volume has mounted on this VM.
//  3. Volume has mounted on other VM.
func (c *controllerServer) filterNeedAttachVolumes(attachVolumes []string, vm *models.VM) ([]string, error) {
	// Return early when attachVolumes length is 0.
	if len(attachVolumes) == 0 {
		return nil, nil
	}

	getVMVolumeParams := vmvolume.NewGetVMVolumesParams()
	getVMVolumeParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			IDIn: attachVolumes,
		},
	}

	// GetVMVolumes will filter volume which need attach to VM is not found in Tower.
	getVMVolumeRes, err := c.config.TowerClient.VMVolume.GetVMVolumes(getVMVolumeParams)
	if err != nil {
		return nil, err
	}

	// Return early when all volumes which need attach to VM are not found in Tower.
	if len(getVMVolumeRes.Payload) == 0 {
		klog.Infof("All volumes %v which need attach to VM %s is not found in Tower", attachVolumes, vm.Name)
		return nil, nil
	}

	skipReason := ""
	vmVolumeInTowerIDMap := make(map[string]bool)

	for _, vmVolume := range getVMVolumeRes.Payload {
		vmVolumeInTowerIDMap[*vmVolume.ID] = true
	}

	for _, attachVolume := range attachVolumes {
		if _, ok := vmVolumeInTowerIDMap[attachVolume]; ok {
			continue
		}

		skipReason = fmt.Sprintf("%s \n volume %s is not found in Tower", skipReason, attachVolume)
	}

	// vmDiskIDMap is map for VMDisk ID in this VM.
	vmDiskIDMap := make(map[string]bool)

	for _, vmDisk := range vm.VMDisks {
		vmDiskIDMap[*vmDisk.ID] = true
	}

	needAttachVolumes := []string{}

	for _, volume := range getVMVolumeRes.Payload {
		// Volume VMDisks length is 0 means the volume has not mounted,
		// should append to attach volumes.
		if len(volume.VMDisks) == 0 {
			needAttachVolumes = append(needAttachVolumes, *volume.ID)
			continue
		}

		// If volume VMDisk ID is not in vmDiskIDMap,
		// means the volume has mounted on other VM.
		// skipping attach to this VM.
		if _, ok := vmDiskIDMap[*volume.VMDisks[0].ID]; !ok {
			skipReason = fmt.Sprintf("%s \n volume %s is already mounted in other vm", skipReason, *volume.ID)
			continue
		}

		// If volume VMDisk ID is in vmDiskIDMap,
		// means the volume has mounted on this VM.
		// skipping attach to this VM.
		skipReason = fmt.Sprintf("%s \n volume %s already attached, corresponding vm disk: %v", skipReason, *volume.ID, volume.VMDisks)
	}

	klog.Infof("Skip volumes for reason %s", skipReason)

	return needAttachVolumes, nil
}
