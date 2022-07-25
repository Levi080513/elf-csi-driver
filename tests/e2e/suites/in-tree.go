// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package suites

import (
	"os"

	"github.com/onsi/ginkgo"
	"k8s.io/klog"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	"github.com/iomesh/csi-driver/tests/e2e/driver"
)

var CSITestSuites = []func() storageframework.TestSuite{
	testsuites.InitDisruptiveTestSuite,
	testsuites.InitVolumesTestSuite,
	testsuites.InitVolumeIOTestSuite,
	testsuites.InitVolumeModeTestSuite,
	testsuites.InitSubPathTestSuite,
	testsuites.InitProvisioningTestSuite,
	testsuites.InitSnapshottableTestSuite,
	testsuites.InitVolumeExpandTestSuite,
	testsuites.InitMultiVolumeTestSuite,
	testsuites.InitAuthTestSuite,
}

var parameterGroups = []map[string]string{
	{
		"replicaFactor": "2",
		"thinProvision": "true",
	},
	{
		"replicaFactor": "3",
		"thinProvision": "true",
	},
	{
		"replicaFactor": "2",
		"thinProvision": "false",
	},
	{
		"replicaFactor": "3",
		"thinProvision": "false",
	},
}

func getDriverName() string {
	driverName, ok := os.LookupEnv("DRIVER_NAME")
	if !ok {
		klog.Fatal("failed to get DRIVER_NAME")
	}

	return driverName
}

var _ = ginkgo.Describe("CSI Driver RWO volumes ", func() {
	csiDriver := driver.NewZBSDriver(getDriverName(), false, parameterGroups)
	ginkgo.Context(storageframework.GetDriverNameWithFeatureTags(csiDriver), func() {
		storageframework.DefineTestSuites(csiDriver, CSITestSuites)
	})
})

var _ = ginkgo.Describe("CSI Driver RWX, RWO, ROX volumes", func() {
	csiDriver := driver.NewZBSDriver(getDriverName(), true, parameterGroups)
	ginkgo.Context(storageframework.GetDriverNameWithFeatureTags(csiDriver), func() {
		storageframework.DefineTestSuites(csiDriver, CSITestSuites)
	})
})
