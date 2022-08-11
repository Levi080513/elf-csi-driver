// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"fmt"
	"k8s.io/utils/pointer"
	"net"
	"net/rpc"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/openlyinc/pointy"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/vm"
	vmvolume "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_volume"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"github.com/smartxworks/cloudtower-go-sdk/v2/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/utils/keymutex"
)

const (
	GB            = 1 << 30
	ReplicaFactor = "replicaFactor"
	ThinProvision = "thinProvision"
	StoragePolicy = "storagePolicy"

	defaultVolumeSize = 1 * GB
	labelKeyName      = "k8s-cluster-id"
)

type contextKey string

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
	clusterId := params["clusterId"]
	sp, err := getStoragePolicy(params)
	if err != nil {
		return nil, err
	}

	sharing, err := checkNeedSharing(req.GetVolumeCapabilities())
	if err != nil {
		return nil, err
	}

	vmVolume, err := c.createVmVolume(clusterId, volumeName, *sp, size, sharing)
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

// TODO(tower): refine this function via tower sdk best practice
func (c *controllerServer) createVmVolume(clusterID string, name string, storagePolicy models.VMVolumeElfStoragePolicyType,
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

	// TODO(tower): vmVolume name duplication is allowed in tower, handle this situation
	if len(getRes.Payload) > 0 {
		return getRes.Payload[0], nil
	}

	createParams := vmvolume.NewCreateVMVolumeParams()
	createParams.RequestBody = []*models.VMVolumeCreationParams{
		{
			ClusterID:        pointy.String(clusterID),
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
	if err := utils.WaitTask(c.config.TowerClient, withTaskVMVolume.TaskID); err != nil {
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

// TODO(tower): refine this function via tower sdk best practice
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

	// TODO(tower): vmVolume name duplication is allowed in tower, handle this situation
	if len(getRes.Payload) < 1 {
		return fmt.Errorf("unable to get volume: %v", volumeID)
	}

	if len(getRes.Payload[0].VMDisks) > 0 {
		klog.Info("volume %v already published, corresponding vm disk: %v", getRes.Payload[0].VMDisks)
		return nil
	}

	if *getRes.Payload[0].Mounting {
		klog.Info("volume %v is mounting on node %v", volumeID, nodeName)
		return nil
	}

	updateParams := vm.NewAddVMDiskParams()
	updateParams.RequestBody = &models.VMAddDiskParams{
		Where: &models.VMWhereInput{
			// TODO(tower): mapping node name to VM
			Name: pointy.String(nodeName),
		},
		Data: &models.VMAddDiskParamsData{
			VMDisks: &models.VMAddDiskParamsDataVMDisks{
				MountDisks: []*models.MountDisksParams{
					{
						Index:      pointy.Int32(0),
						VMVolumeID: pointy.String(volumeID),
						Boot:       pointy.Int32(0),
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

	return utils.WaitTask(c.config.TowerClient, updateRes.Payload[0].TaskID)
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

// TODO(tower): implement DeleteVolume by tower sdk
func (c *controllerServer) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

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

// TODO(tower): implement DeleteVolume by tower sdk
func (c *controllerServer) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {

	return &csi.ControllerUnpublishVolumeResponse{}, nil
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
		klog.Info("volume's the new size is not larger than the current size, request ignored, volume ID: %v", volumeID)
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
