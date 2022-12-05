// Copyright (c) 2020-2022 SMARTX
// All rights reserved

// Package feature implements feature functionality.
package feature

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// Enable batch publish/unpublush volume to/from VM
	//
	// beta: v0.4.7
	BatchProcessVolume featuregate.Feature = "BatchProcessVolume"
)

func Init() {
	runtime.Must(MutableGates.Add(defaultELFCSIFeatureGates))
}

// defaultKubernetesFeatureGates consists of all known elf-csi-specific feature keys.
// To add a new feature, define a key for it above and add it here.
var defaultELFCSIFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	// Every feature should be initiated here:
	BatchProcessVolume: {Default: true, PreRelease: featuregate.Beta},
}
