package pod

import (
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AdmitDeletePod(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {

	name := ar.Request.Name
	namespace := ar.Request.Namespace

	pod, err := kubeCli.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return util.ARFail(err)
	}

	_, existed := pod.Labels[labelKey]
	if !existed {
		return util.ARSuccess()
	}
	if pod.Labels[labelKey] != "test" {
		return util.ARSuccess()
	}

	err = kubeCli.CoreV1().Services(namespace).Delete(name, &metav1.DeleteOptions{})

	if err != nil {
		return util.ARFail(err)
	}

	glog.Infof("mixed pod[%s/%s] admit to delete", namespace, name)
	return util.ARSuccess()
}
