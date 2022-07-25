// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zbs_proto "github.com/iomesh/zbs-client-go/gen/proto/zbs"
	"github.com/iomesh/zbs-client-go/gen/proto/zbs/meta"
	"github.com/iomesh/zbs-client-go/zbs"
	zbsError "github.com/iomesh/zbs-client-go/zbs/error"
)

type volumeFilesystemStats struct {
	availableBytes int64
	totalBytes     int64
	usedBytes      int64

	availableInodes int64
	totalInodes     int64
	usedInodes      int64
}

func roundUp(num uint64, base uint64) uint64 {
	div := num / base

	mod := num % base

	if mod > 0 {
		div++
	}

	return div * base
}

func getVolumeSize(capablitiesRange *csi.CapacityRange) (uint64, error) {
	if capablitiesRange.GetRequiredBytes() < 0 && capablitiesRange.GetLimitBytes() < 0 {
		return 0,
			status.Error(codes.InvalidArgument,
				fmt.Sprintf("volume capcity range error %+v", capablitiesRange))
	}

	var size uint64 = defaultVolumeSize
	if capablitiesRange.GetRequiredBytes() > 0 {
		size = roundUp(uint64(capablitiesRange.GetRequiredBytes()), GB)
	} else if capablitiesRange.GetLimitBytes() > 0 {
		size = roundUp(uint64(capablitiesRange.GetLimitBytes()), GB)
	}

	return size, nil
}

func checkVolumeCapabilities(volumeCapabilities []*csi.VolumeCapability) error {
	if len(volumeCapabilities) == 0 {
		return status.Error(codes.InvalidArgument, "volumeCapablities is empty")
	}

	for _, cap := range volumeCapabilities {
		if cap == nil {
			return status.Error(codes.InvalidArgument, "volumeCapablities is empty")
		}

		accessMode := cap.GetAccessMode()
		if accessMode == nil {
			return status.Error(codes.InvalidArgument, "access mode is empty")
		}

		if cap.GetMount() != nil {
			_, ok := mountModeAccessModes[accessMode.GetMode()]
			if !ok {
				return status.Errorf(codes.InvalidArgument,
					"mount mode volume does not support access mode %v",
					accessMode.String())
			}

			fsType := cap.GetMount().GetFsType()
			if fsType == "" {
				fsType = defaultFS
			}

			_, ok = supportedFS[fsType]
			if !ok {
				return status.Errorf(codes.InvalidArgument, "unsupported fs %v", fsType)
			}
		} else {
			_, ok := blockModeAccessModes[accessMode.GetMode()]
			if !ok {
				return status.Errorf(codes.InvalidArgument,
					"block mode volume does not support %v", accessMode.String())
			}
		}
	}

	return nil
}

func isNotFound(err error) bool {
	if zbsErr, ok := err.(*zbsError.Error); ok {
		return zbsErr.EC() == zbs_proto.ErrorCode_ENotFound
	}

	return false
}

func isDuplicate(err error) bool {
	if zbsErr, ok := err.(*zbsError.Error); ok {
		return zbsErr.EC() == zbs_proto.ErrorCode_EDuplicate
	}

	return false
}

func makeListener(socketaddr string) (net.Listener, error) {
	serverURL, err := url.Parse(socketaddr)
	if err != nil {
		return nil, fmt.Errorf("parse socket addr %v, %v", socketaddr, err)
	}

	serverAddr := path.Join(serverURL.Host, filepath.FromSlash(serverURL.Path))
	if serverURL.Host == "" {
		serverAddr = filepath.FromSlash(serverURL.Path)
	}

	if serverURL.Scheme == "unix" {
		if err = os.Remove(serverAddr); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove unix domain socket file %v,  %v", serverAddr, err)
		}
	}

	listener, err := net.Listen(serverURL.Scheme, serverAddr)
	if err != nil {
		return nil, fmt.Errorf("listen %v,  %v", serverAddr, err)
	}

	return listener, nil
}

func detectLunConflict(lun *zbs.ISCSILun, size uint64, sp *zbs.StoragePolicy) error {
	err := status.Error(codes.AlreadyExists,
		fmt.Sprintf("created lun %+v conflict with req (size: %v, sp: %+v)",
			lun, size, sp))

	if size != lun.GetSize() {
		return err
	}

	if sp.ReplicaFactor != lun.GetReplicaNum() {
		return err
	}

	if sp.ThinProvision != lun.GetThinProvision() {
		return err
	}

	if sp.StripeSize != lun.GetStripeSize() {
		return err
	}

	if sp.StripeNum != lun.GetStripeNum() {
		return err
	}

	return nil
}

func needAuth(params map[string]string) bool {
	auth, ok := params["auth"]
	if ok && auth == "true" {
		return true
	}

	return false
}

func checkIqnExist(iqns []string, iqn string) bool {
	for i := range iqns {
		if iqn == iqns[i] {
			return true
		}
	}

	return false
}

