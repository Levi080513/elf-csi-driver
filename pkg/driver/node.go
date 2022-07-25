// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/iomesh/operator/pkg/commonutils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kmount "k8s.io/utils/mount"

	"github.com/iomesh/csi-driver/pkg/utils"
)

type NodeServer interface {
	csi.NodeServer
	registerNodeEntry() error
}

func registerNodeEntry(config *DriverConfig) error {
	nodeIP, err := getNodeIP()
	if err != nil {
		return err
	}

	entry := &NodeEntry{
		NodeIP:       nodeIP,
		LivenessPort: config.LivenessPort,
	}

	const retryLimit = 5
	const retryInterval = time.Millisecond * 500

	// Configmap update uses the optimistic locking mechanism,
	// so it needs to retry when conflict
	err = utils.Retry(func() error {
		return config.NodeMap.Put(config.NodeID, entry)
	}, retryLimit, retryInterval)

	return err
}

type nodeServer struct {
	config *DriverConfig
}

func newNodeServer(config *DriverConfig) *nodeServer {
	return &nodeServer{
		config: config,
	}
}

func (n *nodeServer) registerNodeEntry() error {
	return registerNodeEntry(n.config)
}

func (n *nodeServer) detectBlockVolumeConflict(device string, path string) error {
	var mountInfo *kmount.MountInfo

	mountInfo, err := n.config.Mount.GetMountRef(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to get %v mount ref, %v", path, err)
	}
	// compare device name
	if filepath.Base(mountInfo.Root) != filepath.Base(device) {
		return status.Errorf(codes.AlreadyExists,
			"%v source device %v alreadyExists not %v", path, mountInfo.Root, device)
	}

	return nil
}

func (n *nodeServer) mountBlockVolume(device string, path string, readonly bool) error {
	err := createFile(path)
	if err != nil {
		return err
	}

	isNotMountPoint, err := n.config.Mount.IsLikelyNotMountPoint(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to check mount point %v, %v", path, err)
	}

	if !isNotMountPoint {
		return n.detectBlockVolumeConflict(device, path)
	}

	mountOpts := []string{"bind"}
	if readonly {
		mountOpts = append(mountOpts, "ro")
	}

	err = n.config.Mount.Mount(device, path, "", mountOpts)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to bind %v to %v, %v", device, path, err)
	}

	return nil
}

func (n *nodeServer) detectMountVolumeConflict(device string, path string,
	volume *csi.VolumeCapability_MountVolume) error {
	mountFlags := volume.MountFlags

	fsType := volume.GetFsType()
	if len(fsType) == 0 {
		fsType = defaultFS
	}

	var mountPoint *kmount.MountPoint

	mountPoint, err := n.config.Mount.GetMountPoint(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to get %v mount point, %v", path, err)
	}
	// compare device name
	if mountPoint.Device != device {
		return status.Errorf(codes.AlreadyExists,
			"%v source device %v alreadyExists not %v", path, mountPoint.Device, device)
	}

	if mountPoint.Type != fsType {
		return status.Errorf(codes.AlreadyExists,
			"path %v fsType is %v not %v ", path, mountPoint.Type, fsType)
	}

	if !checkMountFlags(mountPoint.Opts, mountFlags) {
		return status.Errorf(codes.AlreadyExists,
			"%v actual mount flags %v is not compatible with %v",
			path, mountPoint.Opts, mountFlags)
	}

	return nil
}

func (n *nodeServer) bindMountVolume(source string, path string,
	volume *csi.VolumeCapability_MountVolume, readonly bool) error {
	err := createDir(path)
	if err != nil {
		return err
	}

	fsType := volume.GetFsType()
	if fsType == "" {
		fsType = defaultFS
	}

	isNotMountPoint, err := n.config.Mount.IsLikelyNotMountPoint(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to check mount point %v, %v", path, err)
	}

	mp, err := n.config.Mount.GetMountPoint(source)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to get source mount point %v, %v", path, err)
	}

	if !isNotMountPoint {
		return n.detectMountVolumeConflict(mp.Device, path, volume)
	}

	mountOpts := volume.GetMountFlags()

	if readonly {
		mountOpts = utils.MakeReadOnlyMountOpts(mountOpts)
	}

	mountOpts = append(mountOpts, "bind")

	err = n.config.Mount.Mount(source, path, fsType, mountOpts)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to bind %v to %v, %v", source, path, err)
	}

	return nil
}

