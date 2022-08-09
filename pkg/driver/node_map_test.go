// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"reflect"
	"testing"

	kcorev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	kmetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	testNodeID       = "test-node-name-1"
	testConfigMap    = "test-config-name"
	testNodeIP       = "127.0.0.1"
	testLivenessPort = 0
	testNamespace    = "test-namespace"
)

func newConfigMap() *kcorev1.ConfigMap {
	return &kcorev1.ConfigMap{
		ObjectMeta: kmetav1.ObjectMeta{
			Name:      testConfigMap,
			Namespace: testNamespace,
		},
		Data:       make(map[string]string),
		BinaryData: make(map[string][]byte),
	}
}
func TestNodeMap(t *testing.T) {
	client, err := utils.NewFakeConfigMapClient(testNamespace)
	if err != nil {
		t.Fatalf("failed to new client, %v", err)
	}

	_, err = client.Create(context.Background(), newConfigMap(), kmetav1.CreateOptions{})
	if err != nil && !kerrors.IsAlreadyExists(err) {
		t.Fatalf("failed to create configmap, %v", err)
	}

	m := NewNodeMap(testConfigMap, client)

	entry := &NodeEntry{
		NodeIP:       testNodeIP,
		NodeName:     testNodeID,
		LivenessPort: testLivenessPort,
	}

	err = m.Put(testNodeID, entry)
	if err != nil {
		t.Fatalf("failed to create node entry, %v", err)
	}

	entry1, err := m.Get(testNodeID)
	if err != nil {
		t.Fatalf("failed to get node entry, %v", err)
	}

	if !reflect.DeepEqual(entry, entry1) {
		t.Fatalf("entry %+v != entry1 %+v", entry, entry1)
	}
}
