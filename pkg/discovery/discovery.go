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

package discovery

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/apis/pingcap.com/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TiDBDiscovery helps new PD member to discover all other members in cluster bootstrap phase.
type TiDBDiscovery interface {
	Discover(string) (string, error)
}

type tidbDiscovery struct {
	cli       versioned.Interface
	lock      sync.Mutex
	clusters  map[string]*clusterInfo
	tcGetFn   func(ns, tcName string) (*v1alpha1.TidbCluster, error)
	pdControl pdapi.PDControlInterface
}

type clusterInfo struct {
	resourceVersion string
	peers           map[string]struct{}
}

// NewTiDBDiscovery returns a TiDBDiscovery
func NewTiDBDiscovery(cli versioned.Interface) TiDBDiscovery {
	td := &tidbDiscovery{
		cli:       cli,
		pdControl: pdapi.NewDefaultPDControl(),
		clusters:  map[string]*clusterInfo{},
	}
	td.tcGetFn = td.realTCGetFn
	return td
}

func (td *tidbDiscovery) Discover(advertisePeerUrl string) (string, error) {

	// 加锁，每次只能有一个请求同时访问Discover方法
	td.lock.Lock()
	// 释放锁
	defer td.lock.Unlock()

	// 防御性检查
	if advertisePeerUrl == "" {
		return "", fmt.Errorf("advertisePeerUrl is empty")
	}
	glog.Infof("advertisePeerUrl is: %s", advertisePeerUrl)
	strArr := strings.Split(advertisePeerUrl, ".")
	if len(strArr) != 4 {
		return "", fmt.Errorf("advertisePeerUrl format is wrong: %s", advertisePeerUrl)
	}

	// 获取信息
	podName, peerServiceName, ns := strArr[0], strArr[1], strArr[2]
	tcName := strings.TrimSuffix(peerServiceName, "-pd-peer")
	podNamespace := os.Getenv("MY_POD_NAMESPACE")
	if ns != podNamespace {
		return "", fmt.Errorf("the peer's namespace: %s is not equal to discovery namespace: %s", ns, podNamespace)
	}
	// 获取TidbCluster Spec
	tc, err := td.tcGetFn(ns, tcName)
	if err != nil {
		log.Println("td.tcGetFn Error " + podName)
		return "", err
	}
	keyName := fmt.Sprintf("%s/%s", ns, tcName)
	// 获取TidbCluster定义的 pd replicas
	// TODO: the replicas should be the total replicas of pd sets.
	replicas := tc.Spec.PD.Replicas
	// 进程内维护currentCluster
	currentCluster := td.clusters[keyName]
	if currentCluster == nil || currentCluster.resourceVersion != tc.ResourceVersion {
		td.clusters[keyName] = &clusterInfo{
			resourceVersion: tc.ResourceVersion,
			peers:           map[string]struct{}{},
		}
	}
	currentCluster = td.clusters[keyName]
	currentCluster.peers[podName] = struct{}{}

	log.Println("peerUrl = " + advertisePeerUrl + ",podName = " + podName + " ,peerServiceName = " + peerServiceName + ", currentClusterPeers size = " + strconv.Itoa(len(currentCluster.peers)) + ", replicas = " + strconv.FormatInt(int64(replicas), 10))
	strpeerLen := strconv.Itoa(len(currentCluster.peers))
	strReplicas := strconv.FormatInt(int64(replicas), 10)
	if len(currentCluster.peers) == int(replicas) {
		delete(currentCluster.peers, podName)
		log.Println("initial-cluster for " + podName + " peers = " + strpeerLen + ",replicas = " + strReplicas)
		return fmt.Sprintf("--initial-cluster=%s=%s://%s", podName, tc.Scheme(), advertisePeerUrl), nil
	}

	pdClient := td.pdControl.GetPDClient(pdapi.Namespace(tc.GetNamespace()), tc.GetName(), tc.Spec.EnableTLSCluster)
	membersInfo, err := pdClient.GetMembers()
	if err != nil {
		log.Println("pdClient error " + podName + ",error = " + err.Error())
		return "", err
	}

	membersArr := make([]string, 0)
	for id, member := range membersInfo.Members {
		memberURL := strings.ReplaceAll(member.PeerUrls[0], ":2380", ":2379")
		membersArr = append(membersArr, memberURL)
		log.Println("id = " + strconv.Itoa(id) + ",memberURL = " + memberURL)
	}
	delete(currentCluster.peers, podName)
	return fmt.Sprintf("--join=%s", strings.Join(membersArr, ",")), nil
}

func (td *tidbDiscovery) realTCGetFn(ns, tcName string) (*v1alpha1.TidbCluster, error) {
	return td.cli.PingcapV1alpha1().TidbClusters(ns).Get(tcName, metav1.GetOptions{})
}
