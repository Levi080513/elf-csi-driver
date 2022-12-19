package driver

import (
	"os"
	"path"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"

	testutil "github.com/smartxworks/elf-csi-driver/pkg/testing/utils"
)

var (
	testDeviceSymlinkPath = "/tmp/disk"
	testDevicePath        = "/tmp/dev"

	timeout = time.Second * 10
)

var _ = Describe("Device Serial Cache Test", func() {
	var (
		err               error
		deviceSerialCache *DeviceSerialCache
		stopCh            chan struct{}
	)

	BeforeEach(func() {
		err = os.Mkdir(testDeviceSymlinkPath, 0755)
		Expect(err).Should(BeNil())
		stopCh = make(chan struct{}, 1)

		deviceSerialCache = NewDeviceSerialCache(testDeviceSymlinkPath)
		deviceSerialCache.Run(stopCh)
	})

	AfterEach(func() {
		err = os.RemoveAll(testDeviceSymlinkPath)
		Expect(err).Should(BeNil())
		close(stopCh)
	})

	Context("Device Add", func() {
		It("Test Device Add", func() {
			testDevice := "vda"

			deviceSymlinkPath, err := testutil.NewDeviceSymlink(path.Join(testDevicePath, testDevice), testDeviceSymlinkPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCache.serialPrefixToDeviceCacheMap) == 0 {
					return false
				}
				serial := strings.Split(deviceSymlinkPath, symlinkPrefixForAttachedSCSIBus)[1]
				if _, ok := deviceSerialCache.serialPrefixToDeviceCacheMap[serial]; !ok {
					return false
				}
				if deviceSerialCache.serialPrefixToDeviceCacheMap[serial] != testDevice {
					return false
				}
				return true
			}, timeout).Should(BeTrue())
		})
	})

	Context("Device Remove", func() {
		It("Test Device Remove", func() {
			testDevice := "vda"

			deviceSymlinkPath, err := testutil.NewDeviceSymlink(path.Join(testDevicePath, testDevice), testDeviceSymlinkPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCache.serialPrefixToDeviceCacheMap) == 0 {
					return false
				}
				serial := strings.Split(deviceSymlinkPath, symlinkPrefixForAttachedSCSIBus)[1]
				if _, ok := deviceSerialCache.serialPrefixToDeviceCacheMap[serial]; !ok {
					return false
				}
				if deviceSerialCache.serialPrefixToDeviceCacheMap[serial] != testDevice {
					return false
				}
				return true
			}, timeout).Should(BeTrue())

			err = os.RemoveAll(path.Join(testDevicePath, testDevice))
			Expect(err).Should(BeNil())
			err = os.RemoveAll(deviceSymlinkPath)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCache.serialPrefixToDeviceCacheMap) == 0 {
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

			testDevice := "vda"

			_, err := testutil.NewDeviceSymlink(path.Join(testDevicePath, testDevice), testDeviceSymlinkPath, models.BusVIRTIO)
			Expect(err).Should(BeNil())

			Eventually(func() bool {
				if len(deviceSerialCache.serialPrefixToDeviceCacheMap) == 0 {
					return true
				}
				return false
			}, timeout).Should(BeTrue())

		})
	})
})
