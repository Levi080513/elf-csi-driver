// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	// symlinks in the /dev/disk/by-id folder are created by udev rules (and are specific to OS'es using udev),
	// the rule for the symlinks is:
	// /dev/disk/by-id/{BUS}-{ID_SERIAL}
	DevDiskIDPath = "/dev/disk/by-id"

	// symlinkPrefixForAttachedVIRTIOBus is the prefix of symlink name for device which attach to VIRTIO Bus.
	symlinkPrefixForAttachedVIRTIOBus = "virtio-"
	// symlinkPrefixForAttachedSCSIBus is the prefix of symlink name for device which attach to SCSI Bus.
	symlinkPrefixForAttachedSCSIBus = "scsi-"
)

type deviceSerialCache struct {

	// serialToDeviceCacheMap is the map of serial prefix to device in this VM.
	serialPrefixToDeviceCacheMap map[string]string

	// serialPrefixToSCSISerialPrefixCacheMap is the map of serial prefix to scsi serial prefix in this VM.
	serialPrefixToSCSISerialPrefixCacheMap map[string]string

	rLock sync.RWMutex

	osUtil utils.OsUtil

	name string
}

func NewDeviceSerialCache(config *DriverConfig) *deviceSerialCache {
	return &deviceSerialCache{
		serialPrefixToDeviceCacheMap:           make(map[string]string),
		serialPrefixToSCSISerialPrefixCacheMap: make(map[string]string),

		osUtil: config.OsUtil,
	}
}

// resyncCache call listDeviceAndStoreSerialCache to resync cache.
func (n *deviceSerialCache) resyncCache() error {
	err := n.listDeviceAndStoreSerialCache()
	if err != nil {
		return err
	}

	return nil
}

// Run repeatedly uses the cache's ListAndWatchVMDevice to fetch all the
// devices and subsequent deltas.
// Run will exit when stopCh is closed.
func (n *deviceSerialCache) Run(stopCh <-chan struct{}) error {
	go wait.Until(func() {
		if err := n.ListAndWatchVMDevice(stopCh); err != nil {
			klog.Errorf("failed to ListAndWatchVMDevice, error %v", err.Error())
		}
	}, time.Second*2, stopCh)

	return nil
}

// ListAndWatchVMDevice first lists all symlinks in /dev/disk/by-id and store serial cache,
// and then use the fsnotify watcher to observe /dev/disk/by-id folder changes.
// It returns error if ListAndWatchVMDevice didn't even try to initialize watch.
func (n *deviceSerialCache) ListAndWatchVMDevice(stopCh <-chan struct{}) error {
	var err error

	err = n.listDeviceAndStoreSerialCache()
	if err != nil {
		return err
	}

	for {
		select {
		case <-stopCh:
			return nil
		default:
		}

		w, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer w.Close()

		go func() {
			<-stopCh
			w.Close()
		}()

		err = w.Add(DevDiskIDPath)
		if err != nil {
			return err
		}

		err = n.processDeviceEvent(w.Events)
		if err != nil {
			return err
		}
	}
}

// listDeviceAndStoreSerialCache lists all symlinks in /dev/disk/by-id and store serial cache.
func (n *deviceSerialCache) listDeviceAndStoreSerialCache() error {
	devs, err := os.ReadDir(DevDiskIDPath)
	if err != nil {
		return err
	}

	for _, dev := range devs {
		deviceSymlink := path.Join(DevDiskIDPath, dev.Name())
		if !n.isDeviceSymlinkShouldBeProcess(deviceSymlink) {
			continue
		}

		err = n.AddDeviceToSerialCache(deviceSymlink)
		if err != nil {
			klog.Errorf("failed to add VM device for symlink %s, error %v", deviceSymlink, err)
			return err
		}
	}

	return nil
}

// processDeviceEvent process event for /dev/disk/by-id folder changes.
func (n *deviceSerialCache) processDeviceEvent(eventChan <-chan fsnotify.Event) error {
	for {
		event, ok := <-eventChan
		if !ok {
			klog.Warningf("event channel for device path %s is close", DevDiskIDPath)
			return nil
		}

		klog.Infof("process event %s", event.String())

		var err error
		deviceSymlink := event.Name

		if !n.isDeviceSymlinkShouldBeProcess(deviceSymlink) {
			continue
		}

		if event.Op&fsnotify.Create == fsnotify.Create {
			err = n.AddDeviceToSerialCache(deviceSymlink)
		}

		if event.Op&fsnotify.Remove == fsnotify.Remove {
			err = n.RemoveDeviceFromSerialCache(deviceSymlink)
		}

		if err != nil {
			klog.Warningf("failed to process event %s, error %v", event.String(), err)
			return err
		}
	}
}

