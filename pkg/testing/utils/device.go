package utils

import (
	"fmt"
	"os"
	"path"

	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
)

func CreateDeviceSymlinkForVolumeID(volumeID, deviceDir, deviceSymlinkDir string, mountBus models.Bus) (string, error) {
	devicePath := path.Join(deviceDir, volumeID)
	_, err := os.OpenFile(devicePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.ModeCharDevice)
	if err != nil {
		return "", err
	}

	deviceSymlinkFile := ""
	if mountBus == models.BusVIRTIO {
		deviceSymlinkFile = fmt.Sprintf("virtio-%s", volumeID)
	}
	if mountBus == models.BusSCSI {
		deviceSymlinkFile = fmt.Sprintf("scsi-%s", volumeID)
	}

	deviceSymlinkPath := path.Join(deviceSymlinkDir, deviceSymlinkFile)

	err = os.Symlink(devicePath, deviceSymlinkPath)
	if err != nil {
		return "", err
	}
	return deviceSymlinkPath, nil
}

func RemoveDeviceSymlinkForVolumeID(volumeID, deviceDir, deviceSymlinkDir string, mountBus models.Bus) error {
	devicePath := path.Join(deviceDir, volumeID)
	err := os.RemoveAll(devicePath)
	if err != nil {
		return err
	}

	deviceSymlinkFile := ""
	if mountBus == models.BusVIRTIO {
		deviceSymlinkFile = fmt.Sprintf("virtio-%s", volumeID)
	}
	if mountBus == models.BusSCSI {
		deviceSymlinkFile = fmt.Sprintf("scsi-%s", volumeID)
	}

	deviceSymlinkPath := path.Join(deviceSymlinkDir, deviceSymlinkFile)

	err = os.RemoveAll(deviceSymlinkPath)
	if err != nil {
		return err
	}
	return nil
}
