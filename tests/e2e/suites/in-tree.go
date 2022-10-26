// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package suites

import (
	"github.com/onsi/ginkgo"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	"github.com/smartxworks/elf-csi-driver/tests/e2e/driver"
)

var CSITestSuites = []func() storageframework.TestSuite{
	testsuites.InitDisruptiveTestSuite,
	testsuites.InitVolumesTestSuite,
	testsuites.InitVolumeIOTestSuite,
	testsuites.InitVolumeModeTestSuite,
	testsuites.InitSubPathTestSuite,
	testsuites.InitProvisioningTestSuite,
	testsuites.InitMultiVolumeTestSuite,
}

var _ = ginkgo.Describe("CSI Driver RWO volumes ", func() {
	var (
		scParameterGroups []map[string]string
		csiDriver         storageframework.TestDriver
	)

	scParameterGroups = NewStorageClassParameterGroups()
	csiDriver = driver.NewELFDriver(GetDriverName(), false, scParameterGroups)

	ginkgo.Context(storageframework.GetDriverNameWithFeatureTags(csiDriver), func() {
		storageframework.DefineTestSuites(csiDriver, CSITestSuites)
	})
})

var _ = ginkgo.Describe("CSI Driver RWX, RWO, ROX volumes", func() {
	var (
		scParameterGroups []map[string]string
		csiDriver         storageframework.TestDriver
	)
	scParameterGroups = NewStorageClassParameterGroups()
	csiDriver = driver.NewELFDriver(GetDriverName(), false, scParameterGroups)

	ginkgo.Context(storageframework.GetDriverNameWithFeatureTags(csiDriver), func() {
		storageframework.DefineTestSuites(csiDriver, CSITestSuites)
	})
})
