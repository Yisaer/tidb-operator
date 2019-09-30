// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	informers "github.com/pingcap/tidb-operator/pkg/client/informers/externalversions"
	"github.com/pingcap/tidb-operator/pkg/controller"
	"github.com/pingcap/tidb-operator/pkg/controller/backup"
	"github.com/pingcap/tidb-operator/pkg/controller/backupschedule"
	"github.com/pingcap/tidb-operator/pkg/controller/restore"
	"github.com/pingcap/tidb-operator/pkg/controller/tidbcluster"
	"github.com/pingcap/tidb-operator/pkg/version"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/util/logs"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

var (
	printVersion       bool
	workers            int
	autoFailover       bool
	pdFailoverPeriod   time.Duration
	tikvFailoverPeriod time.Duration
	tidbFailoverPeriod time.Duration
	leaseDuration      = 15 * time.Second
	renewDuration      = 5 * time.Second
	retryPeriod        = 3 * time.Second
	waitDuration       = 5 * time.Second
)

func init() {
	// 参数解析
	flag.BoolVar(&printVersion, "V", false, "Show version and quit")
	flag.BoolVar(&printVersion, "version", false, "Show version and quit")
	flag.IntVar(&workers, "workers", 5, "The number of workers that are allowed to sync concurrently. Larger number = more responsive management, but more CPU (and network) load")
	flag.BoolVar(&controller.ClusterScoped, "cluster-scoped", true, "Whether tidb-operator should manage kubernetes cluster wide TiDB Clusters")
	flag.StringVar(&controller.DefaultStorageClassName, "default-storage-class-name", "standard", "Default storage class name")
	flag.StringVar(&controller.DefaultBackupStorageClassName, "default-backup-storage-class-name", "standard", "Default storage class name for backup and restore")
	flag.BoolVar(&autoFailover, "auto-failover", true, "Auto failover")
	flag.DurationVar(&pdFailoverPeriod, "pd-failover-period", time.Duration(5*time.Minute), "PD failover period default(5m)")
	flag.DurationVar(&tikvFailoverPeriod, "tikv-failover-period", time.Duration(5*time.Minute), "TiKV failover period default(5m)")
	flag.DurationVar(&tidbFailoverPeriod, "tidb-failover-period", time.Duration(5*time.Minute), "TiDB failover period")
	flag.DurationVar(&controller.ResyncDuration, "resync-duration", time.Duration(30*time.Second), "Resync time of informer")
	flag.BoolVar(&controller.TestMode, "test-mode", false, "whether tidb-operator run in test mode")
	flag.StringVar(&controller.TidbBackupManagerImage, "tidb-backup-manager-image", "pingcap/tidb-backup-manager:latest", "The image of backup manager tool")

	flag.Parse()
}

func main() {
	if printVersion {
		version.PrintVersionInfo()
		os.Exit(0)
	}
	version.LogVersionInfo()

	logs.InitLogs()
	defer logs.FlushLogs()

	hostName, err := os.Hostname()
	if err != nil {
		glog.Fatalf("failed to get hostname: %v", err)
	}

	ns := os.Getenv("NAMESPACE")
	if ns == "" {
		glog.Fatal("NAMESPACE environment variable not set")
	}

	// 获取api server token
	cfg, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("failed to get config: %v", err)
	}

	cli, err := versioned.NewForConfig(cfg)
	if err != nil {
		glog.Fatalf("failed to create Clientset: %v", err)
	}

	// 构建kube Client
	kubeCli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		glog.Fatalf("failed to get kubernetes Clientset: %v", err)
	}

	// PingCap 资源的Informer组件
	var informerFactory informers.SharedInformerFactory


	// K8S的原生Informer组件资源
	var kubeInformerFactory kubeinformers.SharedInformerFactory

	// Controller是否全集群监听
	if controller.ClusterScoped {
		informerFactory = informers.NewSharedInformerFactory(cli, controller.ResyncDuration)
		kubeInformerFactory = kubeinformers.NewSharedInformerFactory(kubeCli, controller.ResyncDuration)
	} else {
		options := []informers.SharedInformerOption{
			informers.WithNamespace(ns),
		}
		informerFactory = informers.NewSharedInformerFactoryWithOptions(cli, controller.ResyncDuration, options...)

		kubeoptions := []kubeinformers.SharedInformerOption{
			kubeinformers.WithNamespace(ns),
		}
		kubeInformerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(kubeCli, controller.ResyncDuration, kubeoptions...)
	}

	// Controller全局锁，用于保证每次只有一个Leader，进行资源的修改
	rl := resourcelock.EndpointsLock{
		EndpointsMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      "tidb-controller-manager",
		},
		Client: kubeCli.CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      hostName,
			EventRecorder: &record.FakeRecorder{},
		},
	}

	// TC Controller
	tcController := tidbcluster.NewController(kubeCli, cli, informerFactory, kubeInformerFactory, autoFailover, pdFailoverPeriod, tikvFailoverPeriod, tidbFailoverPeriod)


	backupController := backup.NewController(kubeCli, cli, informerFactory, kubeInformerFactory)
	restoreController := restore.NewController(kubeCli, cli, informerFactory, kubeInformerFactory)
	bsController := backupschedule.NewController(kubeCli, cli, informerFactory, kubeInformerFactory)
	controllerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start informer factories after all controller are initialized.
	informerFactory.Start(controllerCtx.Done())
	kubeInformerFactory.Start(controllerCtx.Done())

	// Wait for all started informers' cache were synced.
	// Informer组件本地缓存
	for v, synced := range informerFactory.WaitForCacheSync(wait.NeverStop) {
		if !synced {
			glog.Fatalf("error syncing informer for %v", v)
		}
	}
	for v, synced := range kubeInformerFactory.WaitForCacheSync(wait.NeverStop) {
		if !synced {
			glog.Fatalf("error syncing informer for %v", v)
		}
	}
	glog.Infof("cache of informer factories sync successfully")

	// 将Controller逻辑放到各个goroutine中执行
	onStarted := func(ctx context.Context) {
		go wait.Forever(func() { backupController.Run(workers, ctx.Done()) }, waitDuration)
		go wait.Forever(func() { restoreController.Run(workers, ctx.Done()) }, waitDuration)
		go wait.Forever(func() { bsController.Run(workers, ctx.Done()) }, waitDuration)
		// 这里的Run函数会被ctx.Done()所阻塞而长期运行，所以整个进程周期内Run方法只会被调用一次
		wait.Forever(func() { tcController.Run(workers, ctx.Done()) }, waitDuration)
	}
	onStopped := func() {
		glog.Fatalf("leader election lost")
	}

	// 多controller实例时进行leader竞选
	// leader election for multiple tidb-cloud-manager
	go wait.Forever(func() {
		leaderelection.RunOrDie(controllerCtx, leaderelection.LeaderElectionConfig{
			Lock:          &rl,
			LeaseDuration: leaseDuration,
			RenewDeadline: renewDuration,
			RetryPeriod:   retryPeriod,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: onStarted,
				OnStoppedLeading: onStopped,
			},
		})
	}, waitDuration)

	// 监听端口，主goroutine 阻塞运行
	glog.Fatal(http.ListenAndServe(":6060", nil))
}
