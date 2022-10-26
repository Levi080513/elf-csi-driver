// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package suites

import (
	"os"

	"k8s.io/klog"
)

const (
	ELFCluster = "elfCluster"
)

func NewStorageClassParameterGroups() []map[string]string {
	elfClusterID := GetELFClusterID()

	return []map[string]string{
		{
			"replicaFactor": "2",
			"thinProvision": "true",
			ELFCluster:      elfClusterID,
		},
		{
			"replicaFactor": "3",
			"thinProvision": "true",
			ELFCluster:      elfClusterID,
		},
		{
			"replicaFactor": "2",
			"thinProvision": "false",
			ELFCluster:      elfClusterID,
		},
		{
			"replicaFactor": "3",
			"thinProvision": "false",
			ELFCluster:      elfClusterID,
		},
	}
}

func GetDriverName() string {
	driverName, ok := os.LookupEnv("DRIVER_NAME")
	if !ok {
		klog.Fatal("failed to get DRIVER_NAME")
	}

	return driverName
}

func GetELFClusterID() string {
	elfClusterID, ok := os.LookupEnv("ELF_CLUSTER_ID")
	if !ok {
		klog.Fatal("failed to get ELF_CLUSTER_ID")
	}

	return elfClusterID
}
