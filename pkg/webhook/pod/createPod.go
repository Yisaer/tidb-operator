package pod

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strconv"
	"strings"
)

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
	err = createService(name, namespace)
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

func createService(name, namespace string) error {
	svc := generateService(name)
	_, err := kubeCli.CoreV1().Services(namespace).Create(svc)
	if err != nil {
		return err
	}
	return nil
}

func generateService(name string) *core.Service {
	pdLabel := map[string]string{
		"statefulset.kubernetes.io/pod-name": name,
	}
	svc := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: pdLabel,
		},
		Spec: core.ServiceSpec{
			Type: core.ServiceTypeNodePort,
			Ports: []core.ServicePort{
				{
					Name:       "tikv",
					Port:       20160,
					TargetPort: intstr.FromInt(20160),
					Protocol:   core.ProtocolTCP,
				},
			},
			Selector: pdLabel,
		},
	}
	return svc
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
		Op:    "replace",
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
	commands = append(commands, fmt.Sprintf("--advertise-peer-urls=%s", fmt.Sprintf("http://%s.demo-peer.%s.svc:2380", name, namespace)))
	commands = append(commands, "--client-urls=http://0.0.0.0:2379")
	commands = append(commands, fmt.Sprintf("--advertise-client-urls=%s", fmt.Sprintf("http://%s.demo-peer.%s.svc:2379", name, namespace)))
	commands = append(commands, "--config=/etc/pd/pd.toml")
	commands = append(commands, generateJoinOrInitial(name, namespace))
	return commands
}

func generateJoinOrInitial(name, namespace string) (command string) {
	ordinal := getPodOrdinal(name)
	if ordinal < 1 {
		command = fmt.Sprintf("--initial-cluster=%s=http://%s.demo-peer.%s.svc:2380", name, name, namespace)
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
		aibo = aibo + fmt.Sprintf("http://%s.demo-peer.%s.svc:2380", podName, namespace)
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
