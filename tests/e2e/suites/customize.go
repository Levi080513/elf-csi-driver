// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package suites

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"time"

	"github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"

	"github.com/smartxworks/elf-csi-driver/pkg/testing/utils"
)

const (
	csiDriverName = "com.smartx.elf-csi-driver"
	pvcNamePrefix = "pvc-test-"
)

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
	ginkgo.It("[custom] create 100 pvc simultaneously", func() {
		pvcNum := 100

		scParameterGroups := NewStorageClassParameterGroups()
		r, _ := rand.Int(rand.Reader, big.NewInt(int64(len(scParameterGroups))))
		parameters := scParameterGroups[int(r.Int64())]

		sc, err := utils.CreateStorageClass(c.ClientSet, csiDriverName, parameters, nil, c.Namespace.Name)
		framework.ExpectNoError(err)

		// create 100 pvcs
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

	ginkgo.It("[custom] create pod which mount 10 pvc", func() {
		pvcNum := 10

		scParameterGroups := NewStorageClassParameterGroups()
		r, _ := rand.Int(rand.Reader, big.NewInt(int64(len(scParameterGroups))))
		parameters := scParameterGroups[int(r.Int64())]

		sc, err := utils.CreateStorageClass(c.ClientSet, csiDriverName, parameters, nil, c.Namespace.Name)
		framework.ExpectNoError(err)

		// create 10 pvcs
		pvcNames := make([]string, pvcNum)
		mountPvcs := make([]*corev1.PersistentVolumeClaim, pvcNum)
		for i := 0; i < pvcNum; i++ {
			pvc, createErr := utils.CreatePVC(c.ClientSet, pvcNamePrefix+strconv.Itoa(i), c.Namespace.Name,
				sc.Name, "1Gi", corev1.PersistentVolumeFilesystem)
			framework.ExpectNoError(createErr)

			pvcNames[i] = pvc.Name
			mountPvcs[i] = pvc
		}

		// wait all pvc success bound pv
		err = utils.WaitForPersistentVolumeClaimsPhase(c.ClientSet, corev1.ClaimBound, c.Namespace.Name,
			pvcNames, 5*time.Second, 5*time.Minute)
		framework.ExpectNoError(err)

		podConfig := e2epod.Config{
			PVCs: mountPvcs,
			NS:   c.Namespace.Name,
		}

		// create pod which mount 10 pvcs, wait pod running
		pod, err := e2epod.CreateSecPod(c.ClientSet, &podConfig, time.Minute*5)
		defer func() {
			framework.ExpectNoError(e2epod.DeletePodWithWait(c.ClientSet, pod))
		}()
		framework.ExpectNoError(err)
	})
}
