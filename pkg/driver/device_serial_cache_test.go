package driver

import (
	"os"
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
	testDeviceSymlinkDir = "/tmp/mock/disk"
	testDeviceDir        = "/tmp/mock/device"

	timeout = time.Second * 10
)

var _ = Describe("Device Serial Cache Test", func() {
	var (
		err                       error
		deviceSerialCacheInstance *deviceSerialCache
		stopCh                    chan struct{}
	)

	BeforeEach(func() {
		err = os.Mkdir(testDeviceSymlinkDir, 0755)
		Expect(err).Should(BeNil())
		err = os.Mkdir(testDeviceDir, 0755)
		Expect(err).Should(BeNil())

		stopCh = make(chan struct{}, 1)

		config := &DriverConfig{
			OsUtil: utils.NewOsUtil(),
		}

		deviceSerialCacheInstance = NewDeviceSerialCache(config)
		deviceSerialCacheInstance.Run(stopCh)
	})

	AfterEach(func() {
		err = os.RemoveAll(testDeviceSymlinkDir)
		Expect(err).Should(BeNil())
		err = os.RemoveAll(testDeviceDir)
		Expect(err).Should(BeNil())
		close(stopCh)
	})

	Context("Device Add", func() {
		It("Test Device Add", func() {
			testVolumeID := uuid.New().String()

			deviceSymlinkPath, err := testutil.CreateDeviceSymlinkForVolumeID(testVolumeID, testDeviceDir, testDeviceSymlinkDir, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCacheInstance.serialPrefixToDeviceCacheMap) == 0 {
					return false
				}
				serial := strings.Split(deviceSymlinkPath, symlinkPrefixForAttachedSCSIBus)[1]
				if _, ok := deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial]; !ok {
					return false
				}
				if deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial] != testVolumeID {
					return false
				}
				return true
			}, timeout).Should(BeTrue())
		})
	})

	Context("Device Remove", func() {
		It("Test Device Remove", func() {
			testVolumeID := uuid.New().String()

			deviceSymlinkPath, err := testutil.CreateDeviceSymlinkForVolumeID(testVolumeID, testDeviceDir, testDeviceSymlinkDir, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCacheInstance.serialPrefixToDeviceCacheMap) == 0 {
					return false
				}
				serial := strings.Split(deviceSymlinkPath, symlinkPrefixForAttachedSCSIBus)[1]
				if _, ok := deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial]; !ok {
					return false
				}
				if deviceSerialCacheInstance.serialPrefixToDeviceCacheMap[serial] != testVolumeID {
					return false
				}
				return true
			}, timeout).Should(BeTrue())

			err = testutil.RemoveDeviceSymlinkForVolumeID(testVolumeID, testDeviceDir, testDeviceSymlinkDir, models.BusVIRTIO)
			Expect(err).Should(BeNil())
			Eventually(func() bool {
				if len(deviceSerialCacheInstance.serialPrefixToDeviceCacheMap) == 0 {
					return true
				}
				return false
			}, timeout).Should(BeTrue())
		})
	})

	Context("Device Serial Cache Exit", func() {
		It("Test Device Serial Cache Exit", func() {
			// close run
			stopCh <- struct{}{}

			testVolumeID := uuid.New().String()

			_, err := testutil.CreateDeviceSymlinkForVolumeID(testVolumeID, testDeviceDir, testDeviceSymlinkDir, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCacheInstance.serialPrefixToDeviceCacheMap) == 0 {
					return true
				}
				return false
			}, timeout).Should(BeTrue())
		})
	})
})
