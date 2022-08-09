// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package mockdriver

import (
	"context"
	"os"

	"github.com/iomesh/zbs-client-go/zbs"
	snapshotfake "github.com/kubernetes-csi/external-snapshotter/client/v4/clientset/versioned/fake"
	kcorev1 "k8s.io/api/core/v1"
	kmetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/smartxworks/elf-csi-driver/pkg/driver"
	"github.com/smartxworks/elf-csi-driver/pkg/testing/constant"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	iscsiPortal  = "127.0.0.1"
	livenessPort = 2020
	nodeIP       = "127.0.0.1"
	nodeName     = "test-node"
	nodeMapName  = "node-map"
	namespace    = "default"
)

func RunMockDriver(kubeClient kubernetes.Interface, zbsClient *zbs.Client, stopCh chan struct{}) (*driver.ConnMap, error) {
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
		return nil, err
	}

	connMap := driver.NewConnMap(constant.DriverName, kubeClient, snapshotClient, driver.Conn{
		ClusterID:   "mock",
		External:    true,
		Client:      zbsClient,
		ISCSIPortal: iscsiPortal,
	})

	drv, err := driver.NewDriver(&driver.DriverConfig{
		NodeID:            nodeName,
		DriverName:        constant.DriverName,
		Version:           "2.0",
		Role:              driver.ALL,
		ServerAddr:        constant.SocketAddr,
		NodeMap:           driver.NewNodeMap(nodeMapName, kubeClient.CoreV1().ConfigMaps(namespace)),
		ConnMap:           connMap,
		LivenessPort:      livenessPort,
		ISCSIDaemonConfig: utils.NewFakeISCSIDaemonConfig(map[string]string{}),
		ISCSI:             utils.NewFakeISCSI(),
		Mount:             utils.NewFakeMount(),
		Resizer:           utils.NewFakeResizer(),
		KubeClient:        kubeClient,
		SnapshotClient:    snapshotClient,
	})
	if err != nil {
		return nil, err
	}

	go func() {
		_ = drv.Run(stopCh)
	}()

	return connMap, nil
}
