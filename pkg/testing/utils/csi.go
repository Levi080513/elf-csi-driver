// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
)

func NewDeleteVolumeRequest(volumeId string) *csi.DeleteVolumeRequest {
	return &csi.DeleteVolumeRequest{
		VolumeId: volumeId,
	}
}

func NewCreateVolumeRequest(pvName, elfClusterID string, accessMode csi.VolumeCapability_AccessMode_Mode, size int64) *csi.CreateVolumeRequest {
	params := make(map[string]string)

	params["elfCluster"] = elfClusterID

	capabilities := []*csi.VolumeCapability{
		{
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: accessMode,
			},
		},
	}

	reqCreate := &csi.CreateVolumeRequest{
		Name: pvName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: size,
		},
		Parameters:         params,
		VolumeCapabilities: capabilities,
	}

	return reqCreate
}

func NewControllerPublishVolumeRequest(volumeID, nodeID string, readOnly bool) *csi.ControllerPublishVolumeRequest {
	reqControllerPublishVolume := &csi.ControllerPublishVolumeRequest{
		VolumeId:         volumeID,
		NodeId:           nodeID,
		VolumeCapability: &csi.VolumeCapability{},
		Readonly:         false,
	}

	return reqControllerPublishVolume
}

func NewControllerUnpublishVolumeRequest(volumeID, nodeID string, readOnly bool) *csi.ControllerUnpublishVolumeRequest {
	reqControllerPublishVolume := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volumeID,
		NodeId:   nodeID,
	}

	return reqControllerPublishVolume
}
