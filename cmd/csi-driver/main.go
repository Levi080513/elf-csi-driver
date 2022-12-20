// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	httptransport "github.com/go-openapi/runtime/client"
	snapshotclientset "github.com/kubernetes-csi/external-snapshotter/client/v4/clientset/versioned"
	towerclient "github.com/smartxworks/cloudtower-go-sdk/v2/client"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/yaml"

	"github.com/smartxworks/elf-csi-driver/pkg/driver"
	"github.com/smartxworks/elf-csi-driver/pkg/feature"
	"github.com/smartxworks/elf-csi-driver/pkg/service"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	version        = "2.0"
	cloudTowerYaml = "/etc/cloudtower-server/cloudtower.yaml"
)

var (
	csiAddr        = flag.String("csi-addr", "", "csi server addr")
	driverName     = flag.String("driver-name", "com.smartx.elf-csi-driver", "driver name")
	role           = flag.String("role", "node", "plugin role: controller / node / all")
	livenessPort   = flag.Int("liveness-port", -1, "node plugin livness port")
	namespace      = flag.String("namespace", "default", "k8s resource namespace used by driver")
	nodeMap        = flag.String("node-map", "node-map", "node configmap name")
	kubeConfigPath = flag.String("kubeconfig", "", "kube config path, eg. $HOME/.kube/config")
	pprofPort      = flag.Int("pprof-port", 0, "")
	clusterID      = flag.String("cluster-id", "", "kubernetes cluster id")
	// The preferred disk bus type. Default to VIRTIO because the performance of VIRTIO Bus is litter better than SCSI Bus.
	preferredVolumeBusType = flag.String("preferred-volume-bus-type", "VIRTIO", "preferred VM bus for volume attach to VM")
)

func main() {
	// init ELF CSI feature gates.
	feature.Init()

	klog.InitFlags(nil)
	defer klog.Flush()

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	feature.MutableGates.AddFlag(pflag.CommandLine)

	pflag.Parse()

	config := &driver.DriverConfig{}
	initCommonConfig(config)

	if config.Role == driver.ALL || config.Role == driver.CONTROLLER {
		initControllerConfig(config)
	}

	if config.Role == driver.ALL || config.Role == driver.NODE {
		initNodeConfig(config)
	}

	drv, err := driver.NewDriver(config)
	if err != nil {
		klog.Fatalf("new driver, %v", err)
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM, syscall.SIGINT)

	stopCh := make(chan struct{})

	if *pprofPort != 0 {
		go func() {
			defer func() {
				err := recover()
				klog.Errorf("start pprof failed: %v", err)
			}()

			mux := http.NewServeMux()
			mux.HandleFunc("/debug/pprof/", pprof.Index)
			mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

			pprofServer := &http.Server{
				Addr:              fmt.Sprintf(":%d", *pprofPort),
				ReadHeaderTimeout: 32 * time.Second,
				Handler:           mux,
			}

			wait.Until(func() {
				if pproferr := pprofServer.ListenAndServe(); pproferr != nil {
					klog.Errorf("listen pprof failed: %v", err)
				}
			}, time.Second, stopCh)
		}()
	}

	go func() {
		<-signalCh
		defer close(signalCh)

		klog.Infof("stopping...")
		// close stopCh to notify all coroutines to terminate
		close(stopCh)
	}()

	// run driver
	err = drv.Run(stopCh)
	if err != nil {
		klog.Fatalf("driver run error, %v", err)
	}
}

type CloudTowerAuthMode string

func (m CloudTowerAuthMode) String() string {
	return string(m)
}

type CloudTowerConnect struct {
	// Server is the URI for CloudTower server API.
	Server string `json:"server"`
	// AuthMode is the authentication mode of CloudTower server.
	AuthMode CloudTowerAuthMode `json:"authMode"`
	// Username is the username for authenticating the CloudTower server.
	Username string `json:"username"`
	// Password is the password for authenticating the CloudTower server.
	Password string `json:"password"`
	// SkipTLSVerify indicates whether to skip verification for the TLS certificate of the CloudTower server.
	SkipTLSVerify bool `json:"skipTLSVerify,omitempty"`
}

func initCommonConfig(config *driver.DriverConfig) {
	config.KubeClient, config.SnapshotClient = getKClient()
	config.DriverName = *driverName
	config.Version = version
	config.Role = *role
	config.NodeMap = driver.NewNodeMap(*nodeMap, config.KubeClient.CoreV1().ConfigMaps(*namespace))
	config.ServerAddr = *csiAddr
	config.ClusterID = *clusterID
	config.PreferredVolumeBusType = *preferredVolumeBusType

	cloudTower := &CloudTowerConnect{}

	raw, err := os.ReadFile(cloudTowerYaml)
	if err != nil {
		klog.Fatalf("failed to get cloudtower yaml")
	}

	if err = yaml.Unmarshal(raw, cloudTower); err != nil {
		klog.Fatalf("failed to parse cloudtower")
	}

	transport := httptransport.New(cloudTower.Server, "/v2/api", []string{"http"})
	transport.DefaultAuthentication = httptransport.APIKeyAuth("Authorization", "header", "token")
	source := models.UserSourceLOCAL

	if cloudTower.AuthMode == "LDAP" {
		source = models.UserSourceLDAP
	}

	towerClient, err := towerclient.NewWithUserConfig(towerclient.ClientConfig{
		Host:     cloudTower.Server,
		BasePath: "v2/api",
		Schemes:  []string{"http"},
	}, towerclient.UserConfig{
		Name:     cloudTower.Username,
		Password: cloudTower.Password,
		Source:   source,
	})

	if err != nil {
		klog.Fatalf("driver config init error, %v", err)
	}

	config.TowerClient = service.NewTowerService(towerClient)
}

func initNodeConfig(config *driver.DriverConfig) {
	config.LivenessPort = *livenessPort
	config.Mount = utils.NewMount()
	config.Resizer = utils.NewResizer()
	config.OsUtil = utils.NewOsUtil()

	nodeID, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		klog.Fatalf("failed to look up NODE_NAME")
	}

	config.NodeID = nodeID
}

func initControllerConfig(config *driver.DriverConfig) {
	// empty now
}

func getKClient() (kubernetes.Interface, snapshotclientset.Interface) {
	var config *rest.Config

	var err error
	if len(*kubeConfigPath) > 0 {
		// OutofCluster
		config, err = clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
	} else {
		// InClusterConfig
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		klog.Fatalf("failed to config k8s client, %v", err)
	}

	return kubernetes.NewForConfigOrDie(config), snapshotclientset.NewForConfigOrDie(config)
}
