module github.com/smartxworks/elf-csi-driver

go 1.14

require (
	github.com/container-storage-interface/spec v1.3.0
	github.com/evanphx/json-patch v4.11.0+incompatible // indirect
	github.com/go-openapi/runtime v0.24.1
	github.com/golang/protobuf v1.5.2
	github.com/googleapis/gnostic v0.5.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/kubernetes-csi/csi-test/v3 v3.1.1
	github.com/kubernetes-csi/external-snapshotter/client/v4 v4.1.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.19.0
	github.com/openlyinc/pointy v1.1.2
	github.com/prometheus/client_golang v1.11.0 // indirect
	github.com/smartxworks/cloudtower-go-sdk/v2 v2.1.0
	github.com/stretchr/testify v1.7.0
	go.uber.org/zap v1.19.0 // indirect
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/grpc v1.41.0
	google.golang.org/protobuf v1.26.1-0.20210525005349-febffdd88e85 // indirect
	k8s.io/api v0.21.4
	k8s.io/apimachinery v0.21.4
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.21.4
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.8.0
	k8s.io/kubectl v0.17.4 // indirect
	k8s.io/kubernetes v1.21.3
	k8s.io/utils v0.0.0-20210819203725-bdf08cb9a70a
)

replace (
	k8s.io/api => k8s.io/api v0.21.3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.21.3
	k8s.io/apimachinery => k8s.io/apimachinery v0.21.3
	k8s.io/apiserver => k8s.io/apiserver v0.21.3
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.21.3
	k8s.io/client-go => k8s.io/client-go v0.21.3
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.21.3
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.21.3
	k8s.io/code-generator => k8s.io/code-generator v0.21.3
	k8s.io/component-base => k8s.io/component-base v0.21.3
	k8s.io/component-helpers => k8s.io/component-helpers v0.21.3
	k8s.io/controller-manager => k8s.io/controller-manager v0.21.3
	k8s.io/cri-api => k8s.io/cri-api v0.21.3
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.21.3
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.21.3
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.21.3
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.21.3
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.21.3
	k8s.io/kubectl => k8s.io/kubectl v0.21.3
	k8s.io/kubelet => k8s.io/kubelet v0.21.3
	k8s.io/kubernetes v1.21.3 => github.com/iomesh/kubernetes v1.22.0-alpha.0.0.20210811080617-9444b71d048d
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.21.3
	k8s.io/metrics => k8s.io/metrics v0.21.3
	k8s.io/mount-utils => k8s.io/mount-utils v0.21.3
	k8s.io/node-api => k8s.io/node-api v0.21.3
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.21.3
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.21.3
	k8s.io/sample-controller => k8s.io/sample-controller v0.21.3
)
