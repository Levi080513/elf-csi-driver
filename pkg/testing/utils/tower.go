// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"github.com/google/uuid"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
)

func NewVMDisks(bus models.Bus, number int) []*models.VMDisk {
	vmdisks := make([]*models.VMDisk, number)
	for i := 0; i < number; i++ {
		vmDiskId := uuid.New().String()
		vmdisks[i] = &models.VMDisk{
			ID:  &vmDiskId,
			Bus: &bus,
		}
	}

	return vmdisks
}

func NewVMDisk() *models.VMDisk {
	vmDiskId := uuid.New().String()

	return &models.VMDisk{
		ID: &vmDiskId,
	}
}

func NewLabel() *models.Label {
	labelId := uuid.New().String()

	return &models.Label{
		ID: &labelId,
	}
}

func NewTask() *models.Task {
	taskId := uuid.New().String()

	return &models.Task{
		ID: &taskId,
	}
}

func NewVolume() *models.VMVolume {
	volumeId := uuid.New().String()

	return &models.VMVolume{
		ID:   &volumeId,
		Name: &volumeId,
	}
}

func NewVM() *models.VM {
	vmId := uuid.New().String()

	return &models.VM{
		ID:   &vmId,
		Name: &vmId,
	}
}
