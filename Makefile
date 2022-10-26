VERSION ?= $(shell git describe --tags --always --dirty)
LATEST ?= false

IMAGE_REPO ?= registry.smtx.io
IMAGE_PROJECT ?= elf
IMAGE_PREFIX ?= ${IMAGE_REPO}/${IMAGE_PROJECT}/
IMAGE_TAG ?= ${shell echo $(VERSION) | awk -F '/' '{print $$NF}'}
ELF_CSI_DRIVER_IMAGE := $(IMAGE_PREFIX)csi-driver
ELF_CSI_DRIVER_FILE := ${IMAGE_PROJECT}-csi-driver-$(VERSION)
GO := go
SHELL := /bin/bash

all: csi-driver

csi-driver:
	$(GO) build -o bin/$@ ./cmd/$@

docker-build:
	go mod vendor
	docker build -t $(ELF_CSI_DRIVER_IMAGE):$(VERSION) .
ifeq ($(LATEST),true)
	docker tag $(ELF_CSI_DRIVER_IMAGE):$(VERSION) $(ELF_CSI_DRIVER_IMAGE):latest
endif

docker-push: docker-build
	docker login ${IMAGE_REPO} -u ${IMAGE_PUSH_USERNAME} -p ${IMAGE_PUSH_TOKEN}
	docker push $(ELF_CSI_DRIVER_IMAGE):$(VERSION)
ifeq ($(LATEST),true)
	docker push $(ELF_CSI_DRIVER_IMAGE):latest
endif

docker-save: docker-build
	docker save $(ELF_CSI_DRIVER_IMAGE) > $(ELF_CSI_DRIVER_FILE).tar

ENVTEST_ASSETS_DIR=$(shell pwd)/bin
test:
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.8.3/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test -race -coverprofile coverage.txt -covermode=atomic $(shell go list ./... | grep -v 'e2e')

clean:
	$(GO) clean -i -x ./...
	rm -rf bin
	rm -rf $(ELF_CSI_DRIVER_IMAGE).tar

.PHONY: test clean
