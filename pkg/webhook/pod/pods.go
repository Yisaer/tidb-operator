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
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	labelKey = "app.kubernetes.io/instance"
)

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

var (
	pdControl    pdapi.PDControlInterface
	kubeCli      kubernetes.Interface
	deserializer runtime.Decoder
)

func init() {

	if pdControl == nil {
		pdControl = pdapi.NewDefaultPDControl()
	}
	deserializer = util.GetCodec()
}

func AdmitPods(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return util.ARFail(err)
	}

	kubeCli, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		return util.ARFail(err)
	}

	name := ar.Request.Name
	namespace := ar.Request.Namespace
	operation := ar.Request.Operation
	glog.Infof("receive admission to %s pod[%s/%s]", operation, namespace, name)

	switch operation {
	case v1beta1.Create:
		return AdmitCreatePod(ar)
	case v1beta1.Delete:
		return AdmitDeletePod(ar)
	default:
		return util.ARSuccess()
	}
}
