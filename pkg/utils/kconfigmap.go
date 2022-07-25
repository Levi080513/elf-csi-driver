// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

func NewConfigMapClient(client kubernetes.Interface, namespace string) (v1.ConfigMapInterface, error) {
	return client.CoreV1().ConfigMaps(namespace), nil
}

func NewFakeConfigMapClient(namespace string) (v1.ConfigMapInterface, error) {
	return fake.NewSimpleClientset().CoreV1().ConfigMaps(namespace), nil
}
