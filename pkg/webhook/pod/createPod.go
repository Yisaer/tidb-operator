package pod

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	//pvc1Name, pvc2Name := generatePVCName(name)
	//is1Need, err := checkPVCNeedDelete(pvc1Name, namespace)
	//if err != nil {
	//	return util.ARFail(err)
	//}
	//is2Need, err := checkPVCNeedDelete(pvc2Name, namespace)
	//if err != nil {
	//	return util.ARFail(err)
	//}

	//glog.Infof("createPod,PVC1=%v,PVC2=%v", is1Need, is2Need)
	//if is1Need || is2Need {
	//	if is1Need {
	//		deletePVC(pvc1Name, namespace)
	//	}
	//	if is2Need {
	//		deletePVC(pvc2Name, namespace)
	//	}
	//	glog.Infof("AfterDeletePVC,NextTime")
	//	for {
	//		if !is1Need && !is2Need {
	//			break
	//		} else {
	//			is1Need, err = checkPVCNeedDelete(pvc1Name, namespace)
	//			if err != nil {
	//				return util.ARFail(err)
	//			}
	//			is2Need, err = checkPVCNeedDelete(pvc2Name, namespace)
	//			if err != nil {
	//				return util.ARFail(err)
	//			}
	//		}
	//	}
	//}

	patchBytes, err := createPatch(name, namespace)
	if err != nil {
		return util.ARFail(err)
	}
	glog.Infof("Admit to create mixed pod:[%s/%s]", namespace, name)
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
	ordinal := getPodOrdinal(name)
	if ordinal > 0 {
		patch = append(patch, addInitContainer())
	}
	return json.Marshal(patch)
}

func addInitContainer() (patch patchOperation) {
	var containers []core.Container
	command := []string{
		"sh", "-c", "sleep 8",
	}
	container := core.Container{
		Name:            "init",
		Image:           "busybox:1.26.2",
		ImagePullPolicy: "IfNotPresent",
		Command:         command,
	}
	containers = append(containers, container)
	patch = patchOperation{
		Op:    "replace",
		Path:  "/spec/initContainers",
		Value: containers,
	}
	return patch
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

func generateJoinAibo(name, namespace string) (aibo string) {
	ordinal := getPodOrdinal(name)
	aibo = ""
	for i := 0; int32(i) < ordinal; i++ {
		if i > 0 {
			aibo = aibo + ","
		}
		podName := generatePodName(name, int32(i))
		aibo = aibo + fmt.Sprintf("http://%s.demo-peer.%s.svc:2379", podName, namespace)
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

func checkPVCNeedDelete(pvcName, namespace string) (bool, error) {
	glog.Infof("start to find pvc %s", pvcName)
	pvc, err := kubeCli.CoreV1().PersistentVolumeClaims(namespace).Get(pvcName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("failed to find pvc %s", pvcName)
			return false, nil
		}
		return false, err
	}
	_, existed := pvc.Annotations[deleteWhenCreate]
	if existed {
		glog.Infof("pvc %s with annotation", pvcName)
		return true, nil
	}
	glog.Infof("pvc %s without annotation", pvcName)
	return false, nil
}

func deletePVC(pvcName, namespace string) {
	kubeCli.CoreV1().PersistentVolumeClaims(namespace).Delete(pvcName, &metav1.DeleteOptions{})
}
