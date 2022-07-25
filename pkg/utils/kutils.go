// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"context"
	"errors"

	"github.com/container-storage-interface/spec/lib/go/csi"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

func GetPvcFromCreateVolumeRequest(ctx context.Context, client kubernetes.Interface, req *csi.CreateVolumeRequest) (*corev1.PersistentVolumeClaim, error) {
	if req == nil {
		return nil, errors.New("CreateVolumeRequest is nil")
	}

	pvcName, pvcNameExist := req.Parameters["csi.storage.k8s.io/pvc/name"]
	pvcNamespace, pvcNamespaceExist := req.Parameters["csi.storage.k8s.io/pvc/namespace"]

	if !pvcNameExist || !pvcNamespaceExist {
		// for backward compatible. This means the corner case: users of the old version csi-driver
		// upgraded the csi-driver image separately instead of through helm upgrade, so that the csi-provisioner
		// sidecar did not add the --extra-create-metadata startup parameter, resulting in the pvc could not be
		// obtained (https ://github.com/kubernetes-csi/external-provisioner).
		// in this scenario, external pvc is not supported. output a warning log here.
		klog.V(2).Info("unable to get pvc's name/namespace in CreateVolumeRequest's parameters, should set flag --extra-create-metadata in external-provisioner sidecar")

		return nil, nil
	}

	return client.CoreV1().PersistentVolumeClaims(pvcNamespace).Get(ctx, pvcName, v1.GetOptions{})
}
