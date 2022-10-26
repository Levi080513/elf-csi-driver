// Copyright (c) 2020-2022 SMARTX
// All rights reserved
package service

import (
	"errors"
	"fmt"

	"github.com/openlyinc/pointy"
	towerclient "github.com/smartxworks/cloudtower-go-sdk/v2/client"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/cluster"
	iscsilun "github.com/smartxworks/cloudtower-go-sdk/v2/client/iscsi_lun"
	clientlabel "github.com/smartxworks/cloudtower-go-sdk/v2/client/label"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/task"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/vm"
	vmdisk "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_disk"
	vmvolume "github.com/smartxworks/cloudtower-go-sdk/v2/client/vm_volume"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
)

var (
	ErrVMVolumeNotFound = errors.New("volume is not found")

	ErrVMNotFound = errors.New("VM is not found")

	ErrTaskNotFound = errors.New("task is not found")

	ErrLabelNotFound = errors.New("label is not found")
)

type TowerService interface {
	CreateVMVolume(clusterIdOrLocalId string, name string, storagePolicy models.VMVolumeElfStoragePolicyType,
		size uint64, sharing bool) (*models.Task, error)
	DeleteVMVolume(volumeID string) (*models.Task, error)
	GetVMVolumeByName(volumeName string) (*models.VMVolume, error)
	GetVMVolumesByID(volumeIDs []string) ([]*models.VMVolume, error)
	GetVMVolumeByID(volumeID string) (*models.VMVolume, error)
	GetVM(vmName string) (*models.VM, error)

	AddVMDisks(vmName string, volumeIDs []string, mountBus models.Bus) (*models.Task, error)
	RemoveVMDisks(vmName string, diskIDs []string) (*models.Task, error)
	GetVMDisks(vmName string, volumeIDs []string) ([]*models.VMDisk, error)

	GetISCSILuns(lunIDs []string) ([]*models.IscsiLun, error)

	GetTask(id string) (*models.Task, error)

	CreateLabel(key, value string) (*models.Task, error)
	GetLabel(key, value string) (*models.Label, error)
	AddLabelsToVolume(volumeID string, labels []string) (*models.Task, error)
}

type towerService struct {
	client *towerclient.Cloudtower
}

func NewTowerService(cloudtower *towerclient.Cloudtower) TowerService {
	return &towerService{
		client: cloudtower,
	}
}

func (ts *towerService) CreateVMVolume(elfClusterID string, name string, storagePolicy models.VMVolumeElfStoragePolicyType,
	size uint64, sharing bool) (*models.Task, error) {
	getClusterParams := cluster.NewGetClustersParams()
	getClusterParams.RequestBody = &models.GetClustersRequestBody{Where: &models.ClusterWhereInput{
		OR: []*models.ClusterWhereInput{
			{
				LocalID: pointy.String(elfClusterID),
			},
			{
				ID: pointy.String(elfClusterID),
			},
		},
	}}

	getClusterRes, err := ts.client.Cluster.GetClusters(getClusterParams)
	if err != nil {
		return nil, err
	}

	if len(getClusterRes.Payload) == 0 {
		return nil, fmt.Errorf("failed to get SMTX OS ELF Cluster with id or local id: %v", elfClusterID)
	}

	createParams := vmvolume.NewCreateVMVolumeParams()
	createParams.RequestBody = []*models.VMVolumeCreationParams{
		{
			ClusterID:        getClusterRes.Payload[0].ID,
			ElfStoragePolicy: storagePolicy.Pointer(),
			Size:             pointy.Int64(int64(size)),
			Name:             pointy.String(name),
			Sharing:          pointy.Bool(sharing),
		},
	}

	createRes, err := ts.client.VMVolume.CreateVMVolume(createParams)
	if err != nil {
		return nil, err
	}

	return &models.Task{ID: createRes.Payload[0].TaskID}, nil
}

func (ts *towerService) DeleteVMVolume(volumeID string) (*models.Task, error) {
	deleteParams := vmvolume.NewDeleteVMVolumeFromVMParams()
	deleteParams.RequestBody = &models.VMVolumeDeletionParams{Where: &models.VMVolumeWhereInput{
		ID: pointy.String(volumeID),
	}}

	deleteRes, err := ts.client.VMVolume.DeleteVMVolumeFromVM(deleteParams)
	if err != nil {
		return nil, err
	}

	return &models.Task{ID: deleteRes.Payload[0].TaskID}, nil
}