func (n *deviceSerialCache) AddDeviceToSerialCache(deviceSymlink string) error {
	n.rLock.Lock()
	defer n.rLock.Unlock()

	device, err := n.osUtil.GetDeviceBySymlink(deviceSymlink)
	if err != nil {
		return err
	}

	if isDeviceInVIRTIOBus(deviceSymlink) {
		serial := strings.Split(deviceSymlink, symlinkPrefixForAttachedVIRTIOBus)[1]
		n.serialPrefixToDeviceCacheMap[serial] = device
	}

	if isDeviceInSCSIBus(deviceSymlink) {
		serial := strings.Split(deviceSymlink, symlinkPrefixForAttachedSCSIBus)[1]
		n.serialPrefixToDeviceCacheMap[serial] = device

		scsiSerial, err := n.osUtil.GetDeviceSCSISerial(device)
		if err != nil {
			return err
		}

		n.serialPrefixToSCSISerialPrefixCacheMap[serial] = scsiSerial
	}
	fmt.Println(n.serialPrefixToDeviceCacheMap)
	fmt.Println(n.name)
	return nil
}

func (n *deviceSerialCache) RemoveDeviceFromSerialCache(deviceSymlink string) error {
	n.rLock.Lock()
	defer n.rLock.Unlock()

	if isDeviceInVIRTIOBus(deviceSymlink) {
		serialPrefix := strings.Split(deviceSymlink, symlinkPrefixForAttachedVIRTIOBus)[1]
		delete(n.serialPrefixToDeviceCacheMap, serialPrefix)
	}

	if isDeviceInSCSIBus(deviceSymlink) {
		serialPrefix := strings.Split(deviceSymlink, symlinkPrefixForAttachedSCSIBus)[1]
		delete(n.serialPrefixToDeviceCacheMap, serialPrefix)
		delete(n.serialPrefixToSCSISerialPrefixCacheMap, serialPrefix)
	}

	return nil
}

func (n *deviceSerialCache) GetDeviceByIDSerial(serial string) (string, bool) {
	n.rLock.RLock()
	defer n.rLock.RUnlock()

	for serialPrefix := range n.serialPrefixToDeviceCacheMap {
		if strings.HasPrefix(serial, serialPrefix) {
			targetDevice := n.serialPrefixToDeviceCacheMap[serialPrefix]
			return fmt.Sprintf("/dev/%v", targetDevice), true
		}
	}

	return "", false
}

func (n *deviceSerialCache) GetDeviceByIDSCSISerial(scsiSerial string) (string, bool) {
	n.rLock.RLock()
	defer n.rLock.RUnlock()

	targetDeviceSerialPrefix := ""

	for serialPrefix, scsiSerialPrefix := range n.serialPrefixToSCSISerialPrefixCacheMap {
		if strings.HasPrefix(scsiSerial, scsiSerialPrefix) {
			targetDeviceSerialPrefix = serialPrefix
			break
		}
	}

	if targetDeviceSerialPrefix == "" {
		return "", false
	}

	if targetDevice, ok := n.serialPrefixToDeviceCacheMap[targetDeviceSerialPrefix]; ok {
		return fmt.Sprintf("/dev/%v", targetDevice), true
	}

	return "", false
}

// isDeviceSymlinkShouldBeProcess return true when the device associated with the symlink is attached by ELF CSI.
func (n *deviceSerialCache) isDeviceSymlinkShouldBeProcess(deviceSymlink string) bool {
	// ELF CSI only support attach to VIRTIO Bus and SCSI Bus,
	// If the device associated with the symlink is attached to the VIRTIO bus or SCSI bus,
	// return true.
	if isDeviceInSCSIBus(deviceSymlink) || isDeviceInVIRTIOBus(deviceSymlink) {
		return true
	}

	return false
}
