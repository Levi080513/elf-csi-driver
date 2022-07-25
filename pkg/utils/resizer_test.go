// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import "testing"

func TestResizer(t *testing.T) {
	resizer := NewFakeResizer()
	device := "/dev/sda"
	fs := "ext4"

	err := resizer.ResizeBlock(device, 18<<30)
	if err != nil {
		t.Fatalf("failed to resize device %v, %v", device, err)
	}

	err = resizer.ResizeFS(device, fs)
	if err != nil {
		t.Fatalf("failed to resize device fs %v, %v, %v", device, fs, err)
	}
}
