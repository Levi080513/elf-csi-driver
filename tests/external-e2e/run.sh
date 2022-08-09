# Copyright (c) 2021 SMARTX
# All rights reserved.

set -xe

DRIVER="com.smartx.elf-csi-driver"
KUBEVERSION=$(kubectl version | grep "Server Version" | grep -oP "GitVersion:\"\Kv[0-9]*\.[0-9]*").0
KUBECONFIG="/root/.kube/config"

# k8s external e2e test does not exist in the v1.13 version, use v1.14 instead
if [[ $KUBEVERSION == "v1.13.0" ]]; then
	KUBEVERSION="v1.14.0"
fi

# download k8s external e2e binary
if [[ ! -e kubernetes ]]; then
	curl -sL https://storage.googleapis.com/kubernetes-release/release/${KUBEVERSION}/kubernetes-test-linux-amd64.tar.gz --output e2e-tests.tar.gz
	tar -xvf e2e-tests.tar.gz && rm e2e-tests.tar.gz
fi

mkdir -p /tmp/csi
cp tests/external-e2e/testdata/* /tmp/csi/

ginkgo --v -focus="External.Storage" -skip="\[Disruptive\]|\[Slow\]" kubernetes/test/bin/e2e.test  -- -storage.testdriver=/tmp/csi/driver.yaml --kubeconfig=$KUBECONFIG
