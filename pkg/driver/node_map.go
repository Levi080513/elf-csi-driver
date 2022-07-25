// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kmetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

type NodeEntry struct {
	NodeName     string `json:"nodeName"`
	NodeIP       string `json:"nodeIP"`
	LivenessPort int    `json:"livenessPort"`
}

type nodeMap struct {
	client kcorev1.ConfigMapInterface
	name   string
}

func NewNodeMap(name string, client kcorev1.ConfigMapInterface) *nodeMap {
	return &nodeMap{
		client: client,
		name:   name,
	}
}

func (m *nodeMap) Put(key string, entry *NodeEntry) error {
	configMap, err := m.client.Get(context.Background(), m.name, kmetav1.GetOptions{})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get configMap %v, %v",
			m.name, err)
	}

	if len(configMap.BinaryData) == 0 {
		configMap.BinaryData = make(map[string][]byte)
	}

	entryJson, ok := configMap.BinaryData[key]
	if ok {
		oldEntry := &NodeEntry{}

		err = json.Unmarshal(entryJson, oldEntry)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid old entry %+v, %v", entryJson, err)
		}
	}

	entryJsonByte, err := json.Marshal(entry)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid entry %+v, %v", entry, err)
	}

	configMap.BinaryData[key] = entryJsonByte

	_, err = m.client.Update(context.Background(), configMap, kmetav1.UpdateOptions{})
	if err != nil {
		return status.Errorf(codes.Internal,
			"failed to update configMap %v, key %v, entry %+v, %v", m.name, key, entry, err)
	}

	return nil
}

func (m *nodeMap) Get(key string) (*NodeEntry, error) {
	configMap, err := m.client.Get(context.Background(), m.name, kmetav1.GetOptions{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get configMap %v, %v", m.name, err)
	}

	entryJson, ok := configMap.BinaryData[key]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "not found key %v in configMap %v", key, m.name)
	}

	entry := &NodeEntry{}

	// TODO(weipengzhu) allow to delete a invalid entry
	err = json.Unmarshal(entryJson, entry)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"invalid enrty, key %v, entry %v, %v", key, entryJson, err)
	}

	return entry, nil
}
