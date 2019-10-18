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
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/apis/pingcap.com/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/controller"
	"github.com/pingcap/tidb-operator/pkg/label"
	pdutil "github.com/pingcap/tidb-operator/pkg/manager/member"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	operatorUtils "github.com/pingcap/tidb-operator/pkg/util"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

func AdmitDeletePdPods(pod *corev1.Pod, ownerStatefulSet *apps.StatefulSet, tc *v1alpha1.TidbCluster) *v1beta1.AdmissionResponse {

	name := pod.Name
	namespace := pod.Namespace
	tcName := tc.Name
	ordinal, err := operatorUtils.GetOrdinalFromPodName(pod.Name)
	if err != nil {
		return util.ARFail(err)
	}

	pdClient := pdControl.GetPDClient(pdapi.Namespace(tc.GetNamespace()), tc.GetName(), tc.Spec.EnableTLSCluster)

	isMember, err := IsPodInPdMembers(tc, pod, pdClient)
	if err != nil {
		return util.ARFail(err)
	}
	if !isMember {
		glog.Infof("pd pod[%s/%s] is not member of tc[%s/%s],admit to delete", namespace, name, namespace, tcName)
		return util.ARSuccess()
	}

	isOutOfOrdinal, err := operatorUtils.IsPodOutOfOrdinalOrdinal(pod, *ownerStatefulSet.Spec.Replicas)
	if err != nil {
		return util.ARFail(err)
	}

	if isOutOfOrdinal {
		pvcName := operatorUtils.OrdinalPVCName(v1alpha1.PDMemberType, controller.PDMemberName(tcName), ordinal)
		pvc, err := kubeCli.CoreV1().PersistentVolumeClaims(namespace).Get(pvcName, metav1.GetOptions{})
		if err != nil {
			return util.ARFail(err)
		}
		if pvc.Annotations == nil {
			pvc.Annotations = map[string]string{}
		}
		_, existed := pvc.Annotations[label.AnnPVCDeferDeleting]
		if !existed {
			now := time.Now().Format(time.RFC3339)
			pvc.Annotations[label.AnnPVCDeferDeleting] = now
			_, err = kubeCli.CoreV1().PersistentVolumeClaims(namespace).Update(pvc)
			if err != nil {
				return util.ARFail(err)
			}
		}
		err = pdClient.DeleteMember(name)
		if err != nil {
			return util.ARFail(err)
		}
		return &v1beta1.AdmissionResponse{
			Allowed: false,
		}
	}

	leader, err := pdClient.GetPDLeader()
	if err != nil {
		glog.Errorf("tc[%s/%s] fail to get pd leader %v,refuse to delete pod[%s/%s]", namespace, tc.Name, err, namespace, name)
		return util.ARFail(err)
	}

	glog.Infof("tc[%s/%s]'s pd leader is pod[%s/%s] during deleting pod[%s/%s]", namespace, tc.Name, namespace, leader.Name, namespace, name)
	if leader.Name == name {
		lastOrdinal := tc.Status.PD.StatefulSet.Replicas - 1
		var targetName string
		if ordinal == lastOrdinal {
			targetName = pdutil.PdPodName(tc.Name, 0)
		} else {
			targetName = pdutil.PdPodName(tc.Name, lastOrdinal)
		}
		err = pdClient.TransferPDLeader(targetName)
		if err != nil {
			glog.Errorf("tc[%s/%s] failed to transfer pd leader to pod[%s/%s],%v", namespace, tc.Name, namespace, name, err)
			return util.ARFail(err)
		}
		glog.Infof("tc[%s/%s] start to transfer pd leader to pod[%s/%s],refuse to delete pod[%s/%s]", namespace, tc.Name, namespace, targetName, namespace, name)
		return &v1beta1.AdmissionResponse{
			Allowed: false,
		}
	}

	glog.Infof("pod[%s/%s] is not pd-leader,admit to delete", namespace, name)
	return util.ARSuccess()
}
