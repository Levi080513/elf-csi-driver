// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"os"
	"testing"
)

const (
	targetPath1  = "/tmp/target1"
	targetPath2  = "/tmp/target2"
	sourceDevice = "/dev/sda"
	fsType       = "ext4"
)

func createFSTargetPath(t *testing.T) {
	err := os.MkdirAll(targetPath1, os.ModeDir)
	if err != nil && !os.IsExist(err) {
		t.Fatalf("failed to mkdir %v, %v", targetPath1, err)
	}

	err = os.MkdirAll(targetPath2, os.ModeDir)
	if err != nil && !os.IsExist(err) {
		t.Fatalf("failed to mkdir %v, %v", targetPath2, err)
	}
}

func createBindTargetPath(t *testing.T) {
	f, err := os.Create(targetPath1)
	if err != nil && !os.IsExist(err) {
		t.Fatalf("failed to mkdir %v, %v", targetPath1, err)
	}

	f.Close()

	f, err = os.Create(targetPath2)
	if err != nil && !os.IsExist(err) {
		t.Fatalf("failed to mkdir %v, %v", targetPath2, err)
	}

	f.Close()
}

func deleteTargetPath(t *testing.T, m Mount) {
	_ = m.Unmount(targetPath1)

	err := os.RemoveAll(targetPath1)
	if err != nil && !os.IsNotExist(err) {
		t.Errorf("failed remove %v, %v", targetPath1, err)
	}

	_ = m.Unmount(targetPath2)

	err = os.RemoveAll(targetPath2)
	if err != nil && !os.IsNotExist(err) {
		t.Errorf("failed remove %v, %v", targetPath2, err)
	}
}

func TestMount(t *testing.T) {
	m := NewFakeMount()

	createFSTargetPath(t)
	defer deleteTargetPath(t, m)

	err := m.FormatAndMount(sourceDevice, targetPath1, fsType, nil)
	if err != nil {
		t.Fatalf("failed to farmatAndMount, %v", err)
	}

	err = m.FormatAndMount(sourceDevice, targetPath2, fsType, nil)
	if err != nil {
		t.Fatalf("failed to mount %v, %v", targetPath2, err)
	}
	// get sourceDevice mount refs except  targetPath2
	refs, err := m.GetMountRefs(targetPath2)
	if err != nil || len(refs) != 1 {
		t.Fatalf("failed to get mount refs %+v, %v", refs, err)
	}

	point, err := m.GetMountPoint(targetPath2)
	if err != nil {
		t.Fatalf("failed to get targetPath2 mount point, %v", err)
	}

	if point.Device != sourceDevice && point.Path != targetPath2 {
		t.Fatalf("targetPath2 mount point not match %+v", point)
	}

	err = m.Unmount(targetPath1)
	if err != nil {
		t.Fatalf("failed to unmount %v, %v", targetPath1, err)
	}

	err = m.Unmount(targetPath2)
	if err != nil {
		t.Fatalf("failed to unmount %v, %v", targetPath2, err)
	}
}

func TestBind(t *testing.T) {
	m := NewFakeMount()

	createBindTargetPath(t)
	defer deleteTargetPath(t, m)

	err := m.Mount(sourceDevice, targetPath1, "", []string{"bind"})
	if err != nil {
		t.Fatalf("failed to farmatAndMount, %v", err)
	}

	refs, err := m.GetMountRefs(targetPath1)
	if err != nil {
		t.Fatalf("failed to get mount refs %v", err)
	}

	if len(refs) != 0 {
		t.Fatalf("refs not empty, %v", refs)
	}

	err = m.Mount(sourceDevice, targetPath2, "", []string{"bind"})
	if err != nil {
		t.Fatalf("failed to mount %v, %v", targetPath2, err)
	}

	targetRef, err := m.GetMountRef(targetPath2)
	if err != nil || targetRef == nil {
		t.Fatalf("failed to get %v mount ref, %v", targetPath2, err)
	}

	err = m.Unmount(targetPath1)
	if err != nil {
		t.Fatalf("failed to unmount %v, %v", targetPath1, err)
	}

	err = m.Unmount(targetPath2)
	if err != nil {
		t.Fatalf("failed to unmount %v, %v", targetPath2, err)
	}
}
