// Copyright (c) 2020-2022 SMARTX
// All rights reserved
package service

import (
	"github.com/google/uuid"
	"github.com/openlyinc/pointy"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	testutil "github.com/smartxworks/elf-csi-driver/pkg/testing/utils"
	"time"
)

type fakeTowerService struct {
	volumes map[string]*models.VMVolume
	publish map[string]string
	vms     map[string]*models.VM

	deviceSymlinkDir string
	deviceDir        string
}

func NewFakeTowerService(deviceSymlinkDir, deviceDir string) TowerService {
	return &fakeTowerService{
		volumes:          make(map[string]*models.VMVolume),
		publish:          make(map[string]string),
		vms:              make(map[string]*models.VM),
		deviceSymlinkDir: deviceSymlinkDir,
		deviceDir:        deviceDir,
	}
}

func (ts *fakeTowerService) CreateVMVolume(elfClusterID string, name string, storagePolicy models.VMVolumeElfStoragePolicyType,
	size uint64, sharing bool) (*models.Task, error) {
	volumeId := uuid.New().String()
	ts.volumes[volumeId] = &models.VMVolume{
		ID:               &volumeId,
		Name:             &name,
		Sharing:          pointy.Bool(sharing),
		Size:             pointy.Int64(int64(size)),
		ElfStoragePolicy: storagePolicy.Pointer(),
		Mounting:         pointy.Bool(false),
		VMDisks:          []*models.NestedVMDisk{},
		Lun:              &models.NestedIscsiLun{ID: pointy.String("lunID")},
		LocalID:          pointy.String(volumeId),
	}

	return &models.Task{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) DeleteVMVolume(volumeID string) (*models.Task, error) {
	_, ok := ts.volumes[volumeID]
	if !ok {
		return nil, nil
	}

	delete(ts.volumes, volumeID)

	return &models.Task{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) GetVMVolumeByName(volumeName string) (*models.VMVolume, error) {
	for _, volume := range ts.volumes {
		if *volume.Name == volumeName {
			return volume, nil
		}
	}

	return nil, ErrVMVolumeNotFound
}

func (ts *fakeTowerService) GetVM(vmName string) (*models.VM, error) {
	vm := &models.VM{
		Name:    &vmName,
		VMDisks: []*models.NestedVMDisk{},
	}
	ts.vms[vmName] = vm

	return ts.vms[vmName], nil
}

func (ts *fakeTowerService) GetVMVolumeByID(volumeID string) (*models.VMVolume, error) {
	volume, ok := ts.volumes[volumeID]
	if !ok {
		return nil, ErrVMVolumeNotFound
	}

	return volume, nil
}

func (ts *fakeTowerService) GetVMVolumesByID(volumeIDs []string) ([]*models.VMVolume, error) {
	vmVolumes := []*models.VMVolume{}

	for _, volumeID := range volumeIDs {
		volume, ok := ts.volumes[volumeID]
		if !ok {
			continue
		}

		vmVolumes = append(vmVolumes, volume)
	}

	return vmVolumes, nil
}

func (ts *fakeTowerService) AddVMDisks(vmName string, volumeIDs []string, bus models.Bus) (*models.Task, error) {
	for _, volumeID := range volumeIDs {
		ts.publish[volumeID] = vmName
		ts.volumes[volumeID].Mounting = pointy.Bool(true)
		nestID := uuid.New().String()
		ts.volumes[volumeID].VMDisks = []*models.NestedVMDisk{{ID: &nestID}}
		ts.vms[vmName].VMDisks = append(ts.vms[vmName].VMDisks, &models.NestedVMDisk{ID: &nestID})
		_, err := testutil.CreateDeviceSymlinkForVolumeID(volumeID, ts.deviceDir, ts.deviceSymlinkDir, models.BusVIRTIO)
		if err != nil {
			return nil, err
		}

		// add time sleep wait for device serial cache get
		time.Sleep(time.Second * 5)
	}

	return &models.Task{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) RemoveVMDisks(vmName string, volumeIDs []string) (*models.Task, error) {
	removeNestIDsMap := map[string]string{}

	for index := 0; index < len(volumeIDs); index++ {
		ts.publish[volumeIDs[index]] = ""
		ts.volumes[volumeIDs[index]].Mounting = pointy.Bool(false)

		removeNestIDsMap[*ts.volumes[volumeIDs[index]].VMDisks[0].ID] = *ts.volumes[volumeIDs[index]].VMDisks[0].ID
		ts.volumes[volumeIDs[index]].VMDisks = []*models.NestedVMDisk{}
		err := testutil.RemoveDeviceSymlinkForVolumeID(volumeIDs[index], ts.deviceDir, ts.deviceSymlinkDir, models.BusVIRTIO)
		if err != nil {
			return nil, err
		}
		// add time sleep wait for device serial cache get
		time.Sleep(time.Second * 5)
	}

	vmNestDisks := []*models.NestedVMDisk{}
	for _, vmNestDisk := range vmNestDisks {
		if _, ok := removeNestIDsMap[*vmNestDisk.ID]; ok {
			continue
		}

		vmNestDisks = append(vmNestDisks, vmNestDisk)
	}

	ts.vms[vmName].VMDisks = vmNestDisks

	return &models.Task{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) GetVMDisks(vmName string, volumeIDs []string) ([]*models.VMDisk, error) {
	var vmDisks []*models.VMDisk

	for volumeID, vm := range ts.publish {
		if vm != vmName {
			continue
		}

		vmDisks = append(vmDisks, &models.VMDisk{
			ID:     pointy.String(volumeID),
			Serial: pointy.String(volumeID),
			Bus:    models.BusVIRTIO.Pointer(),
			VMVolume: &models.NestedVMVolume{
				ID: pointy.String(volumeID),
			},
			VM: &models.NestedVM{
				Name: pointy.String(vmName),
			},
		})
	}
	if len(volumeIDs) == 0 {
		return vmDisks, nil
	}

	var result []*models.VMDisk

	for _, vmDisk := range vmDisks {
		for _, needVolumeId := range volumeIDs {
			if *vmDisk.VMVolume.ID == needVolumeId {
				result = append(result, vmDisk)
			}
		}
	}

	return result, nil
}

func (ts *fakeTowerService) GetTask(id string) (*models.Task, error) {
	if id == "1" {
		return &models.Task{
			ID:     pointy.String(id),
			Status: models.TaskStatusSUCCESSED.Pointer(),
		}, nil
	}

	return &models.Task{
		ID:     pointy.String(id),
		Status: models.TaskStatusFAILED.Pointer(),
	}, nil
}

func (ts *fakeTowerService) CreateLabel(key, value string) (*models.Task, error) {
	return &models.Task{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) GetLabel(key, value string) (*models.Label, error) {
	return &models.Label{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) AddLabelsToVolume(volumeID string, labels []string) (*models.Task, error) {
	return &models.Task{ID: pointy.String("1")}, nil
}

func (ts *fakeTowerService) GetISCSILuns(lunIDs []string) ([]*models.IscsiLun, error) {
	return []*models.IscsiLun{{ZbsVolumeID: pointy.String("testSerial")}}, nil
}
