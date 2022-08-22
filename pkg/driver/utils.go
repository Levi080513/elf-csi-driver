// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"fmt"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

	for _, c := range volumeCapabilities {
		if c == nil {
			return status.Error(codes.InvalidArgument, "volumeCapablities is empty")
		}

		accessMode := c.GetAccessMode()
		if accessMode == nil {
			return status.Error(codes.InvalidArgument, "access mode is empty")
		}

		if c.GetMount() != nil {
			_, ok := mountModeAccessModes[accessMode.GetMode()]
			if !ok {
				return status.Errorf(codes.InvalidArgument,
					"mount mode volume does not support access mode %v",
					accessMode.String())
			}

			fsType := c.GetMount().GetFsType()
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

func getNodeIP() (string, error) {
	// get NODE_IP env injected by yaml
	nodeIP, ok := os.LookupEnv("NODE_IP")
	if !ok {
		return "", fmt.Errorf("failed to lookup NODE_IP env")
	}

	return nodeIP, nil
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

func getStoragePolicy(params map[string]string) (*models.VMVolumeElfStoragePolicyType, error) {
	defaultStoragePolicy := models.VMVolumeElfStoragePolicyTypeREPLICA2THINPROVISION

	spStr, ok := params[StoragePolicy]
	if !ok {
		return &defaultStoragePolicy, nil
	}
	sp := defaultStoragePolicy
	switch spStr {
	case "REPLICA_2_THIN_PROVISION":
		sp = models.VMVolumeElfStoragePolicyTypeREPLICA2THINPROVISION
	case "REPLICA_3_THIN_PROVISION":
		sp = models.VMVolumeElfStoragePolicyTypeREPLICA3THINPROVISION
	case "REPLICA_2_THICK_PROVISION":
		sp = models.VMVolumeElfStoragePolicyTypeREPLICA2THICKPROVISION
	case "REPLICA_3_THICK_PROVISION":
		sp = models.VMVolumeElfStoragePolicyTypeREPLICA3THICKPROVISION
	}
	return &sp, nil
}

var accessModesNeedSharing = map[csi.VolumeCapability_AccessMode_Mode]bool{
	csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:  false,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:       false,
	csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:  true,
	csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:   true,
	csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER: true,
}

func checkNeedSharing(caps []*csi.VolumeCapability) (bool, error) {
	needSharing := false
	for _, c := range caps {
		mode := c.GetAccessMode().GetMode()
		sharing, ok := accessModesNeedSharing[mode]
		if !ok {
			return false, status.Errorf(codes.InvalidArgument,
				"unknown access mode %v",
				mode.String())
		}
		if sharing {
			needSharing = true
			break
		}
	}
	return needSharing, nil
}