func (ts *towerService) GetVMVolumeByName(volumeName string) (*models.VMVolume, error) {
	getParams := vmvolume.NewGetVMVolumesParams()
	getParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			Name: pointy.String(volumeName),
		},
	}

	getRes, err := ts.client.VMVolume.GetVMVolumes(getParams)
	if err != nil {
		return nil, err
	}

	if len(getRes.Payload) == 0 {
		return nil, ErrVMVolumeNotFound
	}

	return getRes.Payload[0], nil
}

func (ts *towerService) GetVMVolumeByID(volumeID string) (*models.VMVolume, error) {
	getVolumeParams := vmvolume.NewGetVMVolumesParams()
	getVolumeParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			ID: pointy.String(volumeID),
		},
	}

	getVolumeRes, err := ts.client.VMVolume.GetVMVolumes(getVolumeParams)
	if err != nil {
		return nil, err
	}

	if len(getVolumeRes.Payload) == 0 {
		return nil, ErrVMVolumeNotFound
	}

	return getVolumeRes.Payload[0], nil
}

func (ts *towerService) GetVMVolumesByID(volumeIDs []string) ([]*models.VMVolume, error) {
	if len(volumeIDs) == 0 {
		return nil, nil
	}

	getVolumeParams := vmvolume.NewGetVMVolumesParams()
	getVolumeParams.RequestBody = &models.GetVMVolumesRequestBody{
		Where: &models.VMVolumeWhereInput{
			IDIn: volumeIDs,
		},
	}

	getVolumeRes, err := ts.client.VMVolume.GetVMVolumes(getVolumeParams)
	if err != nil {
		return nil, err
	}

	return getVolumeRes.Payload, nil
}

func (ts *towerService) GetVM(vmName string) (*models.VM, error) {
	getVmParams := vm.NewGetVmsParams()
	getVmParams.RequestBody = &models.GetVmsRequestBody{
		Where: &models.VMWhereInput{
			Name: pointy.String(vmName),
		},
	}

	getVMRes, err := ts.client.VM.GetVms(getVmParams)
	if err != nil {
		return nil, err
	}

	if len(getVMRes.Payload) == 0 {
		return nil, ErrVMNotFound
	}

	return getVMRes.Payload[0], nil
}

func (ts *towerService) AddVMDisks(vmName string, volumeIDs []string, mountBus models.Bus) (*models.Task, error) {
	attachVM, err := ts.GetVM(vmName)
	if err != nil {
		return nil, err
	}

	addVMDiskParams := vm.NewAddVMDiskParams()
	addVMDiskParams.RequestBody = &models.VMAddDiskParams{
		Where: &models.VMWhereInput{
			Name: pointy.String(vmName),
		},
		Data: &models.VMAddDiskParamsData{
			VMDisks: &models.VMAddDiskParamsDataVMDisks{
				MountDisks: []*models.MountDisksParams{},
			},
		},
	}

	for index, volumeID := range volumeIDs {
		mountParam := &models.MountDisksParams{
			Index:      pointy.Int32(int32(index)),
			VMVolumeID: pointy.String(volumeID),
			Boot:       pointy.Int32(int32(len(attachVM.VMDisks) + 1 + index)),
			Bus:        mountBus.Pointer(),
		}
		addVMDiskParams.RequestBody.Data.VMDisks.MountDisks = append(addVMDiskParams.RequestBody.Data.VMDisks.MountDisks, mountParam)
	}

	addVMDiskResp, err := ts.client.VM.AddVMDisk(addVMDiskParams)
	if err != nil {
		return nil, err
	}

	if len(addVMDiskResp.Payload) == 0 {
		return nil, fmt.Errorf("failed to add VM Disk. VM name %s, volume %s", vmName, volumeIDs)
	}

	return &models.Task{ID: addVMDiskResp.Payload[0].TaskID}, nil
}

func (ts *towerService) RemoveVMDisks(vmName string, diskIDs []string) (*models.Task, error) {
	updateParams := vm.NewRemoveVMDiskParams()
	updateParams.RequestBody = &models.VMRemoveDiskParams{
		Where: &models.VMWhereInput{
			Name: pointy.String(vmName),
		},
		Data: &models.VMRemoveDiskParamsData{
			DiskIds: diskIDs,
		},
	}

	removeVMDiskResp, err := ts.client.VM.RemoveVMDisk(updateParams)
	if err != nil {
		return nil, err
	}

	if len(removeVMDiskResp.Payload) == 0 {
		return nil, fmt.Errorf("failed to remove VM Disk. VM name %s, volume %s", vmName, diskIDs)
	}

	return &models.Task{ID: removeVMDiskResp.Payload[0].TaskID}, nil
}

