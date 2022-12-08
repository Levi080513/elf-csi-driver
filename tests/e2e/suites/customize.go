// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package suites

import (
	"context"
	"crypto/rand"
	"math/big"
	"strconv"
	"time"

	"github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
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

	// Test Process:
	// 1. Create 60 PVC
	// 2. Create 6 Pod which mounted 10 PVC and scheduler to one node, wait for all pods running, then delete all pods.
	// 3. Create 6 Pod which mounted 10 PVC and scheduler to another one node, wait for all pods running, then delete all pods.
	ginkgo.It("[custom] simulates KSC Cluster Create and Upgrade case", func() {
		pvcNum := 60

		scParameterGroups := NewStorageClassParameterGroups()
		r, _ := rand.Int(rand.Reader, big.NewInt(int64(len(scParameterGroups))))
		parameters := scParameterGroups[int(r.Int64())]

		sc, err := utils.CreateStorageClass(c.ClientSet, csiDriverName, parameters, nil, c.Namespace.Name)
		framework.ExpectNoError(err)

		// create 60 pvcs
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

		// scheduler pods to one node to simulates KSC create.
		schedulerNode := testCreatePodsWithTheSameNumberPVC(c.Framework, c.ClientSet, mountPvcs, 10, nil)

		// scheduler pods to another one node to simulates KSC upgrade.
		node := &e2epod.NodeSelection{}
		e2epod.SetAntiAffinity(node, schedulerNode)
		testCreatePodsWithTheSameNumberPVC(c.Framework, c.ClientSet, mountPvcs, 10, node)
	})
}

func testCreatePodsWithTheSameNumberPVC(f *framework.Framework, cs clientset.Interface, mountPVCs []*corev1.PersistentVolumeClaim,
	mountNumber int, node *e2epod.NodeSelection) string {
	pvcNum := len(mountPVCs)
	// every test pod mount 10 pvc
	podNum := pvcNum / mountNumber
	testPods := make([]corev1.Pod, podNum)
	actualNodeName := ""

	if node == nil {
		node = &e2epod.NodeSelection{}
	}

	createTimeout := time.Minute * 5
	ctx, _ := context.WithTimeout(context.Background(), createTimeout)
	// create pods scheduler to one node
	for i := 0; i < podNum; i++ {
		curPodMountPvcsStartIndex := i * 10
		curPodMountPvcsEndIndex := (i + 1) * 10
		// create pod which mount 10 pvcs
		podConfig := e2epod.Config{
			PVCs:          mountPVCs[curPodMountPvcsStartIndex:curPodMountPvcsEndIndex],
			NS:            f.Namespace.Name,
			NodeSelection: *node,
		}

		pod, err := e2epod.MakeSecPod(&podConfig)
		framework.ExpectNoError(err)

		pod, err = cs.CoreV1().Pods(podConfig.NS).Create(ctx, pod, metav1.CreateOptions{})
		framework.ExpectNoError(err)

		testPods[i] = *pod

		err = utils.WaitTimeoutForPodScheduled(cs, *pod, time.Minute)
		framework.ExpectNoError(err)

		pod, err = cs.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		framework.ExpectNoError(err)

		// set pod affinity to make all pod scheduler to one node.
		if i == 0 {
			node = &e2epod.NodeSelection{}
			actualNodeName = pod.Spec.NodeName
			e2epod.SetAffinity(node, actualNodeName)
		}
	}

	// wait all pod running.
	err := utils.WaitTimeoutForPodsRunning(cs, testPods, time.Minute*10)
	framework.ExpectNoError(err)

	// delete all test pods immediately
	err = e2epod.DeletePodsWithGracePeriod(cs, testPods, 0)
	framework.ExpectNoError(err)

	return actualNodeName
}
