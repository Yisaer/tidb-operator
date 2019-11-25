package main

import (
	"github.com/openshift/generic-admission-server/pkg/cmd"
	admission "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

func main() {
	cmd.RunAdmissionServer(&admissionHook{})
}

type admissionHook struct {
	kubeCli     kubernetes.Interface
	initialized bool
}

// where to host it
func (a *admissionHook) ValidatingResource() (plural schema.GroupVersionResource, singular string) {
	return schema.GroupVersionResource{
			Group:    "pingcap.com",
			Version:  "v1alpha1",
			Resource: "admissionreviews",
		},
		"admissionreview"
}

// your business logic
func (a *admissionHook) Validate(admissionSpec *admission.AdmissionRequest) *admission.AdmissionResponse {

	if !a.initialized {
		klog.Infof("admission server not initializer ready")
		return &admission.AdmissionResponse{
			Allowed: false,
		}
	}

	name := admissionSpec.Name
	namespace := admissionSpec.Namespace
	klog.Infof("receive delete operator for pod[%s/%s]", namespace, name)
	return &admission.AdmissionResponse{
		Allowed: true,
	}
}

func (a *admissionHook) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {

	kubeCli, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}
	a.kubeCli = kubeCli
	a.initialized = true
	return nil
}
