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
	"github.com/pingcap/tidb-operator/pkg/apis/pingcap.com/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	core "k8s.io/api/core/v1"
)

func IsPodInPdMembers(tc *v1alpha1.TidbCluster, pod *core.Pod, pdClient pdapi.PDClient) (bool, error) {
	name := pod.Name
	namespace := pod.Namespace
	memberInfo, err := pdClient.GetMembers()
	if err != nil {
		return false, fmt.Errorf("tc[%s/%s] failed to get pd memberInfo during delete pod[%s/%s],%v", namespace, tc.Name, namespace, name, err)
	}
	for _, member := range memberInfo.Members {
		if member.Name == name {
			return true, nil
		}
	}
	return false, nil
}
