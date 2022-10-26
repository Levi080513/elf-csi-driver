// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package mockdriver

import (
	"context"
	"os"

	snapshotfake "github.com/kubernetes-csi/external-snapshotter/client/v4/clientset/versioned/fake"
	kcorev1 "k8s.io/api/core/v1"
	kmetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/smartxworks/elf-csi-driver/pkg/driver"
	"github.com/smartxworks/elf-csi-driver/pkg/service"
	"github.com/smartxworks/elf-csi-driver/pkg/testing/constant"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	livenessPort = 2020
	nodeIP       = "127.0.0.1"
	nodeName     = "test-node"
	nodeMapName  = "node-map"
	namespace    = "default"
)

func RunMockDriver(kubeClient kubernetes.Interface, stopCh chan struct{}) error {
	_ = os.Setenv("NODE_NAME", nodeName)
	_ = os.Setenv("NODE_IP", nodeIP)

	snapshotClient := snapshotfake.NewSimpleClientset()

	_, err := kubeClient.CoreV1().ConfigMaps(namespace).Create(context.Background(), &kcorev1.ConfigMap{
		ObjectMeta: kmetav1.ObjectMeta{
			Name:      nodeMapName,
			Namespace: namespace,
		},
		Data:       make(map[string]string),
		BinaryData: make(map[string][]byte),
	}, kmetav1.CreateOptions{})

	if err != nil {
		return err
	}

	drv, err := driver.NewDriver(&driver.DriverConfig{
		NodeID:         nodeName,
		DriverName:     constant.DriverName,
		Version:        "2.0",
		Role:           driver.ALL,
		ServerAddr:     constant.SocketAddr,
		NodeMap:        driver.NewNodeMap(nodeMapName, kubeClient.CoreV1().ConfigMaps(namespace)),
		LivenessPort:   livenessPort,
		Mount:          utils.NewFakeMount(),
		Resizer:        utils.NewFakeResizer(),
		KubeClient:     kubeClient,
		SnapshotClient: snapshotClient,
		TowerClient:    service.NewFakeTowerService(),
	})
	if err != nil {
		return err
	}

	go func() {
		_ = drv.Run(stopCh)
	}()

	return nil
}
