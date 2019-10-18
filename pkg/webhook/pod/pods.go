// Copyright 2019 PingCAP, Inc.
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

package pod

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	"github.com/pingcap/tidb-operator/pkg/label"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	versionCli versioned.Interface
	pdControl  pdapi.PDControlInterface
	kubeCli    kubernetes.Interface
)

func init() {

	if pdControl == nil {
		pdControl = pdapi.NewDefaultPDControl()
	}
}

func AdmitPods(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {

	name := ar.Request.Name
	namespace := ar.Request.Namespace
	operation := ar.Request.Operation
	glog.Infof("receive admission to %s pod[%s/%s]", operation, namespace, name)

	cfg, err := rest.InClusterConfig()
	if err != nil {
		glog.Errorf("Create k8s cluster config failed, err: %v,refuse to %s pod[%s,%s]", err, operation, namespace, name)
		return util.ARFail(err)
	}

	if versionCli == nil {
		versionCli, err = versioned.NewForConfig(cfg)
		if err != nil {
			glog.Errorf("Create ClientSet failed, err: %v,refuse to %s pod[%s,%s]", err, operation, namespace, name)
			return util.ARFail(err)
		}
	}

	if kubeCli == nil {
		kubeCli, err = kubernetes.NewForConfig(cfg)
		if err != nil {
			glog.Errorf("Create k8s client failed, err: %v,refuse to %s pod[%s,%s]", err, operation, namespace, name)
			return util.ARFail(err)
		}
	}

	pod, err := kubeCli.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("Failed to get pod [%s/%s],refuse to %s pod", namespace, name, operation)
		return util.ARFail(err)
	}

	switch operation {
	case v1beta1.Delete:
		return AdmitDeletePods(pod)
	default:
		glog.Infof("Admit to %s pod[%s/%s]", operation, namespace, name)
		return util.ARSuccess()
	}
}

func AdmitDeletePods(pod *core.Pod) *v1beta1.AdmissionResponse {

	name := pod.Name
	namespace := pod.Namespace

	l := label.Label(pod.Labels)
	if !(l.IsPD() || l.IsTiKV() || l.IsTiDB()) {
		glog.Infof("pod[%s/%s] is not TiDB component,admit to delete", namespace, name)
		return util.ARSuccess()
	}

	tcName, exist := pod.Labels[label.InstanceLabelKey]
	if !exist {
		glog.Errorf("pod[%s/%s] has no label: %s", namespace, name, label.InstanceLabelKey)
		return util.ARFail(fmt.Errorf("pod[%s/%s] has no label: %s", namespace, name, label.InstanceLabelKey))
	}

	tc, err := versionCli.PingcapV1alpha1().TidbClusters(namespace).Get(tcName, metav1.GetOptions{})

	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("tc[%s/%s] had been deleted,admit to delete pod[%s/%s]", namespace, tcName, namespace, name)
			return util.ARSuccess()
		}
		glog.Errorf("failed get tc[%s/%s],refuse to delete pod[%s/%s]", namespace, tcName, namespace, name)
		return util.ARFail(err)
	}

	if len(pod.OwnerReferences) == 0 {
		return util.ARSuccess()
	}

	ownerStatefulSetName := pod.OwnerReferences[0].Name
	ownerStatefulSet, err := kubeCli.AppsV1().StatefulSets(namespace).Get(ownerStatefulSetName, metav1.GetOptions{})

	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("statefulset[%s/%s] had been deleted,admit to delete pod[%s/%s]", namespace, ownerStatefulSetName, namespace, name)
			return util.ARSuccess()
		}
		glog.Errorf("failed to get statefulset[%s/%s],refuse to delete pod[%s/%s]", namespace, ownerStatefulSetName, namespace, name)
		return util.ARFail(fmt.Errorf("failed to get statefulset[%s/%s],refuse to delete pod[%s/%s]", namespace, ownerStatefulSetName, namespace, name))
	}

	if l.IsPD() {
		return AdmitDeletePdPods(pod, ownerStatefulSet, tc)
	}

	glog.Infof("[%s/%s] is admit to be deleted", namespace, name)
	return util.ARSuccess()
}
