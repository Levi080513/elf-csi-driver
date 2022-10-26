// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
	kmount "k8s.io/utils/mount"

	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	maxAllowedVolumesPerNode = 60
)

type NodeServer interface {
	csi.NodeServer
	registerNodeEntry() error
	Run(stop <-chan struct{}) error
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

type GetDeviceByDiskSerialFunc func(serial string) (string, bool)

type nodeServer struct {
	config *DriverConfig

	deviceSerialCache *DeviceSerialCache

	GetDeviceByDiskSerialFuncMap map[models.Bus]GetDeviceByDiskSerialFunc
}

func newNodeServer(config *DriverConfig) *nodeServer {
	server := &nodeServer{
		config:                       config,
		deviceSerialCache:            NewDeviceSerialCache(),
		GetDeviceByDiskSerialFuncMap: make(map[models.Bus]GetDeviceByDiskSerialFunc),
	}

	// device ID_SERIAL is disk'serial prefix when volume attach to VIRTIO Bus,
	// so use GetDeviceByIDSerial to get device.
	// device ID_SCSI_SERIAL is disk'serial prefix when volume attach to SCSI Bus,
	// so use GetDeviceByIDSCSISerial to get device.
	server.GetDeviceByDiskSerialFuncMap[models.BusVIRTIO] = server.deviceSerialCache.getDeviceByIDSerial
	server.GetDeviceByDiskSerialFuncMap[models.BusSCSI] = server.deviceSerialCache.getDeviceByIDSCSISerial

	return server
}

func (n *nodeServer) Run(stopCh <-chan struct{}) error {
	return n.deviceSerialCache.Run(stopCh)
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

	device, err := n.getVolumeDevice(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("failed to get volume device. error %s", err.Error()))
	}

	stagingPath := generateStagingPath(stagingTargetPath, volumeID)

	if req.GetVolumeCapability().GetBlock() != nil {
		err = n.mountBlockVolume(*device, stagingPath, false)
	} else {
		err = n.mountMountVolume(*device, stagingPath, req.GetVolumeCapability().GetMount(), false)
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

	device, err := n.getVolumeDevice(volumeID)
	if err != nil {
		return nil, err
	}

	stagingPath := generateStagingPath(stagingTargetPath, volumeID)

	if volumeCapability.GetBlock() != nil {
		err = n.mountBlockVolume(*device, targetPath, req.GetReadonly())
	} else {
		err = n.bindMountVolume(stagingPath, targetPath, volumeCapability.GetMount(),
			req.GetReadonly())
	}

	if err != nil {
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
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
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "the volume id parameter is required")
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "the volume path parameter is required")
	}

	isBlockVolume, err := isBlockDevice(volumePath)
	if err != nil {
		// ENOENT means the volumePath does not exist
		// See https://man7.org/linux/man-pages/man2/stat.2.html for details.
		if errors.Is(err, unix.ENOENT) {
			return nil, status.Errorf(codes.NotFound, "the volume %v is not mounted on the path %v", volumeID, volumePath)
		}

		return nil, status.Errorf(codes.Internal, "failed to check volume mode for volume path %v: %v", volumePath, err)
	}

	if isBlockVolume {
		vmVolume, getVMVolumeErr := n.config.TowerClient.GetVMVolumeByID(volumeID)
		if getVMVolumeErr != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get VM Volume %v, %v", volumeID, getVMVolumeErr))
		}

		volumeByteUsage := &csi.VolumeUsage{
			Total:     *vmVolume.Size,
			Used:      *vmVolume.UniqueSize,
			Available: *vmVolume.Size - *vmVolume.UniqueSize,
			Unit:      csi.VolumeUsage_BYTES,
		}

		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				volumeByteUsage,
			},
		}, nil
	}

	stats, err := getVolumeFilesystemStats(volumePath)
	if err != nil {
		// ENOENT means the volumePath does not exist
		// See http://man7.org/linux/man-pages/man2/statfs.2.html for details.
		if errors.Is(err, unix.ENOENT) {
			return nil, status.Errorf(codes.NotFound, "the volume %v is not mounted on the path %v", volumeID, volumePath)
		}

		return nil, status.Errorf(codes.Internal, "failed to retrieve capacity statistics for volume path %v: %v", volumePath, err)
	}

	volumeByteUsage := &csi.VolumeUsage{
		Available: stats.availableBytes,
		Total:     stats.totalBytes,
		Used:      stats.usedBytes,
		Unit:      csi.VolumeUsage_BYTES,
	}

	volumeInodeUsage := &csi.VolumeUsage{
		Available: stats.availableInodes,
		Total:     stats.totalInodes,
		Used:      stats.usedInodes,
		Unit:      csi.VolumeUsage_INODES,
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			volumeByteUsage,
			volumeInodeUsage,
		},
	}, nil
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
		MaxVolumesPerNode: maxAllowedVolumesPerNode,
	}, nil
}