// GetVMDisks func will return all VM Disk in this VM when volumeIDs length is 0.
func (ts *towerService) GetVMDisks(vmName string, volumeIDs []string) ([]*models.VMDisk, error) {
	getVMDiskParams := vmdisk.NewGetVMDisksParams()
	getVMDiskParams.RequestBody = &models.GetVMDisksRequestBody{
		Where: &models.VMDiskWhereInput{
			VM: &models.VMWhereInput{
				Name: pointy.String(vmName),
			},
			VMVolume: &models.VMVolumeWhereInput{
				IDIn: volumeIDs,
			},
		},
	}

	getVmDiskRes, err := ts.client.VMDisk.GetVMDisks(getVMDiskParams)
	if err != nil {
		return nil, err
	}

	return getVmDiskRes.Payload, nil
}

func (ts *towerService) GetTask(id string) (*models.Task, error) {
	taskParams := task.NewGetTasksParams()
	taskParams.RequestBody = &models.GetTasksRequestBody{
		Where: &models.TaskWhereInput{
			ID: &id,
		},
	}

	tasks, err := ts.client.Task.GetTasks(taskParams)
	if err != nil {
		return nil, err
	}

	if len(tasks.Payload) == 0 {
		return nil, ErrTaskNotFound
	}

	return tasks.Payload[0], nil
}

func (ts *towerService) CreateLabel(key, value string) (*models.Task, error) {
	createLabelParams := clientlabel.NewCreateLabelParams()
	createLabelParams.RequestBody = []*models.LabelCreationParams{
		{Key: &key, Value: &value},
	}

	createLabelResp, err := ts.client.Label.CreateLabel(createLabelParams)
	if err != nil {
		return nil, err
	}

	if len(createLabelResp.Payload) == 0 {
		return nil, fmt.Errorf("create label for key %s value %s failed", key, value)
	}

	return &models.Task{ID: createLabelResp.Payload[0].TaskID}, nil
}

func (ts *towerService) GetLabel(key, value string) (*models.Label, error) {
	getLabelParams := clientlabel.NewGetLabelsParams()
	getLabelParams.RequestBody = &models.GetLabelsRequestBody{
		Where: &models.LabelWhereInput{
			Key:   &key,
			Value: &value,
		},
	}

	getLabelResp, err := ts.client.Label.GetLabels(getLabelParams)
	if err != nil {
		return nil, err
	}

	if len(getLabelResp.Payload) == 0 {
		return nil, ErrLabelNotFound
	}

	return getLabelResp.Payload[0], nil
}

func (ts *towerService) AddLabelsToVolume(volumeID string, labels []string) (*models.Task, error) {
	addLabelsParams := clientlabel.NewAddLabelsToResourcesParams()
	addLabelsParams.RequestBody = &models.AddLabelsToResourcesParams{
		Where: &models.LabelWhereInput{
			IDIn: labels,
		},
		Data: &models.AddLabelsToResourcesParamsData{
			VMVolumes: &models.VMVolumeWhereInput{
				ID: &volumeID,
			},
		},
	}

	addLabelsResp, err := ts.client.Label.AddLabelsToResources(addLabelsParams)
	if err != nil {
		return nil, err
	}

	if len(addLabelsResp.Payload) == 0 {
		return nil, fmt.Errorf("add label to volume %s failed", volumeID)
	}

	return &models.Task{ID: addLabelsResp.Payload[0].TaskID}, nil
}

func (ts *towerService) GetISCSILuns(lunIDs []string) ([]*models.IscsiLun, error) {
	getISCSILunParams := iscsilun.NewGetIscsiLunsParams()
	getISCSILunParams.RequestBody = &models.GetIscsiLunsRequestBody{
		Where: &models.IscsiLunWhereInput{
			IDIn: lunIDs,
		},
	}

	getIscsiLunResp, err := ts.client.IscsiLun.GetIscsiLuns(getISCSILunParams)
	if err != nil {
		return nil, err
	}

	return getIscsiLunResp.Payload, nil
}
