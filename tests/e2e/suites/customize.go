// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package suites

import (
	"strconv"
	"time"

	"github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/smartxworks/elf-csi-driver/pkg/testing/utils"
)

const (
	csiDriverName = "com.iomesh.csi-driver"
	pvcNamePrefix = "pvc-test-"
)

var scParameterGroups = map[string]string{
	"replicaFactor": "2",
	"thinProvision": "true",
}

var _ = ginkgo.Describe("Customized Cases", func() {
	f := framework.NewDefaultFramework("custom")
	suites := NewCustomSuites(f)

	suites.DefineTests()
})

type customSuites struct {
	// The framework provides the basic functions needed in the k8s e2e test, such as a clientset,
	// automatic creation and cleaning of namespaces, etc.
	*framework.Framework
}

func NewCustomSuites(framework *framework.Framework) *customSuites {
	return &customSuites{
		Framework: framework,
	}
}

func (c *customSuites) DefineTests() {
	ginkgo.It("[custom] create 100,000 pvc simultaneously", func() {
		pvcNum := 100000

		sc, err := utils.CreateStorageClass(c.ClientSet, csiDriverName, scParameterGroups, nil, c.Namespace.Name)
		framework.ExpectNoError(err)

		// create 100,000 pvcs
		pvcNames := make([]string, pvcNum)
		for i := 0; i < pvcNum; i++ {
			pvc, createErr := utils.CreatePVC(c.ClientSet, pvcNamePrefix+strconv.Itoa(i), c.Namespace.Name,
				sc.Name, "1Gi", corev1.PersistentVolumeFilesystem)
			framework.ExpectNoError(createErr)

			pvcNames[i] = pvc.Name
		}

		// wait all pvc success bound pv
		err = utils.WaitForPersistentVolumeClaimsPhase(c.ClientSet, corev1.ClaimBound, c.Namespace.Name,
			pvcNames, 5*time.Second, 1*time.Hour)
		framework.ExpectNoError(err)
	})
}