func (n *nodeServer) mountMountVolume(device string, path string,
	volume *csi.VolumeCapability_MountVolume, readonly bool) error {
	err := createDir(path)
	if err != nil {
		return err
	}

	fsType := volume.GetFsType()
	if fsType == "" {
		fsType = defaultFS
	}

	mountOpts := volume.GetMountFlags()

	isNotMountPoint, err := n.config.Mount.IsLikelyNotMountPoint(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to check mount point %v, %v", path, err)
	}

	if !isNotMountPoint {
		return n.detectMountVolumeConflict(device, path, volume)
	}

	if readonly {
		mountOpts = utils.MakeReadOnlyMountOpts(mountOpts)
	}

	err = n.config.Mount.FormatAndMount(device, path, fsType, mountOpts)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to mount %v to %v, %v", device, path, err)
	}

	return nil
}

func (n *nodeServer) NodeStageVolume(
	ctx context.Context,
	req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is empty")
	}

	// TODO(tower): need to locate the specific device letter(/dev/vd?) through the properties of vmvolume in tower,
	// instead of using dmesg
	findDeviceCmd := "dmesg | grep virtio_blk | egrep -o 'vd.' | tail -1 | tr -d '\n'"
	output, err := commonutils.Cmd(findDeviceCmd)
	if err != nil {
		return nil, err
	}

	device := "/dev/" + output
	stagingPath := generateStagingPath(stagingTargetPath, volumeID)

	if req.GetVolumeCapability().GetBlock() != nil {
		err = n.mountBlockVolume(device, stagingPath, false)
	} else {
		err = n.mountMountVolume(device, stagingPath, req.GetVolumeCapability().GetMount(), false)
	}

	if err != nil {
		return nil, err
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (n *nodeServer) unmountVolume(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return status.Errorf(codes.Internal, "failed to stat %v, %v", path, err)
	}

	isNotMountPoint, err := n.config.Mount.IsLikelyNotMountPoint(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to check mount point %v, %v", path, err)
	}

	if !isNotMountPoint {
		err = n.config.Mount.Unmount(path)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to umount %v, %v", path, err)
		}
	}

	err = os.RemoveAll(path)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to remove %v, %v", path, err)
	}

	return nil
}

func (n *nodeServer) NodeUnstageVolume(
	ctx context.Context,
	req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is empty")
	}

	stagingPath := generateStagingPath(stagingTargetPath, volumeID)

	err := n.unmountVolume(stagingPath)
	if err != nil {
		return nil, err
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (n *nodeServer) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is empty")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability is empty")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"STAGE_UNSTAGE_VOLUME capability is set but no staging_target_path is empty")
	}

	// TODO(tower): need to locate the specific device letter(/dev/vd?) through the properties of vmvolume in tower,
	// instead of using dmesg
	findDeviceCmd := "dmesg | grep virtio_blk | egrep -o 'vd.' | tail -1 | tr -d '\n'"
	output, err := commonutils.Cmd(findDeviceCmd)
	if err != nil {
		return nil, err
	}

	device := "/dev/" + output
	stagingPath := generateStagingPath(stagingTargetPath, volumeID)

	if volumeCapability.GetBlock() != nil {
		err = n.mountBlockVolume(device, targetPath, req.GetReadonly())
	} else {
		err = n.bindMountVolume(stagingPath, targetPath, volumeCapability.GetMount(),
			req.GetReadonly())
	}

	if err != nil {
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *nodeServer) detectNodeMultiPublish(stagingPath string, targetPath string) error {
	paths, err := n.config.Mount.GetMountRefs(stagingPath)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get %v mount refs, %v", stagingPath, err)
	}

	for _, path := range paths {
		if path != targetPath {
			return status.Errorf(codes.AlreadyExists, "multi publish %+v", paths)
		}
	}

	return nil
}

func (n *nodeServer) NodeUnpublishVolume(
	ctx context.Context,
	req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volumeId is empty")
	}

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is empty")
	}

	err := n.unmountVolume(targetPath)
	if err != nil {
		return nil, err
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *nodeServer) NodeGetVolumeStats(
	ctx context.Context,
	req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, nil
}

func (n *nodeServer) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			},
		},
	}, nil
}

func (n *nodeServer) NodeGetInfo(
	ctx context.Context,
	req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId:            n.config.NodeID,
		MaxVolumesPerNode: 128,
	}, nil
}

// TODO(tower): implement NodeExpandVolume by tower sdk
func (n *nodeServer) NodeExpandVolume(
	ctx context.Context,
	req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, nil
}
