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
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"strconv"
	"strings"
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

	name := ar.Request.Name
	namespace := ar.Request.Namespace
	operation := ar.Request.Operation
	glog.Infof("receive admission to %s pod[%s/%s]", operation, namespace, name)

	switch operation {
	case v1beta1.Create:
		return AdmitCreatePod(ar)
	default:
		return util.ARSuccess()
	}
}

func AdmitCreatePod(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	raw := ar.Request.Object.Raw
	pod := core.Pod{}
	if _, _, err := deserializer.Decode(raw, nil, &pod); err != nil {
		glog.Errorf("pod %s/%s, decode request failed, err: %v", pod.Name, pod.Namespace, err)
		return util.ARFail(err)
	}
	_, existed := pod.Labels["app.kubernetes.io/instance"]
	if !existed {
		return util.ARSuccess()
	}
	if pod.Labels["app.kubernetes.io/instance"] != "test" {
		return util.ARSuccess()
	}

	name := pod.Name
	namespace := pod.Namespace

	patchBytes, err := createPatch(name, namespace)
	if err != nil {
		return util.ARFail(err)
	}
	return &v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1beta1.PatchType {
			pt := v1beta1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

// create mutation patch
func createPatch(name, namespace string) ([]byte, error) {
	var patch []patchOperation
	patch = append(patch, editPod(name, namespace))

	return json.Marshal(patch)
}

func editPod(name, namespace string) (patch patchOperation) {
	// edit container commands
	commands := generatePDCommand(name, namespace)
	patch = patchOperation{
		Op:    "add",
		Path:  "/spec/containers/0/command",
		Value: commands,
	}
	return patch
}

func generatePDCommand(name, namespace string) (commands []string) {
	commands = append(commands, "/pd-server")
	commands = append(commands, "--data-dir=/var/lib/pd")
	commands = append(commands, fmt.Sprintf("--name=%s", name))
	commands = append(commands, "--peer-urls=http://0.0.0.0:2380")
	commands = append(commands, fmt.Sprintf("--advertise-peer-urls=%s", fmt.Sprintf("http://%s.demo-pd-peer.%s.svc:2380", name, namespace)))
	commands = append(commands, "--client-urls=http://0.0.0.0:2379")
	commands = append(commands, fmt.Sprintf("--advertise-client-urls=%s", fmt.Sprintf("http://%s.demo-pd-peer.%s.svc:2379", name, namespace)))
	commands = append(commands, "--config=/etc/pd/pd.toml")
	commands = append(commands, generateJoinOrInitial(name, namespace))
	return commands
}

func generateJoinOrInitial(name, namespace string) (command string) {
	ordinal := getPodOrdinal(name)
	if ordinal < 1 {
		command = fmt.Sprintf("--initial-cluster=%s=http://%s.demo-pd-peer.%s.svc:2380", name, name, namespace)
	} else {
		command = fmt.Sprintf("--join=%s", generateJoinAibo(name, namespace))
	}
	return command
}

func getPodOrdinal(name string) int32 {
	parts := strings.Split(name, "-")
	ordinal, _ := strconv.ParseInt(parts[len(parts)-1], 10, 32)
	return int32(ordinal)
}

func generateJoinAibo(name, namespace string) (aibo string) {
	ordinal := getPodOrdinal(name)
	aibo = ""
	for i := 0; int32(i) < ordinal; i++ {
		if i > 0 {
			aibo = aibo + ","
		}
		podName := generatePodName(name, int32(i))
		aibo = aibo + fmt.Sprintf("http://%s.demo-pd-peer.%s.svc:2380", podName, namespace)
	}
	return aibo
}

func generatePodName(name string, oridnal int32) (podName string) {
	parts := strings.Split(name, "-")
	podName = ""
	for i := 0; i < len(parts)-1; i++ {
		podName = podName + parts[i] + "-"
	}
	return podName + strconv.FormatInt(int64(oridnal), 10)
}
