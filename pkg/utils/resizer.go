// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

var resizeCmd = map[string]string{
	"ext2": "resize2fs",
	"ext3": "resize2fs",
	"ext4": "resize2fs",
	"xfs":  "xfs_growfs",
}

type Resizer interface {
	// resize block device size
	ResizeBlock(device string, size uint64) error
	// resize fs
	ResizeFS(device string, fs string) error
}

type resizer struct {
}

func NewResizer() Resizer {
	return &resizer{}
}

func (r *resizer) getDeviceSize(device string) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	lsblkCmd := fmt.Sprintf("lsblk -b --output SIZE -n -d %v", device)

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", lsblkCmd).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("lsblk, cmd: %v, output: %v, error: %v",
			lsblkCmd, string(output), err)
	}

	var size uint64

	n, err := fmt.Sscanf(string(output), "%v", &size)
	if err != nil || n != 1 {
		return 0, fmt.Errorf("get device %v, %v", device, string(output))
	}

	return size, nil
}

func (r *resizer) ResizeBlock(device string, size uint64) error {
	const waitInterval = 1 * time.Second

	return Retry(
		func() error {
			err := r.rescan(device)
			if err != nil {
				return err
			}

			actualSize, err := r.getDeviceSize(device)
			if err != nil {
				return err
			}

			if actualSize >= size {
				return nil
			}

			return fmt.Errorf("wait device %v size change", device)
		}, retryLimit, waitInterval)
}

func (r *resizer) rescan(device string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	device = filepath.Base(device)
	rescanPath := fmt.Sprintf("/sys/class/block/%v/device/rescan", device)
	rescanCmd := "echo \"1\" > " + rescanPath

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", rescanCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rescan, cmd: %v, output: %v, error: %v",
			rescanCmd, string(output), err)
	}

	return nil
}

func (r *resizer) ResizeFS(device string, fs string) error {
	resizeFSCmd, ok := resizeCmd[fs]
	if !ok {
		return fmt.Errorf("not support resize for %v", fs)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	resizeFSCmd = resizeFSCmd + " " + device

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", resizeFSCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("resize fs, cmd: %v, output: %v, error: %v", resizeFSCmd, string(output), err)
	}

	return nil
}

type fakeResizer struct {
}

func NewFakeResizer() Resizer {
	return &fakeResizer{}
}

func (r *fakeResizer) ResizeBlock(device string, size uint64) error {
	return nil
}

func (r *fakeResizer) ResizeFS(device string, fs string) error {
	return nil
}
