// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/multierr"
	"gopkg.in/yaml.v2"
	"k8s.io/klog"
	"k8s.io/utils/keymutex"

	"github.com/iomesh/zbs-client-go/zbs"
)

const (
	retryLimit      = 15
	waitLunInterval = 1 * time.Second
	cmdTimeout      = 5 * time.Second
	defaultIface    = "default"

	ChrootPath = "/host"
)

const (
	iscsiSessionDir    = "/sys/class/iscsi_session"
	iscsiConnectionDir = "/sys/class/iscsi_connection"
)

const (
	// exit code detail https://linux.die.net/man/8/iscsiadm
	iscsiErrIdbm             = 6
	iscsiSessionExistsError  = 15
	iscsiErrNoObjsFoundError = 21
)

func isISCSIError(err error, code int) bool {
	if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == code {
		return true
	}

	return false
}

type sessionNotFoundError string

func (e sessionNotFoundError) Error() string {
	return string(e)
}

type ISCSILun struct {
	LunId     uint32
	Initiator string
	IFace     string
	Portal    string
	TargetIqn string
	ChapInfo  *zbs.InitiatorChapInfo
}

type ISCSI interface {
	Attach(lun *ISCSILun) (string, error)
	Detach(lun *ISCSILun) error
	GetLunDevice(lun *ISCSILun) (string, error)
	ListDevices() ([]string, error)
}

const (
	nodeSessionCmdsMax    = "node.session.cmds_max"
	nodeSessionQueueDepth = "node.session.queue_depth"
)

type ISCSIConfig struct {
	// 2,4,8,16...2048
	NodeSessionCmdsMax *int `yaml:"node.session.cmds_max,omitempty"`
	//  The value is in seconds and the default is 120 seconds.
	// Special values:
	// If the value is 0, IO will be failed immediately.
	// If the value is less than 0, IO will remain queued until the session
	// is logged back in, or until the user runs the logout command.
	NodeSessionTimeoutReplacementTimeout *int `yaml:"node.session.timeo.replacement_timeout,omitempty"`
	// 1-1024
	NodeSessionQueueDepth *int `yaml:"node.session.queue_depth,omitempty"`
}

func (config *ISCSIConfig) Validate() error {
	if config.NodeSessionCmdsMax != nil {
		if *config.NodeSessionCmdsMax < 1 || *config.NodeSessionCmdsMax > 2048 {
			return fmt.Errorf("%v %v is out of range [1, 2048]", nodeSessionCmdsMax, *config.NodeSessionCmdsMax)
		}

		if *config.NodeSessionCmdsMax&(*config.NodeSessionCmdsMax-1) != 0 {
			return fmt.Errorf("%v %v is not a power of 2", nodeSessionCmdsMax, *config.NodeSessionCmdsMax)
		}
	}

	if config.NodeSessionQueueDepth != nil {
		if *config.NodeSessionQueueDepth < 1 || *config.NodeSessionQueueDepth > 1024 {
			return fmt.Errorf("%v %v is out of range [1, 2048]", nodeSessionQueueDepth, *config.NodeSessionQueueDepth)
		}
	}

	return nil
}

func (config *ISCSIConfig) WithDefault() *ISCSIConfig {
	if config.NodeSessionCmdsMax == nil {
		config.NodeSessionCmdsMax = &[]int{128}[0]
	}

	if config.NodeSessionQueueDepth == nil {
		config.NodeSessionQueueDepth = &[]int{128}[0]
	}

	if config.NodeSessionTimeoutReplacementTimeout == nil {
		config.NodeSessionTimeoutReplacementTimeout = &[]int{120}[0]
	}

	return config
}

func (config *ISCSIConfig) ToMap() map[string]string {
	out, err := yaml.Marshal(config)
	if err != nil {
		klog.Fatalf("failed to marshal iscsi config %+v, %v", config, err)
	}

	configMap := make(map[string]string)

	err = yaml.Unmarshal(out, configMap)
	if err != nil {
		klog.Fatalf("failed to unmarshal iscsi config %+v, %v", config, err)
	}

	return configMap
}

type iscsi struct {
	kmutex keymutex.KeyMutex
	config *ISCSIConfig
}

