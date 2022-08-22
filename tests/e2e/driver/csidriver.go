// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"fmt"
	"math/rand"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eskipper "k8s.io/kubernetes/test/e2e/framework/skipper"
	e2evolume "k8s.io/kubernetes/test/e2e/framework/volume"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
	"k8s.io/kubernetes/test/e2e/storage/utils"
)

type zbsDriver struct {
	driverInfo      storageframework.DriverInfo
	rwx             bool
	parameterGroups []map[string]string
}

// newZBSDriver returns zbsDriver that implements TestDriver interface
func NewZBSDriver(name string, rwx bool, parameterGroups []map[string]string) storageframework.TestDriver {
	var requiredAccessModes []corev1.PersistentVolumeAccessMode
	if rwx {
		requiredAccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
	} else {
		requiredAccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	return &zbsDriver{
		driverInfo: storageframework.DriverInfo{
			Name:        name,
			MaxFileSize: storageframework.FileSizeLarge,
			SupportedFsType: sets.NewString(
				"ext2",
				"ext3",
				"ext4",
				"xfs",
				// empty meaning csi default fs type
				"",
			),
			Capabilities: map[storageframework.Capability]bool{
				storageframework.CapPersistence:         true,
				storageframework.CapBlock:               true,
				storageframework.CapExec:                !rwx,
				storageframework.CapSnapshotDataSource:  true,
				storageframework.CapPVCDataSource:       true,
				storageframework.CapMultiPODs:           false,
				storageframework.CapRWX:                 rwx,
				storageframework.CapControllerExpansion: true,
				storageframework.CapNodeExpansion:       true,
				storageframework.CapSingleNodeVolume:    true,
			},
			SupportedSizeRange: e2evolume.SizeRange{
				Min: "2Gi",
				Max: "4Gi",
			},
			RequiredAccessModes: requiredAccessModes,
		},
		rwx:             rwx,
		parameterGroups: parameterGroups,
	}
}

var _ storageframework.TestDriver = &zbsDriver{}
var _ storageframework.DynamicPVTestDriver = &zbsDriver{}
var _ storageframework.SnapshottableTestDriver = &zbsDriver{}
var _ storageframework.AuthTestDriver = &zbsDriver{}

func (driver *zbsDriver) GetDriverInfo() *storageframework.DriverInfo {
	return &driver.driverInfo
}

func (driver *zbsDriver) SkipUnsupportedTest(pattern storageframework.TestPattern) {
	if driver.rwx {
		if pattern.VolMode != storageframework.BlockVolModeDynamicPV.VolMode {
			e2eskipper.Skipf("Driver %v doesn't support %+v multi access", driver.driverInfo.Name, pattern)
		}
	}
}

func (driver *zbsDriver) GetDynamicProvisionStorageClass(config *storageframework.PerTestConfig,
	fsType string) *storagev1.StorageClass {
	provisioner := driver.driverInfo.Name

	var parameters map[string]string = make(map[string]string)
	if len(driver.parameterGroups) > 0 {
		parameters = driver.parameterGroups[rand.Intn(len(driver.parameterGroups))]
	}

	if fsType != "" {
		parameters["csi.storage.k8s.io/fstype"] = fsType
	} else {
		parameters["csi.storage.k8s.io/fstype"] = ""
	}

	ns := config.Framework.Namespace.Name

	return storageframework.GetStorageClass(provisioner, parameters, nil, ns)
}

func (driver *zbsDriver) PrepareTest(f *framework.Framework) (*storageframework.PerTestConfig, func()) {
	config := &storageframework.PerTestConfig{
		Driver:    driver,
		Prefix:    "zbs",
		Framework: f,
	}

	return config, func() {}
}

func (driver *zbsDriver) GetSnapshotClass(config *storageframework.PerTestConfig, parameters map[string]string) *unstructured.Unstructured {
	snapshotter := driver.driverInfo.Name
	ns := config.Framework.Namespace.Name
	suffix := fmt.Sprintf("%s-vsc", config.GetUniqueDriverName())

	return utils.GenerateSnapshotClassSpec(snapshotter, parameters, ns, suffix)
}

// AuthTestDriver GetStorageClassAuthParameters interface
func (driver *zbsDriver) GetStorageClassAuthParameters(config *storageframework.PerTestConfig) map[string]string {
	return map[string]string{
		"auth": "true",
		storageframework.CSIControllerPublishSecretName.ToString(): "controller-publish-secret",
		storageframework.CSIControllerPublishSecretNS.ToString():   config.Framework.Namespace.Name,
		storageframework.CSINodeStageSecretName.ToString():         "node-stage-secret",
		storageframework.CSINodeStageSecretNS.ToString():           config.Framework.Namespace.Name,
	}
}

// AuthTestDriver GetAuthSecretData interface
func (driver *zbsDriver) GetAuthSecretData() []map[string]string {
	return []map[string]string{
		{
			"username": "iomesh-A",
			"password": "123456789qwer",
		},
		{
			"username": "iomesh-B",
			"password": "tyuiop[]asdf",
		},
	}
}

// AuthTestDriver GetAuthMatchGroup interface
func (driver *zbsDriver) GetAuthMatchGroup() [][]storageframework.CSIStorageClassAuthParamKey {
	return [][]storageframework.CSIStorageClassAuthParamKey{
		{
			storageframework.CSINodePublishSecretName,
			storageframework.CSINodeStageSecretName,
		},
	}
}
