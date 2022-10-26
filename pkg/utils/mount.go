// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"k8s.io/klog"
	kexec "k8s.io/utils/exec"
	kmount "k8s.io/utils/mount"
)

const (
	procMountInfoPath = "/proc/self/mountinfo"
)

type Mount interface {
	// kmount Mount interface
	kmount.Interface
	GetDiskFormat(device string) (string, error)
	// safe format and mount
	FormatAndMount(source string, target string, fsType string, options []string) error
	// get fs mount point
	GetMountPoint(path string) (*kmount.MountPoint, error)
	// get fs / device mount ref
	GetMountRef(path string) (*kmount.MountInfo, error)

	PathExists(filename string) (bool, error)

	GetDeviceSerial(device string) (string, error)
}

type mount struct {
	*kmount.SafeFormatAndMount
}

func NewMount() Mount {
	return &mount{
		&kmount.SafeFormatAndMount{
			Exec:      kexec.New(),
			Interface: kmount.New(""),
		},
	}
}

func IsMountReadOnly(options []string) bool {
	for _, option := range options {
		if option == "ro" {
			return true
		}
	}

	return false
}

func MakeReadOnlyMountOpts(options []string) []string {
	newOptions := make([]string, 0)

	// remove rw and ro
	for _, opt := range options {
		if opt == "rw" || opt == "ro" {
			continue
		}

		newOptions = append(newOptions, opt)
	}

	newOptions = append(newOptions, "ro")

	return newOptions
}

func (m *mount) formatWithOption(source, target, fsType string, options []string) error {
	existingFormat, err := m.GetDiskFormat(source)
	if err != nil {
		return err
	}

	if existingFormat == "" {
		if IsMountReadOnly(options) {
			return kmount.NewMountError(kmount.UnformattedReadOnly,
				"cannot mount unformatted disk %s as we are manipulating it in read-only mode", source)
		}

		output, err := m.Exec.Command("mkfs."+fsType, append(options, source)...).CombinedOutput()
		if err != nil {
			detailedErr := fmt.Sprintf("format of disk %q failed: type:(%q) target:(%q)"+
				" options:(%q) errcode:(%v) output:(%v) ",
				source, fsType, target, options, err, string(output))

			return kmount.NewMountError(kmount.FormatFailed, detailedErr)
		}
	}

	return nil
}

func (m *mount) FormatAndMount(source string, target string, fsType string, options []string) error {
	// kmount.SafeFormatAndMount does not support specifying the format parameter.
	switch fsType {
	case "xfs":
		// disable xfs reflink
		err := m.formatWithOption(source, target, fsType, []string{
			"-m", "reflink=0", // 3.10 kernel does not support xfs with reflink enabled
			"-K", // use nodiscard options on making xfs file system to speed up format
		})
		if err != nil {
			return err
		}

		options = append(options, "nouuid")

		return m.SafeFormatAndMount.Mount(source, target, fsType, options)
	case "ext4":
		err := m.formatWithOption(source, target, fsType, []string{
			"-E", "nodiscard", // use nodiscard options on making ext4 file system to speed up format
			"-F",  // Force flag
			"-m0", // Zero blocks reserved for super-user
		})
		if err != nil {
			return err
		}

		return m.SafeFormatAndMount.Mount(source, target, fsType, options)
	}

	return m.SafeFormatAndMount.FormatAndMount(source, target, fsType, options)
}

func (m *mount) GetMountRef(path string) (*kmount.MountInfo, error) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}

	mis, err := kmount.ParseMountInfo(procMountInfoPath)
	if err != nil {
		return nil, err
	}

	for _, mi := range mis {
		if mi.MountPoint == path {
			return &mi, nil
		}
	}

	return nil, nil
}

func (m *mount) GetMountPoint(path string) (*kmount.MountPoint, error) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}

	// get mount point
	mps, err := m.List()
	if err != nil {
		return nil, err
	}

	for _, mp := range mps {
		if mp.Path == path {
			return &mp, nil
		}
	}

	return nil, nil
}

func (m *mount) PathExists(path string) (bool, error) {
	return kmount.PathExists(path)
}

func (m *mount) GetDeviceSerial(device string) (string, error) {
	udevCmd := fmt.Sprintf("udevadm info --query=all --name=%v | grep ID_SERIAL", device)

	idSerialLine, err := exec.Command("/bin/sh", "-c", udevCmd).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get ID_SERIAL from udevadm, %v", string(idSerialLine))
	}

	idSerial := strings.Split(strings.TrimSpace(string(idSerialLine)), "=")
	klog.Infof("ID_SERIAL parsed from udevadm is %v", idSerial)

	if len(idSerial) != 2 {
		return "", nil
	}

	return idSerial[1], nil
}

type fakeMount struct {
	kmount.Interface
	deviceFSTypeMap map[string]string
	mutex           sync.Mutex
}

func NewFakeMount() Mount {
	return &fakeMount{
		Interface:       kmount.NewFakeMounter(nil),
		deviceFSTypeMap: make(map[string]string),
	}
}

func (m *fakeMount) FormatAndMount(source string, target string, fsType string, options []string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	deviceFSType, ok := m.deviceFSTypeMap[source]
	if !ok {
		m.deviceFSTypeMap[source] = fsType
		deviceFSType = fsType
	}

	if deviceFSType != fsType {
		return fmt.Errorf("source %v fs type %v != target %v fs type %v",
			source, deviceFSType, target, fsType)
	}

	return m.Mount(source, target, fsType, options)
}

func (m *fakeMount) GetMountPoint(path string) (*kmount.MountPoint, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}

	mps, err := m.List()
	if err != nil {
		return nil, err
	}

	for _, mp := range mps {
		if mp.Path == path {
			return &mp, nil
		}
	}

	return nil, nil
}

func (m *fakeMount) GetMountRef(path string) (*kmount.MountInfo, error) {
	mp, err := m.GetMountPoint(path)
	if err != nil {
		return nil, err
	}

	mi := &kmount.MountInfo{}
	mi.FsType = mp.Type
	mi.MountOptions = mp.Opts
	mi.MountPoint = path
	mi.Source = mp.Device

	return mi, nil
}

func (m *fakeMount) PathExists(filename string) (bool, error) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	return true, nil
}

func (m *fakeMount) GetDiskFormat(disk string) (string, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	diskFSType, ok := m.deviceFSTypeMap[disk]
	if !ok {
		return "", nil
	}

	return diskFSType, nil
}

func (m *fakeMount) GetDeviceSerial(device string) (string, error) {
	return "testSerial", nil
}
