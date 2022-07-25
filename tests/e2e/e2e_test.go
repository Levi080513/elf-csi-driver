// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package e2e

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	"github.com/onsi/ginkgo/reporters"
	"github.com/onsi/gomega"
	"k8s.io/component-base/logs"
	"k8s.io/klog"
	"k8s.io/kubernetes/test/e2e/framework"
	frameworkconfig "k8s.io/kubernetes/test/e2e/framework/config"
	e2ereporters "k8s.io/kubernetes/test/e2e/reporters"

	_ "github.com/iomesh/csi-driver/tests/e2e/suites"
)

const kubeconfigEnvVar = "KUBECONFIG"

func TestMain(m *testing.M) {
	rand.Seed(time.Now().UTC().UnixNano())
	testing.Init()
	// k8s.io/kubernetes/test/e2e/framework requires env KUBECONFIG to be set
	// it does not fall back to defaults
	if os.Getenv(kubeconfigEnvVar) == "" {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		os.Setenv(kubeconfigEnvVar, kubeconfig)
	}

	frameworkconfig.CopyFlags(frameworkconfig.Flags, flag.CommandLine)
	framework.RegisterCommonFlags(flag.CommandLine)
	framework.RegisterClusterFlags(flag.CommandLine)
	flag.Parse()

	framework.AfterReadingAllFlags(&framework.TestContext)

	os.Exit(m.Run())
}

func RunE2ETests(t *testing.T) {
	logs.InitLogs()
	defer logs.FlushLogs()

	gomega.RegisterFailHandler(framework.Fail)
	// Disable skipped tests unless they are explicitly requested.
	if len(config.GinkgoConfig.FocusStrings) == 0 && len(config.GinkgoConfig.SkipStrings) == 0 {
		config.GinkgoConfig.SkipStrings = append(config.GinkgoConfig.SkipStrings, `\[Flaky\]|\[Feature:.+\]`)
	}

	// Run tests through the Ginkgo runner with output to console + JUnit for Jenkins
	var r []ginkgo.Reporter

	if framework.TestContext.ReportDir != "" {
		// TODO: we should probably only be trying to create this directory once
		// rather than once-per-Ginkgo-node.
		if err := os.MkdirAll(framework.TestContext.ReportDir, 0755); err != nil {
			klog.Errorf("Failed creating report directory: %v", err)
		} else {
			r = append(r, reporters.NewJUnitReporter(
				path.Join(framework.TestContext.ReportDir,
					fmt.Sprintf("junit_%v%02d.xml", framework.TestContext.ReportPrefix,
						config.GinkgoConfig.ParallelNode))))
		}
	}

	// Stream the progress to stdout and optionally a URL accepting progress updates.
	r = append(r, e2ereporters.NewProgressReporter(framework.TestContext.ProgressReportURL))

	// The DetailsRepoerter will output details about every test (name, files, lines, etc) which helps
	// when documenting our tests.
	if len(framework.TestContext.SpecSummaryOutput) > 0 {
		r = append(r, e2ereporters.NewDetailsReporterFile(framework.TestContext.SpecSummaryOutput))
	}

	klog.Infof("Starting e2e run %q on Ginkgo node %d", framework.RunID, config.GinkgoConfig.ParallelNode)
	ginkgo.RunSpecsWithDefaultAndCustomReporters(t, "csi-driver e2e suite", r)
}

func TestE2E(t *testing.T) {
	RunE2ETests(t)
}