func NewISCSI(config *ISCSIConfig) (ISCSI, error) {
	if config == nil {
		config = &ISCSIConfig{}
	}

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	return &iscsi{kmutex: keymutex.NewHashed(0), config: config.WithDefault()}, nil
}

func (iscsi *iscsi) createIface(iface string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	createIfaceCmd := []string{
		"iscsiadm",
		"-m", "iface",
		"-I", iface,
		"-o", "new",
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, createIfaceCmd...)
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if (ok && exit.ExitCode() != 15) || !ok {
			return fmt.Errorf("iscsi: failed to create new iface: %s (%v)", string(output), err)
		}
	}

	return nil
}

func (iscsi *iscsi) testIfaceExists(iface string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	testIfaceExists := []string{
		"iscsiadm",
		"-m", "iface",
		"-I", iface,
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, testIfaceExists...)
	if err != nil {
		if isISCSIError(err, iscsiErrIdbm) {
			return false, nil
		}

		return false, fmt.Errorf("failed to test iface exists, cmd %v, output %v, %v",
			testIfaceExists, string(output), err)
	}

	return true, nil
}

func (iscsi *iscsi) updateIface(iface string, name string, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	updateIfaceCmd := []string{
		"iscsiadm",
		"-m", "iface",
		"-I", iface,
		"-o", "update",
		"-n", name,
		"-v", value,
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, updateIfaceCmd...)
	if err != nil {
		return fmt.Errorf("failed to update iface %v, name %v, value %v, %v, %v",
			iface, name, value, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) discoveryTarget(iface string, portal string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	discoveryTargetCmd := []string{
		"iscsiadm",
		"-m", "discovery",
		"-t", "sendtargets",
		"-p", portal,
		"-I", iface,
		"-o", "update",
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, discoveryTargetCmd...)
	if err != nil {
		return fmt.Errorf("failed to iscsiadm  discovery target, command: %v, output: %v, error: %v",
			discoveryTargetCmd, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) updateNodeConfig(iface string, portal string, target string) error {
	for key, value := range iscsi.config.ToMap() {
		err := iscsi.updateNode(iface, portal, target, key, value)
		if err != nil {
			return err
		}
	}

	return nil
}

func (iscsi *iscsi) updateNodeChapConfig(iface string, portal string, target string,
	initiatorChap *zbs.InitiatorChapInfo) error {
	chapConfig := map[string]string{
		"node.session.auth.authmethod": "CHAP",
		"node.session.auth.username":   initiatorChap.ChapName,
		"node.session.auth.password":   initiatorChap.Secret,
	}

	var errs error

	for k, v := range chapConfig {
		if err := iscsi.updateNode(iface, portal, target, k, v); err != nil {
			errs = multierr.Append(errs, err)
		}
	}

	return errs
}

func (iscsi *iscsi) Attach(lun *ISCSILun) (string, error) {
	iscsi.kmutex.LockKey(lun.TargetIqn)

	defer func() {
		err := iscsi.kmutex.UnlockKey(lun.TargetIqn)
		if err != nil {
			klog.Warningf("failed to unlock key %v, %v", lun.TargetIqn, err)
		}
	}()

	iface := defaultIface
	if lun.IFace != "" {
		iface = lun.IFace
	}

	if iface != defaultIface {
		err := iscsi.createIface(lun.IFace)
		if err != nil {
			return "", err
		}

		err = iscsi.updateIface(lun.IFace, "iface.initiatorname", lun.Initiator)
		if err != nil {
			return "", err
		}
	}

	err := iscsi.discoveryTarget(iface, lun.Portal)
	if err != nil {
		return "", err
	}

	if iface != defaultIface {
		// Prevent the node from restarting and automatically connect to the target
		err = iscsi.updateNode(iface, lun.Portal, "", "node.startup", "manual")
		if err != nil {
			return "", err
		}

		// Prevent the node automatically scan lun
		err = iscsi.updateNode(iface, lun.Portal, "", "node.session.scan", "manual")
		if err != nil {
			return "", err
		}

		err = iscsi.updateNodeConfig(iface, lun.Portal, lun.TargetIqn)
		if err != nil {
			return "", err
		}

		if lun.ChapInfo != nil {
			err = iscsi.updateNodeChapConfig(iface, lun.Portal, lun.TargetIqn, lun.ChapInfo)
			if err != nil {
				return "", err
			}
		}
	}

	err = iscsi.login(iface, lun.Portal, lun.TargetIqn)
	if err != nil {
		return "", err
	}

	// Because the iscsi target configuration update has a delay,
	// it is possible that iscsi lun is not mounted on the host, so multiple scans are required.
	device := ""
	err = Retry(func() error {
		err = iscsi.scanLunDevice(iface, lun.Portal, lun.TargetIqn, lun.LunId)
		if err != nil {
			return err
		}

		device, err = iscsi.getLunDevice(iface, lun.Portal, lun.TargetIqn, lun.LunId)
		if err != nil {
			return err
		}

		if device != "" {
			return nil
		}

		return fmt.Errorf("wait iscsi iface %v target %v lun %v", iface, lun.TargetIqn, lun.LunId)
	}, retryLimit, waitLunInterval)

	return device, err
}

func (iscsi *iscsi) login(iface string, portal string, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	loginCmd := []string{
		"iscsiadm",
		"-m", "node",
		"-I", iface,
		"-p", portal,
		"-T", target,
		"--login",
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, loginCmd...)
	// Ignore ISCSI_ERR_SESS_EXISTS error to keep idempotence.
	// Repeat login success on Centos will always return exit(0) but
	// Debian will return exit(15)
	if err != nil && !isISCSIError(err, iscsiSessionExistsError) {
		return fmt.Errorf("failed to iscsiadm login, command: %v, "+
			"output: %v, error: %v", loginCmd, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) updateNode(iface string, portal string, target string, name string, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	updateNodeCmd := []string{
		"iscsiadm",
		"-m", "node",
		"-I", iface,
		"-p", portal,
	}

	if target != "" {
		updateNodeCmd = append(updateNodeCmd, "-T", target)
	}

	updateNodeCmd = append(updateNodeCmd, "-o", "update", "-n", name, "-v", value)

	output, err := ExecCommandChroot(ctx, ChrootPath, updateNodeCmd...)
	if err != nil {
		return fmt.Errorf("failed to iscsiadm update node, command: %v, output: %v, error: %v",
			updateNodeCmd, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) getSessionHostNumberById(sessionId int) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	showSessionCmd := []string{
		"iscsiadm",
		"-m", "session",
		"-P", "3",
		"-r", strconv.Itoa(sessionId),
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, showSessionCmd...)
	if err != nil {
		return 0, fmt.Errorf("failed to iscsiadm show session info, command: %v, output: %v, error: %v",
			showSessionCmd, string(output), err)
	}
	// get host number
	hostNumberRe := regexp.MustCompile("Host\\s*Number\\s*:\\s*[0-9]+")
	hostNumberString := string(hostNumberRe.Find(output))
	hostNumberString = strings.Trim(hostNumberString, " ")

	var hostNumber int

	n, err := fmt.Sscanf(hostNumberString, "Host Number:  %d", &hostNumber)
	if n != 1 || err != nil {
		return 0, fmt.Errorf("failed to iscsiadm get host number, command: %v, output: %v, error: %v",
			showSessionCmd, string(output), err)
	}

	return hostNumber, nil
}

func getPortalAddress(portal string) (string, error) {
	if portal == "" {
		return "", fmt.Errorf("portal is empty")
	}

	portalPair := strings.Split(portal, ":")
	if len(portalPair) == 2 {
		return portal, nil
	}

	return fmt.Sprintf("%v:%v", portal, "3260"), nil
}

func getConnectionPortals(session string) ([]string, error) {
	devices, err := ioutil.ReadDir(path.Join(iscsiSessionDir, session, "device"))
	if err != nil {
		return nil, fmt.Errorf("failed to read connections, %v", err)
	}

	connections := make([]string, 0)

	for _, device := range devices {
		if strings.HasPrefix(device.Name(), "connection") {
			connections = append(connections, device.Name())
		}
	}

	portals := make([]string, 0)

	for _, connection := range connections {
		address, err := ReadFileString(path.Join(iscsiConnectionDir, connection, "persistent_address"))
		if err != nil {
			return nil, err
		}

		port, err := ReadFileString(path.Join(iscsiConnectionDir, connection, "persistent_port"))
		if err != nil {
			return nil, err
		}

		portals = append(portals, fmt.Sprintf("%v:%v", strings.TrimSpace(address), strings.TrimSpace(port)))
	}

	return portals, nil
}

func (iscsi *iscsi) getSessionId(iface string, portal string, target string) (int, error) {
	fileInfos, err := ioutil.ReadDir(iscsiSessionDir)
	if err != nil {
		return -1, fmt.Errorf("failed to open %v, %v", iscsiSessionDir, err)
	}

	portalAddress, err := getPortalAddress(portal)
	if err != nil {
		return -1, err
	}

	readAndEqual := func(filename string, expectValue string) bool {
		value, err := ReadFileString(filename)
		if err != nil {
			klog.Warningf("failed to read %v, %v", filename, err)
		}

		return strings.TrimSpace(value) == expectValue
	}

	for _, fileInfo := range fileInfos {
		targetFile := path.Join(iscsiSessionDir, fileInfo.Name(), "targetname")
		if !readAndEqual(targetFile, target) {
			continue
		}

		ifaceFile := path.Join(iscsiSessionDir, fileInfo.Name(), "ifacename")
		if !readAndEqual(ifaceFile, iface) {
			continue
		}

		portalAddrs, err := getConnectionPortals(fileInfo.Name())
		if err != nil {
			return -1, err
		}

		comparePortalAddrs := func() bool {
			for _, addr := range portalAddrs {
				if addr == portalAddress {
					return true
				}
			}

			return false
		}

		if !comparePortalAddrs() {
			continue
		}

		var sessionId int

		n, err := fmt.Sscanf(path.Base(fileInfo.Name()), "session%d", &sessionId)
		if n != 1 || err != nil {
			return -1, fmt.Errorf("failed to get session id, wrong path %v, %v", fileInfo.Name(), err)
		}

		return sessionId, nil
	}

	return -1, sessionNotFoundError(fmt.Sprintf("failed to find <%v, %v> session", iface, target))
}

func (iscsi *iscsi) getHostNumber(iface string, portal string, target string) (int, error) {
	sessionId, err := iscsi.getSessionId(iface, portal, target)
	if err != nil {
		return -1, err
	}

	hostNumber, err := iscsi.getSessionHostNumberById(sessionId)
	if err != nil {
		return -1, err
	}

	return hostNumber, nil
}

func (iscsi *iscsi) GetLunDevice(lun *ISCSILun) (string, error) {
	iscsi.kmutex.LockKey(lun.TargetIqn)

	defer func() {
		err := iscsi.kmutex.UnlockKey(lun.TargetIqn)
		if err != nil {
			klog.Warningf("failed to unlock key %v, %v", lun.TargetIqn, err)
		}
	}()

	iface := defaultIface
	if lun.IFace != "" {
		iface = lun.IFace
	}

	return iscsi.getLunDevice(iface, lun.Portal, lun.TargetIqn, lun.LunId)
}

func (iscsi *iscsi) getLunDevice(iface string, portal string, target string, lunId uint32) (string, error) {
	hostNumber, err := iscsi.getHostNumber(iface, portal, target)
	if err != nil {
		// session not found
		if _, ok := err.(sessionNotFoundError); ok {
			klog.Warningf("session not found, %v", err)
			return "", nil
		}

		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	// example [6:0:0:lunId]   disk target,t,0x1  devicePath Id
	findLunDeviceCmd := fmt.Sprintf("lsscsi -it | awk '/\\[%v:[0-9]+:[0-9]+:%v\\]/ { print $4 }'",
		hostNumber, lunId)

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", findLunDeviceCmd).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to find iface %v target %v lun %v, cmd: %v, output: %v, error: %v",
			iface, target, lunId, findLunDeviceCmd, string(output), err)
	}

	device := strings.TrimSpace(string(output))
	// An empty device means that the device has not been attached yet
	if device == "" {
		return "", nil
	}

	_, err = os.Stat(device)
	if err != nil {
		return "", err
	}

	if device == "-" {
		return "", fmt.Errorf("device letter is -")
	}

	return device, nil
}

func (iscsi *iscsi) scanLunDevice(iface string, portal string, target string, lunId uint32) error {
	hostNumber, err := iscsi.getHostNumber(iface, portal, target)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	scanPath := fmt.Sprintf("/sys/class/scsi_host/host%d/scan", hostNumber)
	scanCmd := fmt.Sprintf("echo \"0 0 %d\" > %v", lunId, scanPath)

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", scanCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to scan target %v lun %v, cmd: %v, output: %v, error: %v",
			target, lunId, scanCmd, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) deleteNode(iface string, portal string, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	deleteNodeCmd := []string{
		"iscsiadm",
		"-m", "node",
		"-I", iface,
		"-p", portal,
		"-T", target,
		"-o", "delete",
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, deleteNodeCmd...)
	if err != nil && !isISCSIError(err, iscsiErrNoObjsFoundError) {
		return fmt.Errorf("failed to delete target %v, cmd: %v, output: %v, error: %v",
			target, deleteNodeCmd, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) deleteIface(iface string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	deleteIfaceCmd := []string{
		"iscsiadm",
		"-m", "iface",
		"-I", iface,
		"-o", "delete",
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, deleteIfaceCmd...)
	if err != nil {
		if isISCSIError(err, iscsiSessionExistsError) {
			return err
		}

		return fmt.Errorf("failed to delete iface, cmd: %v, output: %v, error: %v",
			deleteIfaceCmd, string(output), err)
	}

	return nil
}

func (iscsi *iscsi) Detach(lun *ISCSILun) error {
	iface := defaultIface
	if lun.IFace != "" {
		iface = lun.IFace
	}

	exists, err := iscsi.testIfaceExists(iface)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	cleanup := func() error {
		var devices []string

		devices, err = iscsi.listTargetDevices(iface, lun.Portal, lun.TargetIqn)
		if err != nil {
			return err
		}

		if len(devices) > 0 {
			return nil
		}

		err = iscsi.logout(iface, lun.Portal, lun.TargetIqn)
		if err != nil {
			return err
		}

		err = iscsi.deleteNode(iface, lun.Portal, lun.TargetIqn)
		if err != nil {
			return err
		}

		if iface != defaultIface {
			err = iscsi.deleteIface(iface)
			// It is possible that one iface corresponds to multiple sessions,
			// this time there is no need to delete iface
			if isISCSIError(err, iscsiSessionExistsError) {
				klog.Warningf("failed to delete iface %v, %v", iface, err)
			}
		}

		return nil
	}

	iscsi.kmutex.LockKey(lun.TargetIqn)

	defer func() {
		err = iscsi.kmutex.UnlockKey(lun.TargetIqn)
		if err != nil {
			klog.Warningf("failed to unlock key %v, %v", lun.TargetIqn, err)
		}
	}()

	err = iscsi.deleteLunDevice(iface, lun.Portal, lun.TargetIqn, lun.LunId)
	if err != nil {
		return err
	}

	return cleanup()
}

func (iscsi *iscsi) logout(iface string, portal, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	logoutCmd := []string{
		"iscsiadm",
		"-m", "node",
		"-I", iface,
		"-p", portal,
		"-T", target,
		"--logout",
	}

	output, err := ExecCommandChroot(ctx, ChrootPath, logoutCmd...)
	if err != nil && !isISCSIError(err, iscsiErrNoObjsFoundError) {
		return fmt.Errorf("failed to logout, cmd: %v, output: %v, error: %v",
			logoutCmd, string(output), err)
	}

	return nil
}

func parseDevices(devicesString string) []string {
	devices := make([]string, 0)

	for _, device := range strings.Split(devicesString, "\n") {
		device = strings.TrimSpace(device)
		if device == "" {
			continue
		}

		devices = append(devices, device)
	}

	return devices
}

func (iscsi *iscsi) ListDevices() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	lsscsiCmd := "lsscsi -it | awk '{ print $4 }'"

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", lsscsiCmd).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to lsscsi, %v", err)
	}

	return parseDevices(string(output)), nil
}

func (iscsi *iscsi) listTargetDevices(iface string, portal, target string) ([]string, error) {
	hostNumber, err := iscsi.getHostNumber(iface, portal, target)
	if err != nil {
		// session not found
		if _, ok := err.(sessionNotFoundError); ok {
			klog.Warningf("session not found, %v", err)
			return nil, nil
		}

		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	lsscsiCmd := fmt.Sprintf("lsscsi -it | awk '/\\[%v:[0-9]+:[0-9]+:[0-9]+\\]/ { print $4 }'", hostNumber)

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", lsscsiCmd).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to lsscsi, %v", err)
	}

	return parseDevices(string(output)), nil
}

func (iscsi *iscsi) ListTargetDevices(iface string, portal string, target string) ([]string, error) {
	iscsi.kmutex.LockKey(target)

	defer func() {
		err := iscsi.kmutex.UnlockKey(target)
		if err != nil {
			klog.Warningf("failed to unlock key %v, %v", target, err)
		}
	}()

	return iscsi.listTargetDevices(iface, portal, target)
}

func (iscsi *iscsi) deleteLunDevice(iface string, portal string, target string, lunId uint32) error {
	device, err := iscsi.getLunDevice(iface, portal, target, lunId)
	if err != nil {
		return err
	}

	if len(device) == 0 {
		return nil
	}

	deviceName := filepath.Base(device)

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	deleteDeviceCmd := fmt.Sprintf("echo 1 > /sys/block/%v/device/delete", deviceName)

	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", deleteDeviceCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete device, cmd %v, output %v",
			deleteDeviceCmd, string(output))
	}

	return nil
}

type fakeISCSI struct {
	mutex     sync.Mutex
	deviceMap map[string]string
}

func NewFakeISCSI() ISCSI {
	return &fakeISCSI{
		deviceMap: make(map[string]string),
	}
}

func (f *fakeISCSI) GetLunDevice(lun *ISCSILun) (string, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	device := fmt.Sprintf("%v/%v/%v", lun.IFace, lun.Portal, lun.LunId)
	if _, ok := f.deviceMap[device]; !ok {
		return "", nil
	}

	return device, nil
}

func (f *fakeISCSI) Detach(lun *ISCSILun) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	device := fmt.Sprintf("%v/%v/%v", lun.IFace, lun.Portal, lun.LunId)
	delete(f.deviceMap, device)

	return nil
}

func (f *fakeISCSI) Attach(lun *ISCSILun) (string, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	device := fmt.Sprintf("%v/%v/%v", lun.IFace, lun.Portal, lun.LunId)
	f.deviceMap[device] = device

	return device, nil
}

func (f *fakeISCSI) ListDevices() ([]string, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	devices := make([]string, 0)
	for _, device := range f.deviceMap {
		devices = append(devices, device)
	}

	return devices, nil
}

type ISCSIDaemonConfig interface {
	Get(key string) (string, error)
}

type iscsiDaemonConfig struct {
	configPath string
}

func NewISCSIDaemonConfig(configPath string) ISCSIDaemonConfig {
	return &iscsiDaemonConfig{
		configPath: configPath,
	}
}

func findKeyValue(lines []string, key string) (int, []string) {
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		pairs := strings.FieldsFunc(line, func(c rune) bool {
			return c == '='
		})

		if len(pairs) != 2 {
			continue
		}

		pairs[0] = strings.TrimSpace(pairs[0])
		pairs[1] = strings.TrimSpace(pairs[1])

		if pairs[0] == key {
			return i, pairs
		}
	}

	return -1, nil
}

func (config *iscsiDaemonConfig) Get(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("key is empty")
	}

	byteContent, err := ioutil.ReadFile(config.configPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(byteContent), "\n")

	keyIndex, pairs := findKeyValue(lines, key)
	if keyIndex == -1 {
		return "", nil
	}

	return pairs[1], nil
}

type fakeISCSIDaemonConfig struct {
	mutex sync.Mutex
	conf  map[string]string
}

func NewFakeISCSIDaemonConfig(conf map[string]string) ISCSIDaemonConfig {
	return &fakeISCSIDaemonConfig{
		conf: conf,
	}
}

func (config *fakeISCSIDaemonConfig) Get(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("key is empty")
	}

	config.mutex.Lock()
	defer config.mutex.Unlock()

	value, ok := config.conf[key]
	if !ok {
		return "", nil
	}

	return value, nil
}
