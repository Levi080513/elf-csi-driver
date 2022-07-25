// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

const (
	testIface         = "test-iface"
	testPortal        = "127.0.0.1:3260"
	testTargetName    = "iqn.2020-05.com.iomesh:system:test"
	testLunId         = 1
	testInitiatorName = "iqn.2020-05.com.iomes:iface-1"
)

func TestISCSIConfig(t *testing.T) {
	config := &ISCSIConfig{}
	config = config.WithDefault()

	err := config.Validate()
	if err != nil {
		t.Fatal(err)
	}

	*config.NodeSessionCmdsMax = -1

	err = config.Validate()
	if err == nil {
		t.Fatal("cmds max out of range")
	}

	*config.NodeSessionCmdsMax = 4096

	err = config.Validate()
	if err == nil {
		t.Fatal("cmds max out of range")
	}

	*config.NodeSessionCmdsMax = 1023

	err = config.Validate()
	if err == nil {
		t.Fatal("cmds max is not a power of 2")
	}

	*config.NodeSessionCmdsMax = 128
	*config.NodeSessionQueueDepth = -1

	err = config.Validate()
	if err == nil {
		t.Fatal("queue depth out of range")
	}

	*config.NodeSessionQueueDepth = 1025

	err = config.Validate()
	if err == nil {
		t.Fatal("queue depth out of range")
	}
}

func TestISCSI(t *testing.T) {
	iscsi := NewFakeISCSI()

	iscsiLun := &ISCSILun{
		LunId:     1,
		Portal:    testPortal,
		TargetIqn: testTargetName,
		Initiator: testInitiatorName,
		IFace:     testIface,
	}

	device, err := iscsi.Attach(iscsiLun)
	if err != nil {
		t.Fatalf("failed to attach, %v", err)
	}

	deviceGet, err := iscsi.Attach(iscsiLun)
	if err != nil || deviceGet != device {
		t.Fatalf("failed to attach, %v", err)
	}

	deviceGet, err = iscsi.GetLunDevice(iscsiLun)
	if err != nil || deviceGet != device {
		t.Fatalf("failed to get %+v device, %v", iscsiLun, err)
	}

	_, err = iscsi.ListDevices()
	if err != nil {
		t.Fatalf("failed to list devices, %v", err)
	}

	err = iscsi.Detach(iscsiLun)
	if err != nil {
		t.Fatalf("failed to delete lun device, target %v lunId %v, %v",
			testTargetName, testLunId, err)
	}

	err = iscsi.Detach(iscsiLun)
	if err != nil {
		t.Fatalf("failed to delete lun device, target %v lunId %v, %v",
			testTargetName, testLunId, err)
	}

	device, err = iscsi.GetLunDevice(iscsiLun)
	if err != nil {
		t.Fatalf("failed to find lun device, taregt %v lunId %v, %v",
			testTargetName, testLunId, err)
	}

	if device != "" {
		t.Fatalf("device has been deleted, %v", device)
	}
}

func writeLines(writer io.Writer, lines []string) error {
	for _, line := range lines {
		if line == "" {
			continue
		}

		_, err := fmt.Fprintln(writer, line)
		if err != nil {
			return err
		}
	}

	return nil
}

func TestISCSIDaemonConfig(t *testing.T) {
	tempFile, err := ioutil.TempFile("/tmp", "test-csi-iscsid-*.conf")
	if err != nil {
		t.Fatalf("failed to create a temp file, %v", err)
	}

	defer os.Remove(tempFile.Name())

	testLines := []string{
		"key-1 = value-1",
		"#key-2 = value-2-1",
		" #key-2 = value-2-1",
		" # key-2 = value-2-1",
		"key-2 = value-2",
		" ",
	}

	err = writeLines(tempFile, testLines)
	if err != nil {
		t.Fatalf("failed to write line, %v", err)
	}

	err = tempFile.Close()
	if err != nil {
		t.Fatalf("failed to close temp file, %v", err)
	}

	config := NewISCSIDaemonConfig(tempFile.Name())

	_, err = config.Get("")
	if err == nil {
		t.Fatalf("err is nil")
	}

	var value string

	assertKeyValue := func(k string, expectValue string) {
		value, err = config.Get(k)
		if err != nil {
			t.Fatalf("failed to get %v, %v", k, err)
		}

		if value != expectValue {
			t.Fatalf("%v value %v not eq %v", k, value, expectValue)
		}
	}

	assertKeyValue("key-1", "value-1")
	assertKeyValue("key-2", "value-2")
	assertKeyValue("key-3", "")
}
