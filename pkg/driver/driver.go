// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	snapshotclientset "github.com/kubernetes-csi/external-snapshotter/client/v4/clientset/versioned"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"github.com/smartxworks/elf-csi-driver/pkg/service"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	NODE       = "node"
	CONTROLLER = "controller"
	ALL        = "all" // for test
)

const (
	ext2FS    = "ext2"
	ext3FS    = "ext3"
	ext4FS    = "ext4"
	xfsFS     = "xfs"
	defaultFS = ext4FS
)

var supportedFS = map[string]bool{
	ext2FS: true,
	ext3FS: true,
	ext4FS: true,
	xfsFS:  true,
}

const (
	// this method will not be logged by grpcLogInterceptor
	shouldIgnoredMethod = "/csi.v1.Identity/Probe"
)

var mountModeAccessModes = map[csi.VolumeCapability_AccessMode_Mode]bool{
	csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY: true,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:      true,
	csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:  true,
}

var blockModeAccessModes = map[csi.VolumeCapability_AccessMode_Mode]bool{
	csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:  true,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:       true,
	csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:  true,
	csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:   true,
	csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER: true,
}

type DriverConfig struct {
	// driver info
	DriverName string
	Version    string

	// csi role
	Role string

	// kubernetes cluster id
	ClusterID string

	// preferred VM bus for volume attach to VM
	PreferredVolumeBusType string

	// node id to identify node
	NodeID string
	// csi grpc server listen addr
	ServerAddr string
	// node liveness port
	LivenessPort   int
	KubeClient     kubernetes.Interface
	SnapshotClient snapshotclientset.Interface
	NodeMap        *nodeMap      // iqn node map
	Mount          utils.Mount   // mount utils
	Resizer        utils.Resizer // resize utils
	OsUtil         utils.OsUtil  // os utils

	TowerClient service.TowerService
}

type Driver struct {
	config             *DriverConfig
	controller         csi.ControllerServer
	node               NodeServer
	identity           *identityServer
	nodeLivenessServer *NodeLivenessServer
	grpcServer         *grpc.Server
}

func NewDriver(config *DriverConfig) (*Driver, error) {
	driver := &Driver{
		config: config,
	}

	driver.identity = newIdentityServer(driver)

	if config.Role == NODE || config.Role == ALL {

		driver.node = newNodeServer(config)

		driver.nodeLivenessServer = newNodeLivenessServer(config)
	}

	if config.Role == CONTROLLER || config.Role == ALL {
		driver.controller = newControllerServer(config)
	}

	return driver, nil
}

func (d *Driver) Run(stopCh <-chan struct{}) error {
	go func() {
		<-stopCh
		d.stop()
	}()

	// log req, rsp, err
	grpcLogInterceptor := func(ctx context.Context, req interface{},
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		shouldLog := (info.FullMethod != shouldIgnoredMethod)

		if shouldLog {
			klog.Infof("start call %v, req %+v\n", info.FullMethod, req)
		}

		var resp interface{}

		resp, err := handler(ctx, req)
		if err != nil {
			klog.Errorf("call %v, req %+v, resp %+v, %v\n", info.FullMethod, req, resp, err)
		} else if shouldLog {
			klog.Infof("call %v, req %+v, resp %+v\n", info.FullMethod, req, resp)
		}

		return resp, err
	}

	d.grpcServer = grpc.NewServer(grpc.UnaryInterceptor(grpcLogInterceptor))

	if d.identity != nil {
		csi.RegisterIdentityServer(d.grpcServer, d.identity)
	}

	if d.config.Role == ALL || d.config.Role == CONTROLLER {
		csi.RegisterControllerServer(d.grpcServer, d.controller)
	}

	if d.config.Role == ALL || d.config.Role == NODE {
		err := d.node.registerNodeEntry()
		if err != nil {
			return err
		}

		err = d.nodeLivenessServer.Run(stopCh)
		if err != nil {
			return err
		}

		err = d.node.Run(stopCh)
		if err != nil {
			return err
		}

		csi.RegisterNodeServer(d.grpcServer, d.node)
	}

	listener, err := makeListener(d.config.ServerAddr)
	if err != nil {
		return err
	}

	return d.grpcServer.Serve(listener)
}

func (d *Driver) stop() {
	if d.grpcServer != nil {
		d.grpcServer.Stop()
	}
}
