// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type OsUtil interface {
	GetDeviceBySymlink(deviceSymlink string) (string, error)
	GetDeviceSCSISerial(device string) (string, error)
}

type osUtil struct {
}

func NewOsUtil() *osUtil {
	return &osUtil{}
}

func (*osUtil) GetDeviceBySymlink(deviceSymlink string) (string, error) {
	// Eval any symlinks and make sure it points to a device.
	realDevicePath, err := filepath.EvalSymlinks(deviceSymlink)
	if err != nil {
		return "", err
	}

	device, err := os.Stat(realDevicePath)
	if err != nil {
		return "", err
	}

	dm := device.Mode()

	if dm&os.ModeDevice == 0 {
		return "", fmt.Errorf("%s is not a block device", realDevicePath)
	}

	return device.Name(), nil
}

func (*osUtil) GetDeviceSCSISerial(device string) (string, error) {
	udevCmd := fmt.Sprintf("udevadm info --query=all --name=%v | grep ID_SCSI_SERIAL", device)

	idSerialLine, err := exec.Command("/bin/sh", "-c", udevCmd).CombinedOutput()
	if err != nil {
		// the exit status of grep is 0 if selected lines are found and 1 otherwise.
		// But the exit status is 2 if an error occurred.
		// see https://linux.die.net/man/1/grep
		// so if the exit status is 1 and output is null, we should return no error.
		exitErr, ok := err.(*exec.ExitError)
		if ok && exitErr.ProcessState.ExitCode() == 1 && len(idSerialLine) == 0 {
			return "", nil
		}

		return "", fmt.Errorf("failed to get device %s ID_SCSI_SERIAL from udevadm, output %v, error %v", device, string(idSerialLine), err)
	}

	idSerial := strings.Split(strings.TrimSpace(string(idSerialLine)), "=")
	if len(idSerial) != 2 {
		return "", nil
	}

	return idSerial[1], nil
}

type fakeOsUtil struct {
}

func NewFakeOsUtil() *fakeOsUtil {
	return &fakeOsUtil{}
}

func (*fakeOsUtil) GetDeviceBySymlink(deviceSymlink string) (string, error) {
	// Eval any symlinks and make sure it points to a device.
	realDevicePath, err := filepath.EvalSymlinks(deviceSymlink)
	if err != nil {
		return "", err
	}

	device, err := os.Stat(realDevicePath)
	if err != nil {
		return "", err
	}

	return device.Name(), nil
}

func (*fakeOsUtil) GetDeviceSCSISerial(device string) (string, error) {
	return device, nil
}
