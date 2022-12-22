// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package driver

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openlyinc/pointy"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"github.com/smartxworks/elf-csi-driver/pkg/feature"
	"github.com/smartxworks/elf-csi-driver/pkg/service"
	"github.com/smartxworks/elf-csi-driver/pkg/service/mock_services"
	testutil "github.com/smartxworks/elf-csi-driver/pkg/testing/utils"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
	kmetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TestELFClusterID = "test-elf-cluster-id"

	TestClusterID = "test-cluster-id"
)

var _ = Describe("CSI Driver Controller Test", func() {
	ctx := context.Background()
	feature.Init()

	var (
		driver           *controllerServer
		mockCtrl         *gomock.Controller
		mockTowerService *mock_services.MockTowerService
		config           *DriverConfig
		vm               *models.VM
		logBuffer        *bytes.Buffer
	)

	vm = testutil.NewVM()
	nodeIp, err := getNodeIP()
	Expect(err).To(BeNil())
	client, err := utils.NewFakeConfigMapClient(testNamespace)
	Expect(err).To(BeNil())
	_, err = client.Create(context.Background(), newConfigMap(), kmetav1.CreateOptions{})
	Expect(err).To(BeNil())

	nm := NewNodeMap(testConfigMap, client)
	err = nm.Put(*vm.Name, &NodeEntry{
		NodeIP:       nodeIp,
		NodeName:     *vm.Name,
		LivenessPort: 9411,
	})
	Expect(err).To(BeNil())

	config = &DriverConfig{
		TowerClient:            mockTowerService,
		ClusterID:              TestClusterID,
		LivenessPort:           9411,
		NodeMap:                nm,
		PreferredVolumeBusType: string(models.BusVIRTIO),
	}

	// start node liveness server
	stopChan := make(chan struct{})
	err = newNodeLivenessServer(config).Run(stopChan)
	Expect(err).To(BeNil())

	BeforeEach(func() {
		klog.InitFlags(nil)
		// set log
		if err := flag.Set("logtostderr", "false"); err != nil {
			_ = fmt.Errorf("Error setting logtostderr flag")
		}

		logBuffer = new(bytes.Buffer)
		klog.SetOutput(logBuffer)

		mockCtrl = gomock.NewController(GinkgoT())
		mockTowerService = mock_services.NewMockTowerService(mockCtrl)
		config.TowerClient = mockTowerService
		driver = newControllerServer(config)
	})

	AfterSuite(func() {
		close(stopChan)
	})

	Context("Controller Create Volume", func() {
		It("it should return nil for create volume success", func() {
			taskStatusSuccess := models.TaskStatusSUCCESSED
			volume := testutil.NewVolume()
			volumeSize := defaultVolumeSize
			volume.Size = pointy.Int64(int64(volumeSize))
			createVolumeTask := testutil.NewTask()
			createVolumeTask.Status = &taskStatusSuccess
			addLabelTask := testutil.NewTask()
			addLabelTask.Status = &taskStatusSuccess
			commonLabel := testutil.NewLabel()
			systemLabel := testutil.NewLabel()
			keepAfterVMDeleteLabel := testutil.NewLabel()

			mockTowerService.EXPECT().GetVMVolumeByName(*volume.Name).Return(nil, service.ErrVMVolumeNotFound)
			mockTowerService.EXPECT().GetVMVolumeByName(*volume.Name).Return(volume, nil)
			mockTowerService.EXPECT().CreateVMVolume(TestELFClusterID, *volume.Name,
				models.VMVolumeElfStoragePolicyTypeREPLICA2THINPROVISION, uint64(volumeSize), false).Return(createVolumeTask, nil)
			mockTowerService.EXPECT().GetTask(*createVolumeTask.ID).Return(createVolumeTask, nil)
			mockTowerService.EXPECT().GetLabel(defaultClusterLabelKey, TestClusterID).Return(commonLabel, nil)
			mockTowerService.EXPECT().GetLabel(defaultSystemClusterLabelKey, TestClusterID).Return(systemLabel, nil)
			mockTowerService.EXPECT().GetLabel(defaultKeepAfterVMDeleteLabelKey, "true").Return(keepAfterVMDeleteLabel, nil)
			mockTowerService.EXPECT().AddLabelsToVolume(*volume.ID, []string{*commonLabel.ID, *systemLabel.ID, *keepAfterVMDeleteLabel.ID}).Return(addLabelTask, nil)
			mockTowerService.EXPECT().GetTask(*addLabelTask.ID).Return(addLabelTask, nil)

			createVolumeRequest := testutil.NewCreateVolumeRequest(*volume.Name, TestELFClusterID, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, int64(volumeSize))

			_, err := driver.CreateVolume(ctx, createVolumeRequest)
			Expect(err).To(BeNil())
		})

		It("it should return error when volume is in creating", func() {
			volume := testutil.NewVolume()
			volume.LocalID = pointy.String("placeholder-xxx-xxx-xxx")
			volumeSize := defaultVolumeSize
			createVolumeRequest := testutil.NewCreateVolumeRequest(*volume.Name, TestELFClusterID, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, int64(volumeSize))

			mockTowerService.EXPECT().GetVMVolumeByName(*volume.Name).Return(volume, nil)

			_, err := driver.CreateVolume(ctx, createVolumeRequest)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).Should(ContainSubstring("creating now"))
		})

		It("it should return error when get volume failed", func() {
			volume := testutil.NewVolume()
			volumeSize := defaultVolumeSize
			createVolumeRequest := testutil.NewCreateVolumeRequest(*volume.Name, TestELFClusterID, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, int64(volumeSize))

			mockTowerService.EXPECT().GetVMVolumeByName(*volume.Name).Return(nil, errors.New("get volume failed"))

			_, err := driver.CreateVolume(ctx, createVolumeRequest)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).Should(ContainSubstring("get volume failed"))
		})

		It("it should return error when create volume failed", func() {
			volume := testutil.NewVolume()
			volumeSize := defaultVolumeSize
			createVolumeRequest := testutil.NewCreateVolumeRequest(*volume.Name, TestELFClusterID, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, int64(volumeSize))

			mockTowerService.EXPECT().GetVMVolumeByName(*volume.Name).Return(nil, service.ErrVMVolumeNotFound)
			mockTowerService.EXPECT().CreateVMVolume(TestELFClusterID, *volume.Name,
				models.VMVolumeElfStoragePolicyTypeREPLICA2THINPROVISION, uint64(volumeSize), false).Return(nil, errors.New("create volume failed"))

			_, err := driver.CreateVolume(ctx, createVolumeRequest)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).Should(ContainSubstring("create volume failed"))
		})

		It("it should return error when create volume task failed", func() {
			taskStatusFailed := models.TaskStatusFAILED
			volume := testutil.NewVolume()
			volumeSize := defaultVolumeSize
			createVolumeTask := testutil.NewTask()
			createVolumeTask.Status = &taskStatusFailed
			createVolumeRequest := testutil.NewCreateVolumeRequest(*volume.Name, TestELFClusterID, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, int64(volumeSize))

			mockTowerService.EXPECT().GetVMVolumeByName(*volume.Name).Return(nil, service.ErrVMVolumeNotFound)
			mockTowerService.EXPECT().CreateVMVolume(TestELFClusterID, *volume.Name,
				models.VMVolumeElfStoragePolicyTypeREPLICA2THINPROVISION, uint64(volumeSize), false).Return(createVolumeTask, nil)
			mockTowerService.EXPECT().GetTask(*createVolumeTask.ID).Return(createVolumeTask, nil)

			_, err := driver.CreateVolume(ctx, createVolumeRequest)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).Should(ContainSubstring("failed"))
		})
	})

	Context("Controller Publish Volume", func() {
		It("it should return error when VM is in publishing", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			driver.keyMutex.LockKey(*vm.Name)
			defer func() {
				_ = driver.keyMutex.UnlockKey(*vm.Name)
			}()
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).Should(ContainSubstring("is updating now"))

			attachVolumeList := driver.GetVolumesToBeAttachedAndReset(*vm.Name)
			Expect(len(attachVolumeList) == 1).Should(BeTrue())
			Expect(attachVolumeList[0] == *volume.ID).Should(BeTrue())
		})

		It("it should return nil when volume has published to VM", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return([]*models.VMDisk{{}}, nil).AnyTimes()

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())
			Expect(logBuffer.String()).Should(ContainSubstring("already published in VM"))
		})

		It("it should remove volume in attachVolumeList when volume has published to VM", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			driver.addVolumeToVolumesToBeAttached(*volume.ID, *vm.Name)
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return([]*models.VMDisk{{}}, nil).AnyTimes()

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())

			attachVolumeList := driver.GetVolumesToBeAttachedAndReset(*vm.Name)
			Expect(len(attachVolumeList) == 0).Should(BeTrue())
		})

		It("it should return error when get VMDisk failed", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, errors.New("get VMDisk failed"))

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err.Error()).Should(ContainSubstring("get VMDisk failed"))
		})

		It("it should return error when get VM failed", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(nil, service.ErrVMNotFound)
			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err.Error()).Should(ContainSubstring(fmt.Sprintf(VMNotFoundInTowerErrorMessage, *vm.Name)))
		})

		It("it should return error when volume not found", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume.ID}).Return(nil, nil)

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err.Error()).Should(ContainSubstring("VM Disk is not found"))
		})

		It("it should return error when volume is not sharing and mounted on other VM", func() {
			volume := testutil.NewVolume()
			volume.Sharing = pointy.Bool(false)
			volume.VMDisks = []*models.NestedVMDisk{{ID: pointy.String("fake")}}
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume.ID}).Return([]*models.VMVolume{volume}, nil)

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err.Error()).Should(ContainSubstring("VM Disk is not found"))
			Expect(logBuffer.String()).Should(ContainSubstring("still attached to another vm"))
		})

		It("it should return nil when volume is sharing and mounted on other VM", func() {
			volume := testutil.NewVolume()
			volume.Sharing = pointy.Bool(true)
			volume.VMDisks = []*models.NestedVMDisk{{ID: pointy.String("fake")}}
			addVMDiskTask := testutil.NewTask()
			taskStatusSuccess := models.TaskStatusSUCCESSED
			addVMDiskTask.Status = &taskStatusSuccess
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{}).Return(nil, nil)
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume.ID}).Return([]*models.VMVolume{volume}, nil)
			mockTowerService.EXPECT().AddVMDisks(*vm.Name, []string{*volume.ID}, models.BusVIRTIO).Return(addVMDiskTask, nil)
			mockTowerService.EXPECT().GetTask(*addVMDiskTask.ID).Return(addVMDiskTask, nil)

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())
			Expect(logBuffer.String()).Should(ContainSubstring("was already attached"))
		})

		It("it should return nil when volume is not sharing and has not mounted on other VM", func() {
			volume := testutil.NewVolume()
			volume.Sharing = pointy.Bool(false)
			addVMDiskTask := testutil.NewTask()
			taskStatusSuccess := models.TaskStatusSUCCESSED
			addVMDiskTask.Status = &taskStatusSuccess
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{}).Return(nil, nil)
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume.ID}).Return([]*models.VMVolume{volume}, nil)
			mockTowerService.EXPECT().AddVMDisks(*vm.Name, []string{*volume.ID}, models.BusSCSI).Return(addVMDiskTask, nil)
			mockTowerService.EXPECT().GetTask(*addVMDiskTask.ID).Return(addVMDiskTask, nil)

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())
		})

		It("it should mount to VM SCSI Bus when VM VIRTIO Bus is full mounted", func() {
			volume := testutil.NewVolume()
			volume.Sharing = pointy.Bool(false)
			addVMDiskTask := testutil.NewTask()
			taskStatusSuccess := models.TaskStatusSUCCESSED
			addVMDiskTask.Status = &taskStatusSuccess
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{}).Return(testutil.NewVMDisks(models.BusVIRTIO, 32), nil)
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume.ID}).Return([]*models.VMVolume{volume}, nil)
			mockTowerService.EXPECT().AddVMDisks(*vm.Name, []string{*volume.ID}, models.BusSCSI).Return(addVMDiskTask, nil)
			mockTowerService.EXPECT().GetTask(*addVMDiskTask.ID).Return(addVMDiskTask, nil)

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())
		})

		It("it should return failed when all VM Bus is full mounted", func() {
			volume := testutil.NewVolume()
			volume.Sharing = pointy.Bool(false)
			addVMDiskTask := testutil.NewTask()
			taskStatusSuccess := models.TaskStatusSUCCESSED
			addVMDiskTask.Status = &taskStatusSuccess
			controllerPublishVolumeRequest := testutil.NewControllerPublishVolumeRequest(*volume.ID, *vm.ID, false)
			vmDisks := append(testutil.NewVMDisks(models.BusVIRTIO, 32), testutil.NewVMDisks(models.BusSCSI, 32)...)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{}).Return(vmDisks, nil)
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume.ID}).Return([]*models.VMVolume{volume}, nil)

			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).ShouldNot(BeNil())
			Expect(err.Error()).Should(ContainSubstring("all VM Buses are full"))
		})

		It("it should return success when batch process volume", func() {
			volume1 := testutil.NewVolume()
			volume1.Sharing = pointy.Bool(false)
			volume2 := testutil.NewVolume()
			volume2.Sharing = pointy.Bool(false)

			addVMDiskTask := testutil.NewTask()
			taskStatusSuccess := models.TaskStatusSUCCESSED
			addVMDiskTask.Status = &taskStatusSuccess

			controllerPublishVolumeRequest1 := testutil.NewControllerPublishVolumeRequest(*volume1.ID, *vm.ID, false)
			controllerPublishVolumeRequest2 := testutil.NewControllerPublishVolumeRequest(*volume2.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, gomock.Any()).Return(nil, nil).AnyTimes()
			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMVolumesByID([]string{*volume1.ID, *volume2.ID}).Return([]*models.VMVolume{volume1, volume2}, nil)
			mockTowerService.EXPECT().AddVMDisks(*vm.Name, []string{*volume1.ID, *volume2.ID}, models.BusVIRTIO).Return(addVMDiskTask, nil)
			mockTowerService.EXPECT().GetTask(*addVMDiskTask.ID).Return(addVMDiskTask, nil)

			driver.keyMutex.LockKey(*vm.Name)
			_, err := driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest1)
			Expect(err).NotTo(BeNil())
			_, err = driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest2)
			Expect(err).NotTo(BeNil())
			Expect(len(driver.volumesToBeAttachedMap[*vm.Name]) == 2).Should(BeTrue())

			_ = driver.keyMutex.UnlockKey(*vm.Name)
			_, err = driver.ControllerPublishVolume(ctx, controllerPublishVolumeRequest1)
			Expect(err).To(BeNil())
			Expect(len(driver.volumesToBeAttachedMap[*vm.Name]) == 0).Should(BeTrue())
		})
	})

	Context("Controller Unpublish Volume", func() {
		It("it should return nil when volume is mounted on this VM", func() {
			volume := testutil.NewVolume()
			vmDisk := testutil.NewVMDisk()
			vmDisk.VMVolume = &models.NestedVMVolume{ID: volume.ID}
			removeVMDiskTask := testutil.NewTask()
			taskStatusSuccess := models.TaskStatusSUCCESSED
			removeVMDiskTask.Status = &taskStatusSuccess
			controllerPublishVolumeRequest := testutil.NewControllerUnpublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVM(*vm.Name).Return(vm, nil)
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return([]*models.VMDisk{vmDisk}, nil).Times(2)
			mockTowerService.EXPECT().RemoveVMDisks(*vm.Name, []string{*vmDisk.ID}).Return(removeVMDiskTask, nil)
			mockTowerService.EXPECT().GetTask(*removeVMDiskTask.ID).Return(removeVMDiskTask, nil)

			_, err := driver.ControllerUnpublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())
		})

		It("it should return nil when volume has unpublished from VM", func() {
			volume := testutil.NewVolume()
			controllerPublishVolumeRequest := testutil.NewControllerUnpublishVolumeRequest(*volume.ID, *vm.ID, false)

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return([]*models.VMDisk{}, nil)

			_, err := driver.ControllerUnpublishVolume(ctx, controllerPublishVolumeRequest)
			Expect(err).To(BeNil())
		})

		It("it should remove volume in DetachVolumeList when volume has unpublished to VM", func() {
			volume := testutil.NewVolume()
			controllerUnpublishVolumeRequest := testutil.NewControllerUnpublishVolumeRequest(*volume.ID, *vm.ID, false)

			driver.addVolumeToVolumesToBeDetached(*volume.ID, *vm.Name)
			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return([]*models.VMDisk{}, nil)

			_, err := driver.ControllerUnpublishVolume(ctx, controllerUnpublishVolumeRequest)
			Expect(err).To(BeNil())

			detachVolumeList := driver.GetVolumesToBeDetachedAndReset(*vm.Name)
			Expect(len(detachVolumeList) == 0).Should(BeTrue())
		})

		It("it should return error when VM is in unpublishing", func() {
			volume := testutil.NewVolume()
			controllerUnpublishVolumeRequest := testutil.NewControllerUnpublishVolumeRequest(*volume.ID, *vm.ID, false)

			driver.keyMutex.LockKey(*vm.Name)
			defer func() {
				_ = driver.keyMutex.UnlockKey(*vm.Name)
			}()

			mockTowerService.EXPECT().GetVMDisks(*vm.Name, []string{*volume.ID}).Return([]*models.VMDisk{{}}, nil)

			_, err := driver.ControllerUnpublishVolume(ctx, controllerUnpublishVolumeRequest)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).Should(ContainSubstring("is updating now"))

			detachVolumeList := driver.GetVolumesToBeDetachedAndReset(*vm.Name)
			Expect(len(detachVolumeList) == 1).Should(BeTrue())
			Expect(detachVolumeList[0] == *volume.ID).Should(BeTrue())
		})
	})

	Context("Controller Delete Volume", func() {
		It("it should return nil when volume has deleted", func() {
			volume := testutil.NewVolume()
			deleteVolumeRequest := testutil.NewDeleteVolumeRequest(*volume.ID)

			mockTowerService.EXPECT().GetVMVolumeByID(*volume.ID).Return(nil, service.ErrVMVolumeNotFound)

			_, err := driver.DeleteVolume(ctx, deleteVolumeRequest)
			Expect(err).To(BeNil())
		})

		It("it should return nil when volume has deleted", func() {
			volume := testutil.NewVolume()
			deleteVolumeTask := &models.Task{}
			deleteVolumeRequest := testutil.NewDeleteVolumeRequest(*volume.ID)

			mockTowerService.EXPECT().GetVMVolumeByID(*volume.ID).Return(volume, nil)
			mockTowerService.EXPECT().DeleteVMVolume(*volume.ID).Return(deleteVolumeTask, nil)

			_, err := driver.DeleteVolume(ctx, deleteVolumeRequest)
			Expect(err).To(BeNil())
		})
	})
})
