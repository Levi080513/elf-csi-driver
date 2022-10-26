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

	"github.com/smartxworks/elf-csi-driver/pkg/feature"
	"github.com/smartxworks/elf-csi-driver/pkg/testing/constant"
	"github.com/smartxworks/elf-csi-driver/pkg/testing/mockdriver"
)

const (
	basePath = "/tmp/csi"
)

func TestSanity(t *testing.T) {
	feature.Init()
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
