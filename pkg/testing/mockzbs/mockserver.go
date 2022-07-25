// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package mockzbs

import (
	"github.com/iomesh/zbs-client-go/zbs"
	"github.com/iomesh/zbs-client-go/zbs/zrpc"
)

func StartMockServer() (*zrpc.Server, error) {
	server := zrpc.NewServer("127.0.0.1:0")

	if err := server.RegisterName("zbs.meta.MetaService", zrpc.NewMetaServer()); err != nil {
		return nil, err
	}

	if err := server.RegisterName("zbs.meta.ISCSIService", zrpc.NewISCSIServer()); err != nil {
		return nil, err
	}

	if err := server.Run(); err != nil {
		return nil, err
	}

	return server, nil
}

func GetZBSClient(server *zrpc.Server) (*zbs.Client, error) {
	metaAddr, _ := server.Addr()
	config := zrpc.DefaultClientConfig()
	config.Addr = metaAddr

	return zbs.NewClientWithConfig(config)
}
