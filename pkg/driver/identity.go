// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/openlyinc/pointy"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/smartxworks/cloudtower-go-sdk/v2/client/user"
	"k8s.io/klog"
)

type identityServer struct {
	driver *Driver
}

func newIdentityServer(driver *Driver) *identityServer {
	return &identityServer{
		driver: driver,
	}
}

func (i *identityServer) GetPluginInfo(
	ctx context.Context,
	req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          i.driver.config.DriverName,
		VendorVersion: i.driver.config.Version,
	}, nil
}

func (i *identityServer) GetPluginCapabilities(
	ctx context.Context,
	req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_VolumeExpansion_{
					VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
						Type: csi.PluginCapability_VolumeExpansion_ONLINE,
					},
				},
			},
		},
	}, nil
}

func (i *identityServer) Probe(
	ctx context.Context,
	req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	getUserParams := user.NewGetUsersParams()
	getUserParams.RequestBody = &models.GetUsersRequestBody{
		First: pointy.Int32(1),
	}

	_, err := i.driver.config.TowerClient.User.GetUsers(getUserParams)
	if err != nil {
		klog.Warningf("failed to show user info, %+v", err)
	}

	return &csi.ProbeResponse{
		Ready: &wrappers.BoolValue{
			Value: err == nil,
		},
	}, nil
}
