package pod

import (
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	"github.com/pingcap/tidb-operator/pkg/label"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

var (
	versionCli   versioned.Interface
	deserializer runtime.Decoder
)

func init() {
	deserializer = util.GetCodec()
}

func AdmitPods(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {

	name := ar.Request.Name
	namespace := ar.Request.Namespace
	glog.V(4).Infof("admit pod [%s/%s]", namespace, name)

	if versionCli == nil {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			glog.Errorf("statefulset %s/%s, get k8s cluster config failed, err: %v", namespace, name, err)
			return util.ARFail(err)
		}

		versionCli, err = versioned.NewForConfig(cfg)
		if err != nil {
			glog.Errorf("statefulset %s/%s, create Clientset failed, err: %v", namespace, name, err)
			return util.ARFail(err)
		}
	}

	raw := ar.Request.OldObject.Raw
	pod := v1.Pod{}

	if _, _, err := deserializer.Decode(raw, nil, &pod); err != nil {
		glog.Errorf("pod %s/%s, decode request failed, err: %v", namespace, name, err)
		return util.ARFail(err)
	}

	l := label.Label(pod.Labels)

	if !(l.IsPD()) {
		// If it is not pod of pd
		return util.ARSuccess()
	}

	return nil
}
