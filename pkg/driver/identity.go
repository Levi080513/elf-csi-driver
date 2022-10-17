// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes/wrappers"
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
	return &csi.ProbeResponse{
		Ready: &wrappers.BoolValue{
			Value: true,
		},
	}, nil
}
