// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"fmt"
	"net/rpc"
)

// CSI requires ControllerPublihsVolume to detect the health status of the target node,
// such as whether it is healthy or not, and the volume limit is mounted.
// Since the target node should provide a liveness probe interface.
type NodeLivenessServer struct {
	config *DriverConfig
}

func newNodeLivenessServer(config *DriverConfig) *NodeLivenessServer {
	return &NodeLivenessServer{
		config: config,
	}
}

func (s *NodeLivenessServer) Run(stopCh <-chan struct{}) error {
	livenessServer := rpc.NewServer()

	err := livenessServer.Register(s)
	if err != nil {
		return err
	}

	nodeIP, err := getNodeIP()
	if err != nil {
		return err
	}

	listener, err := makeListener(fmt.Sprintf("tcp://%v:%v", nodeIP, s.config.LivenessPort))

	if err != nil {
		return err
	}

	go func() {
		<-stopCh

		_ = listener.Close()
	}()

	go func() {
		livenessServer.Accept(listener)
	}()

	return nil
}

type NodeLivenessReq struct {
}

type NodeLivenessRsp struct {
	Health bool
}

func (s *NodeLivenessServer) Liveness(req *NodeLivenessReq, rsp *NodeLivenessRsp) error {
	rsp.Health = true
	return nil
}
