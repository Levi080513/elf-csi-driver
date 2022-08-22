// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver_test

import (
	"os"
	"testing"

	"github.com/kubernetes-csi/csi-test/v3/pkg/sanity"
	"k8s.io/klog"

	"github.com/smartxworks/elf-csi-driver/pkg/testing/constant"
)

const (
	basePath = "/tmp/csi"
)

func TestSanity(t *testing.T) {
	klog.InitFlags(nil)

	_ = os.RemoveAll(basePath)
	_ = os.MkdirAll(basePath, os.ModePerm)

	stopCh := make(chan struct{})
	defer close(stopCh)

	config := sanity.NewTestConfig()

	defer func() {
		os.RemoveAll(config.TargetPath)
		os.RemoveAll(config.StagingPath)
	}()

	config.Address = constant.SocketAddr
	sanity.Test(t, config)
}