func FindInitiatorChapByIqn(initiatorChap []*meta.InitiatorChapInfo, iqn string) *meta.InitiatorChapInfo {
	for i := range initiatorChap {
		if iqn == string(initiatorChap[i].Iqn) {
			return initiatorChap[i]
		}
	}

	return nil
}

func getNodeIP() (string, error) {
	// get NODE_IP env injected by yaml
	nodeIP, ok := os.LookupEnv("NODE_IP")
	if !ok {
		return "", fmt.Errorf("failed to lookup NODE_IP env")
	}

	return nodeIP, nil
}

func isSingleAccessMode(accessMode csi.VolumeCapability_AccessMode_Mode) bool {
	return accessMode == csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER ||
		accessMode == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY
}

func generateStagingPath(stagingTargetPath string, device string) string {
	return filepath.Join(stagingTargetPath, filepath.Base(device))
}

func checkMountFlags(actualFlags []string, expectFlags []string) bool {
	flagsMap := make(map[string]bool)
	for _, flag := range actualFlags {
		flagsMap[flag] = true
	}

	for _, flag := range expectFlags {
		_, ok := flagsMap[flag]
		if !ok {
			return false
		}
	}

	return true
}

func createDir(path string) error {
	fileInfo, err := os.Stat(path)

	if err == nil {
		if !fileInfo.IsDir() {
			return status.Errorf(codes.AlreadyExists,
				"%v is not dir", path)
		}

		return nil
	} else if !os.IsNotExist(err) {
		return status.Errorf(codes.Internal, "stat %v, %v", path, err)
	}

	err = os.MkdirAll(path, os.ModePerm)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to mkdir %v", path)
	}

	return nil
}

func createFile(path string) error {
	fileInfo, err := os.Stat(path)

	if err == nil {
		if fileInfo.IsDir() {
			return status.Errorf(codes.AlreadyExists,
				"%v is dir not file", path)
		}

		return nil
	} else if !os.IsNotExist(err) {
		return status.Errorf(codes.Internal, "stat %v, %v", path, err)
	}

	f, err := os.Create(path)
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to create %v file", path)
	}

	_ = f.Close()

	return nil
}

func generatorIqn(nodeID string, lunUuid string) string {
	return fmt.Sprintf("iqn.2020-05.com.iomesh:%s-%s", nodeID, lunUuid)
}

// k8s secrets -> InitiatorChapInfo
func generatorChapInfo(secrets map[string]string, iqn string) (*zbs.InitiatorChapInfo, error) {
	var chapInfo *zbs.InitiatorChapInfo

	chapUsername, usernameExist := secrets["username"]
	chapPassword, passwordExist := secrets["password"]

	if usernameExist && passwordExist {
		if len(chapPassword) < 12 || len(chapPassword) > 16 {
			return nil, errors.New("Password length must be a minimum of 12 characters and a maximum of 16 characters.")
		}

		enableChap := true
		chapInfo = &zbs.InitiatorChapInfo{
			Iqn:      iqn,
			ChapName: chapUsername,
			Secret:   chapPassword,
			Enable:   &enableChap,
		}
	}

	return chapInfo, nil
}

// matchLabel judge if labels contains special label
func matchLabel(labels *zbs_proto.Labels, label *zbs_proto.Label) bool {
	if labels == nil || label == nil {
		return false
	}

	for _, l := range labels.Labels {
		if string(label.Key) == string(l.Key) && string(label.Value) == string(l.Value) {
			return true
		}
	}

	return false
}

func isBlockDevice(volumePath string) (bool, error) {
	var stat unix.Stat_t
	// See https://man7.org/linux/man-pages/man2/stat.2.html for details
	err := unix.Stat(volumePath, &stat)
	if err != nil {
		return false, err
	}

	// See https://man7.org/linux/man-pages/man7/inode.7.html for detail
	if (stat.Mode & unix.S_IFMT) == unix.S_IFBLK {
		return true, nil
	}

	return false, nil
}

func getVolumeFilesystemStats(volumePath string) (*volumeFilesystemStats, error) {
	var statfs unix.Statfs_t
	// See http://man7.org/linux/man-pages/man2/statfs.2.html for details.
	err := unix.Statfs(volumePath, &statfs)
	if err != nil {
		return nil, err
	}

	stats := &volumeFilesystemStats{
		availableBytes: int64(statfs.Bavail) * statfs.Bsize,
		totalBytes:     int64(statfs.Blocks) * statfs.Bsize,
		usedBytes:      (int64(statfs.Blocks) - int64(statfs.Bfree)) * statfs.Bsize,

		availableInodes: int64(statfs.Ffree),
		totalInodes:     int64(statfs.Files),
		usedInodes:      int64(statfs.Files) - int64(statfs.Ffree),
	}

	return stats, nil
}
