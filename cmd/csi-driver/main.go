// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package main

import (
	"flag"
	"fmt"
	"gopkg.in/yaml.v2"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	snapshotclientset "github.com/kubernetes-csi/external-snapshotter/client/v4/clientset/versioned"
	"github.com/smartxworks/cloudtower-go-sdk/v2/models"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	httptransport "github.com/go-openapi/runtime/client"
	towerclient "github.com/smartxworks/cloudtower-go-sdk/v2/client"

	"github.com/smartxworks/elf-csi-driver/pkg/driver"
	"github.com/smartxworks/elf-csi-driver/pkg/utils"
)

const (
	version        = "2.0"
	cloudTowerYaml = "/etc/cloudtower-server/cloudtower.yaml"
)

var (
	csiAddr        = flag.String("csi_addr", "", "csi server addr")
	driverName     = flag.String("driver_name", "com.smartx.elf-csi-driver", "driver name")
	role           = flag.String("role", "node", "plugin role: controller / node / all")
	livenessPort   = flag.Int("liveness_port", -1, "node plugin livness port")
	namespace      = flag.String("namespace", "default", "k8s resource namespace used by driver")
	nodeMap        = flag.String("node_map", "node-map", "node configmap name")
	kubeConfigPath = flag.String("kube_config_path", "", "kube config path, eg. $HOME/.kube/config")
	pprofPort      = flag.Int("pprof_port", 0, "")
)

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	flag.Parse()

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
			wait.Until(func() {
				if pproferr := http.ListenAndServe(fmt.Sprintf(":%d", *pprofPort), mux); pproferr != nil {
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

const (
	CloudTowerAuthLocal CloudTowerAuthMode = "LOCAL"
	CloudTowerAuthLDAP  CloudTowerAuthMode = "LDAP"
)

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

	config.TowerClient = towerClient
}

func initNodeConfig(config *driver.DriverConfig) {
	config.LivenessPort = *livenessPort
	config.Mount = utils.NewMount()
	config.Resizer = utils.NewResizer()

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
