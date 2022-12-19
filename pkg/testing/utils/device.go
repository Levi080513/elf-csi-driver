package utils

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"os"
	"path"
)

func NewDeviceSymlink(devPath, deviceSymlinkDir string, mountBus models.Bus) (string, error) {
	_, err := os.Create(devPath)
	if err != nil {
		return "", err
	}

	deviceSymlinkFile := ""
	serial := uuid.New().String()
	if mountBus == models.BusVIRTIO {
		deviceSymlinkFile = fmt.Sprintf("virtio-%s", serial)
	}
	if mountBus == models.BusSCSI {
		deviceSymlinkFile = fmt.Sprintf("scsi-%s", serial)
	}

	deviceSymlinkPath := path.Join(deviceSymlinkDir, deviceSymlinkFile)

	err = os.Symlink(devPath, path.Join(deviceSymlinkDir, deviceSymlinkPath))
	if err != nil {
		return "", err
	}
	return deviceSymlinkPath, nil
}
