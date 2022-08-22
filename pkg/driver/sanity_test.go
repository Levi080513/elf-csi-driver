// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver_test

import (
	"os"
	"testing"

	"github.com/smartxworks/elf-csi-driver/pkg/testing/mockdriver"

	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubernetes-csi/csi-test/v3/pkg/sanity"
	"github.com/stretchr/testify/assert"
	"k8s.io/klog"

	"github.com/smartxworks/elf-csi-driver/pkg/testing/constant"
)

const (
	basePath = "/tmp/csi"
)

func TestSanity(t *testing.T) {
	klog.InitFlags(nil)

	asserter := assert.New(t)

	_ = os.RemoveAll(basePath)
	_ = os.MkdirAll(basePath, os.ModePerm)

	stopCh := make(chan struct{})
	defer close(stopCh)

	kubeClient := kubefake.NewSimpleClientset()

	err := mockdriver.RunMockDriver(kubeClient, stopCh)
	asserter.NoError(err)

	config := sanity.NewTestConfig()

	defer func() {
		os.RemoveAll(config.TargetPath)
		os.RemoveAll(config.StagingPath)
	}()

	config.Address = constant.SocketAddr
	sanity.Test(t, config)
}
