// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	e2epv "k8s.io/kubernetes/test/e2e/framework/pv"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
)

func CreateStorageClass(c clientset.Interface, provisioner string, parameters map[string]string,
	bindingMode *storagev1.VolumeBindingMode, ns string) (*storagev1.StorageClass, error) {
	sc := storageframework.GetStorageClass(provisioner, parameters, bindingMode, ns)
	sc, err := c.StorageV1().StorageClasses().Create(context.TODO(), sc, metav1.CreateOptions{})

	return sc, err
}

func CreatePVC(c clientset.Interface, name, namespace string, storageClassName string,
	claimSize string, volumeMode corev1.PersistentVolumeMode) (*corev1.PersistentVolumeClaim, error) {
	pvc := e2epv.MakePersistentVolumeClaim(e2epv.PersistentVolumeClaimConfig{
		Name:             name,
		StorageClassName: &storageClassName,
		ClaimSize:        claimSize,
		VolumeMode:       &volumeMode,
	}, namespace)
	pvc, err := c.CoreV1().PersistentVolumeClaims(namespace).Create(context.TODO(), pvc, metav1.CreateOptions{})

	return pvc, err
}

func WaitForPersistentVolumeClaimsPhase(c clientset.Interface, phase corev1.PersistentVolumeClaimPhase, ns string, pvcNames []string, poll, timeout time.Duration) error {
	if len(pvcNames) == 0 {
		return fmt.Errorf("Incorrect parameter: Need at least one PVC to track. Found 0")
	}

	for start := time.Now(); time.Since(start) < timeout; time.Sleep(poll) {
		phaseFoundInAllClaims := true

		for _, pvcName := range pvcNames {
			pvc, err := c.CoreV1().PersistentVolumeClaims(ns).Get(context.TODO(), pvcName, metav1.GetOptions{})

			if err != nil {
				return fmt.Errorf("unable get pvc: %v, ns: %v", pvcName, ns)
			}

			if pvc.Status.Phase != phase {
				phaseFoundInAllClaims = false
				break
			}
		}

		if phaseFoundInAllClaims {
			return nil
		}
	}

	return fmt.Errorf("pvcs not all in phase %s within %v", phase, timeout)
}
