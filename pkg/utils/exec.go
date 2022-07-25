// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"context"
	"os/exec"

	"k8s.io/klog/v2"
)

const (
	chrootBinary = "chroot"
)

func ExecCommandChroot(ctx context.Context, newRoot string, commandWithArgs ...string) ([]byte, error) {
	klog.Infof("chroot run cmd: %v", commandWithArgs)

	chrootArgs := append([]string{newRoot}, commandWithArgs...)

	return exec.CommandContext(ctx, chrootBinary, chrootArgs...).CombinedOutput()
}