func (n *nodeServer) NodeExpandVolume(
	ctx context.Context,
	req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is empty")
	}

	volume, err := n.config.TowerClient.GetVMVolumeByID(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to find volume %v", volumeID)
	}

	device, err := n.getVolumeDevice(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to find volume %+v device, %v", volumeID, err)
	}

	err = n.config.Resizer.ResizeBlock(*device, uint64(*volume.Size))
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to resize device %v, %v", device, err)
	}

	fsType, err := n.config.Mount.GetDiskFormat(*device)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to get device %v fs type, %v", device, err)
	}

	if fsType != "" {
		err = n.config.Resizer.ResizeFS(*device, fsType)
		if err != nil {
			return nil, status.Errorf(codes.Internal,
				"failed to resize device %v fs %v , %v", device, fsType, err)
		}
	}

	return &csi.NodeExpandVolumeResponse{
		CapacityBytes: *volume.Size,
	}, nil
}

func (n *nodeServer) getVMVolumeSerial(volumeID string) (*string, error) {
	vmDisks, err := n.config.TowerClient.GetVMDisks(n.config.NodeID, []string{volumeID})
	if err != nil {
		return nil, err
	}

	if len(vmDisks) == 0 {
		return nil, fmt.Errorf("failed to get VM Disk, VM Disk is not found for volume %s node %s", volumeID, n.config.NodeID)
	}

	serial := vmDisks[0].Serial
	if serial == nil || *serial == "" {
		return nil, fmt.Errorf("unable to get serial from Elf API in VM %v with volume %v", n.config.NodeID, volumeID)
	}

	return serial, nil
}

func (n *nodeServer) getVMVolumeZBSVolumeID(volumeID string) (*string, error) {
	volume, err := n.config.TowerClient.GetVMVolumeByID(volumeID)
	if err != nil {
		return nil, err
	}

	if volume.Lun == nil || volume.Lun.ID == nil {
		return nil, fmt.Errorf("volume %s Lun ID is not found", volumeID)
	}

	iscsiLuns, err := n.config.TowerClient.GetISCSILuns([]string{*volume.Lun.ID})
	if err != nil {
		return nil, err
	}

	if len(iscsiLuns) == 0 {
		return nil, fmt.Errorf("failed to get iscsi lun relate volume %v with iscsi lun %v ", volumeID, volume.Lun.ID)
	}

	return iscsiLuns[0].ZbsVolumeID, nil
}

// getVolumeAttachedBus is func to get VM Bus which volume attach to.
func (n *nodeServer) getVolumeAttachedBus(volumeID string) (models.Bus, error) {
	vmDisks, err := n.config.TowerClient.GetVMDisks(n.config.NodeID, []string{volumeID})
	if err != nil {
		return "", err
	}

	if len(vmDisks) == 0 {
		return "", fmt.Errorf("failed to get VM %v disk for volume %v", n.config.NodeID, volumeID)
	}

	disk := vmDisks[0]
	if disk.Bus == nil {
		return "", fmt.Errorf("failed to get bus for disk %v", disk.ID)
	}

	return *disk.Bus, nil
}

func (n *nodeServer) getVolumeDevice(volumeID string) (*string, error) {
	device := ""

	serial, err := n.getVMVolumeSerial(volumeID)
	if err != nil {
		return nil, err
	}

	zbsVolumeID, err := n.getVMVolumeZBSVolumeID(volumeID)
	if err != nil {
		return nil, err
	}

	volumeAttachedBus, err := n.getVolumeAttachedBus(volumeID)
	if err != nil {
		return nil, err
	}

	getDeviceByDiskSerialFunc, ok := n.GetDeviceByDiskSerialFuncMap[volumeAttachedBus]
	if !ok {
		return nil, fmt.Errorf("failed to get device, attach bus %s is not support", volumeAttachedBus)
	}

	// In ELF cluster vhost mode, SERIAL may be the ZBSVolumeID corresponding to the volume,
	// as long as there is a match between serial and ZBSVolumeID,
	// the device corresponding to the mounted volume can be confirmed.
	if targetDevice, ok := getDeviceByDiskSerialFunc(*serial); ok {
		device = targetDevice
	}

	if targetDevice, ok := getDeviceByDiskSerialFunc(*zbsVolumeID); ok {
		device = targetDevice
	}

	if device == "" {
		// The device may not be found because the cache is not up to date，
		// resync device serial cache.
		resyncErr := n.deviceSerialCache.resyncCache()
		if err != nil {
			klog.Errorf("failed to resync device serial cache. error %v", resyncErr.Error())
		}

		return nil, fmt.Errorf("failed to get device, disk serial %s, zbs volume id %s", *serial, *zbsVolumeID)
	}

	return &device, nil
}
