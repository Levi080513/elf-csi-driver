// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/openlyinc/pointy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/utils/keymutex"
	"k8s.io/utils/pointer"

	"github.com/smartxworks/cloudtower-go-sdk/v2/client/cluster"
	clientlabel "github.com/smartxworks/cloudtower-go-sdk/v2/client/label"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/task"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/vm"
	vmdisk "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_disk"
	vmvolume "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_volume"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
)

const (
	GB            = 1 << 30
	StoragePolicy = "storagePolicy"

	defaultVolumeSize = 1 * GB

	defaultClusterLabelKey = "k8s-cluster-id"
)

type controllerServer struct {
	config   *DriverConfig
	keyMutex keymutex.KeyMutex
}

func newControllerServer(config *DriverConfig) *controllerServer {
	return &controllerServer{
		config:   config,
		keyMutex: keymutex.NewHashed(0),
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
	clusterLocalId := params["elfCluster"]
	sp := getStoragePolicy(params)

	sharing, err := checkNeedSharing(req.GetVolumeCapabilities())
	if err != nil {
		return nil, err
	}

	vmVolume, err := c.createVmVolume(clusterLocalId, volumeName, *sp, size, sharing)
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

func (c *controllerServer) createVmVolume(clusterLocalID string, name string, storagePolicy models.VMVolumeElfStoragePolicyType,
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
		LocalID: pointy.String(clusterLocalID),
	}}

	getClusterRes, err := c.config.TowerClient.Cluster.GetClusters(getClusterParams)
	if err != nil {
		return nil, err
	}

	if len(getClusterRes.Payload) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			fmt.Sprintf("failed to get cluster with local id: %v", clusterLocalID))
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

	c.keyMutex.LockKey(nodeName)

	defer func() {
		_ = c.keyMutex.UnlockKey(nodeName)
	}()

	if err := c.publishVolumeToVm(volumeID, nodeName); err != nil {
		return nil, err
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
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

func (c *controllerServer) publishVolumeToVm(volumeID string, nodeName string) error {
	getParams := vmvolume.NewGetVMVolumesParams()
	getParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			ID: pointy.String(volumeID),
		},
	}

	getRes, err := c.config.TowerClient.VMVolume.GetVMVolumes(getParams)
	if err != nil {
		return err
	}

	if len(getRes.Payload) < 1 {
		return fmt.Errorf("unable to get volume: %v", volumeID)
	}

	if len(getRes.Payload[0].VMDisks) > 0 {
		klog.Infof("volume %v already published, corresponding vm disk: %v", volumeID, getRes.Payload[0].VMDisks)
		return nil
	}

	if *getRes.Payload[0].Mounting {
		klog.Infof("volume %v is mounting on node %v", volumeID, nodeName)
		return nil
	}

	getVmParams := vm.NewGetVmsParams()
	getVmParams.RequestBody = &models.GetVmsRequestBody{
		Where: &models.VMWhereInput{
			Name: pointy.String(nodeName),
		},
	}

	getVMRes, err := c.config.TowerClient.VM.GetVms(getVmParams)
	if err != nil {
		return err
	}

	if len(getVMRes.Payload) < 1 {
		return fmt.Errorf("unable to get VM: %v", nodeName)
	}

	updateParams := vm.NewAddVMDiskParams()
	updateParams.RequestBody = &models.VMAddDiskParams{
		Where: &models.VMWhereInput{
			Name: pointy.String(nodeName),
		},
		Data: &models.VMAddDiskParamsData{
			VMDisks: &models.VMAddDiskParamsDataVMDisks{
				MountDisks: []*models.MountDisksParams{
					{
						Index:      pointy.Int32(0),
						VMVolumeID: pointy.String(volumeID),
						Boot:       pointy.Int32(int32(len(getVMRes.Payload[0].VMDisks) + 1)),
						Bus:        models.BusVIRTIO.Pointer(),
					},
				},
			},
		},
	}

	updateRes, err := c.config.TowerClient.VM.AddVMDisk(updateParams)
	if err != nil {
		return err
	}

	return c.waitTask(updateRes.Payload[0].TaskID)
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

	c.keyMutex.LockKey(nodeName)

	defer func() {
		_ = c.keyMutex.UnlockKey(nodeName)
	}()

	if err := c.unpublishVolumeFromVm(volumeID, nodeName); err != nil {
		return nil, err
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (c *controllerServer) unpublishVolumeFromVm(volumeID string, nodeName string) error {
	getVmDiskParams := vmdisk.NewGetVMDisksParams()
	getVmDiskParams.RequestBody = &models.GetVMDisksRequestBody{
		Where: &models.VMDiskWhereInput{
			VM: &models.VMWhereInput{
				Name: pointy.String(nodeName),
			},
			VMVolume: &models.VMVolumeWhereInput{
				ID: pointer.String(volumeID),
			},
		},
	}

	getVmDiskRes, err := c.config.TowerClient.VMDisk.GetVMDisks(getVmDiskParams)
	if err != nil {
		return err
	}

	if len(getVmDiskRes.Payload) < 1 {
		klog.Infof("unable to get VM disk in VM %v with volume %v, skip the unpublish process", nodeName, volumeID)
		return nil
	}

	diskId := getVmDiskRes.Payload[0].ID
	if diskId == nil || *diskId == "" {
		return fmt.Errorf("unable to get disk ID from API in VM %v with volume %v", nodeName, volumeID)
	}

	updateParams := vm.NewRemoveVMDiskParams()
	updateParams.RequestBody = &models.VMRemoveDiskParams{
		Where: &models.VMWhereInput{
			Name: pointy.String(nodeName),
		},
		Data: &models.VMRemoveDiskParamsData{
			DiskIds: []string{*diskId},
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
		return nil, fmt.Errorf("unable to get volume with ID %v", volumeID)
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
	label, err := c.upsertLabel(defaultClusterLabelKey, c.config.ClusterID)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("upsert volume label for cluster %s failed", c.config.ClusterID))
	}

	for _, vmVolumeLabel := range vmVolume.Labels {
		if *vmVolumeLabel.ID == *label.ID {
			return nil
		}
	}

	err = c.addVolumeLabels(*vmVolume.ID, []string{*label.ID})
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("add volume label for volume %s failed", *vmVolume.ID))
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
