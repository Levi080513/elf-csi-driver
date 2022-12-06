// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"errors"
)

var (
	ErrVMVolumeNotFound = errors.New("volume is not found in Tower")
	ErrVMNotFound       = errors.New("VM is not found in Tower")
)

const (
	VMNotFoundInTowerErrorMessage = "The VM %s is not present in SMTX OS ELF or moved to the recycle bin."
)
