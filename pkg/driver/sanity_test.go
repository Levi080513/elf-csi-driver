// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver_test

import (
	"os"
	"testing"

	"github.com/kubernetes-csi/csi-test/v3/pkg/sanity"
	"github.com/stretchr/testify/assert"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog"

	"github.com/iomesh/csi-driver/pkg/testing/constant"
	"github.com/iomesh/csi-driver/pkg/testing/mockdriver"
	"github.com/iomesh/csi-driver/pkg/testing/mockzbs"
)

const (
	basePath = "/tmp/csi"
)

func TestSanity(t *testing.T) {
	klog.InitFlags(nil)

	asserter := assert.New(t)

	_ = os.RemoveAll(basePath)
	_ = os.MkdirAll(basePath, os.ModePerm)

	server, err := mockzbs.StartMockServer()
	asserter.NoError(err)

	defer func() { _ = server.Stop() }()

	zbsClient, err := mockzbs.GetZBSClient(server)
	asserter.NoError(err)

	kubeClient := kubefake.NewSimpleClientset()

	stopCh := make(chan struct{})
	defer close(stopCh)

	_, err = mockdriver.RunMockDriver(kubeClient, zbsClient, stopCh)
	asserter.NoError(err)

	config := sanity.NewTestConfig()

	defer func() {
		os.RemoveAll(config.TargetPath)
		os.RemoveAll(config.StagingPath)
	}()

	config.Address = constant.SocketAddr
	sanity.Test(t, config)
}
