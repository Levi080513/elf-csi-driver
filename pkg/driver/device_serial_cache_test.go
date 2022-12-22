package driver

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"

	testutil "github.com/smartxworks/elf-csi-driver/pkg/testing/utils"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

var (
	timeout = time.Second * 10
)

var _ = Describe("Device Serial Cache Test", func() {
	var (
		deviceSerialCacheInstance *deviceSerialCache
		stopCh                    chan struct{}
		testVolumeID              string
	)

	BeforeEach(func() {
		stopCh = make(chan struct{}, 1)

		config := &DriverConfig{
			OsUtil: utils.NewFakeOsUtil(),
		}

		deviceSerialCacheInstance = NewDeviceSerialCache(config)
		deviceSerialCacheInstance.Run(stopCh)

		testVolumeID = uuid.New().String()
	})

	AfterEach(func() {
		_ = testutil.RemoveDeviceSymlinkForVolumeID(testVolumeID, "/dev", DevDiskIDPath, models.BusVIRTIO)

		close(stopCh)
	})

	Context("Device Add", func() {
		It("Test Device Add", func() {
			deviceSymlinkPath, err := testutil.CreateDeviceSymlinkForVolumeID(testVolumeID, "/dev", DevDiskIDPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())
			Eventually(func() bool {
				deviceSerialCacheInstance.rLock.Lock()
				defer deviceSerialCacheInstance.rLock.Unlock()
				serial := strings.Split(deviceSymlinkPath, symlinkPrefixForAttachedVIRTIOBus)[1]

				if _, ok := deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial]; !ok {
					fmt.Println(serial)
					fmt.Println(deviceSerialCacheInstance.serialPrefixToDeviceCacheMap)
					return false
				}
				if deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial] != testVolumeID {
					fmt.Println(testVolumeID == deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial])
					fmt.Println(testVolumeID)
					fmt.Println(deviceSymlinkPath)
					fmt.Println(deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial])
					fmt.Println(serial)
					return false
				}
				return true
			}, timeout).Should(BeTrue())
		})
	})

	Context("Device Remove", func() {
		It("Test Device Remove", func() {
			deviceSymlinkPath, err := testutil.CreateDeviceSymlinkForVolumeID(testVolumeID, "/dev", DevDiskIDPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				deviceSerialCacheInstance.rLock.Lock()
				defer deviceSerialCacheInstance.rLock.Unlock()
				serial := strings.Split(deviceSymlinkPath, symlinkPrefixForAttachedVIRTIOBus)[1]
				if _, ok := deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial]; !ok {
					return false
				}
				if deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial] != testVolumeID {
					return false
				}
				return true
			}, timeout).Should(BeTrue())

			err = testutil.RemoveDeviceSymlinkForVolumeID(testVolumeID, "/dev", DevDiskIDPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())
			Eventually(func() bool {
				deviceSerialCacheInstance.rLock.Lock()
				defer deviceSerialCacheInstance.rLock.Unlock()
				if _, ok := deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[testVolumeID]; ok {
					return false
				}
				return true
			}, timeout).Should(BeTrue())
		})
	})

	Context("Device Serial Cache Exit", func() {
		It("Test Device Serial Cache Exit", func() {
			// close run
			stopCh <- struct{}{}

			_, err := testutil.CreateDeviceSymlinkForVolumeID(testVolumeID, "/dev", DevDiskIDPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				deviceSerialCacheInstance.rLock.Lock()
				defer deviceSerialCacheInstance.rLock.Unlock()
				if _, ok := deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[testVolumeID]; ok {
					return false
				}
				return true
			}, timeout).Should(BeTrue())
		})
	})
})
