package pod

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"time"
)

const (
	deleteWhenCreate = "deleteWhenCreate"
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

	stsName := pod.OwnerReferences[0].Name
	_, err = kubeCli.AppsV1().StatefulSets(namespace).Get(stsName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("sts is deleted")
			return util.ARSuccess()
		}
	}

	isInRange, err := isInRange(name, namespace, pod)
	if err != nil {
		return util.ARFail(err)
	}
	if isInRange {
		return util.ARSuccess()
	}

	pdClient := pdControl.GetPDClient(pdapi.Namespace(namespace), "demo", false)

	stores, err := getStores(pdClient, name)
	if err != nil {
		return util.ARFail(err)
	}
	isStoreExisted, store := checkStoreExisted(stores, name)
	isTomb := false
	if isStoreExisted {
		isTomb = checkTomStone(store)
		if !isTomb {
			if store.Store.State != metapb.StoreState_Offline {
				glog.Infof("start to delete store,address = %s", store.Store.Address)
				err = pdClient.DeleteStore(store.Store.Id)
				if err != nil {
					glog.Infof("failed to delete member,%v", err)
				}
			}
			return &v1beta1.AdmissionResponse{
				Allowed: false,
			}
		}
	}
	glog.Infof("storeExisted = %v , TombStone = %v", isStoreExisted, isTomb)
	glog.Infof("store is tombStone Or Store is Not existed Now")

	isMember, err := isMember(pdClient, name)
	if err != nil {
		return util.ARFail(err)
	}

	if isMember {
		pdClient.DeleteMember(name)
		glog.Infof("AfterDeleteMember,NextTime Delete")
		return &v1beta1.AdmissionResponse{
			Allowed: false,
		}
	}
	glog.Infof("pd member[%s] is not existed now ", name)

	pvc1Name, pvc2Name := generatePVCName(name)
	go func() {
		time.Sleep(2 * time.Second)
		deletePVC(pvc1Name, namespace)
		deletePVC(pvc2Name, namespace)
	}()

	glog.Infof("mixed pod[%s/%s] admit to delete", namespace, name)
	return util.ARSuccess()
}

func getPodNameFromAddress(address string) string {
	parts := strings.Split(address, ".")
	return parts[0]
}

func getStores(client pdapi.PDClient, name string) ([]*pdapi.StoreInfo, error) {
	stores, err := client.GetStores()
	if err != nil {
		return nil, err
	}
	return stores.Stores, nil
}

func checkStoreExisted(stores []*pdapi.StoreInfo, name string) (bool, *pdapi.StoreInfo) {
	for _, store := range stores {
		address := store.Store.Address
		podName := getPodNameFromAddress(address)
		if name == podName {
			glog.Infof("pod[%s] store state = %s", podName, store.Store.State.String())
			return true, store
		}
	}
	return false, nil
}

func checkTomStone(store *pdapi.StoreInfo) bool {
	if store.Store.State == metapb.StoreState_Tombstone {
		return true
	}
	return false
}

func isInRange(name, namespace string, pod *v1.Pod) (bool, error) {
	ordinal := getPodOrdinal(name)
	stsName := pod.OwnerReferences[0].Name
	sts, err := kubeCli.AppsV1().StatefulSets(namespace).Get(stsName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	number := sts.Spec.Replicas
	if ordinal < *number {
		return true, nil
	}
	return false, nil
}

func generatePVCName(name string) (pdPVCName, tikvPVCName string) {
	return fmt.Sprintf("pd-storage-%s", name), fmt.Sprintf("tikv-storage-%s", name)
}

func editPVC(pvcName, namespace string) error {
	pvc, err := kubeCli.CoreV1().PersistentVolumeClaims(namespace).Get(pvcName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	pvc.Annotations = map[string]string{
		deleteWhenCreate: deleteWhenCreate,
	}
	_, err = kubeCli.CoreV1().PersistentVolumeClaims(namespace).Update(pvc)
	if err != nil {
		return err
	}
	return nil
}

func isMember(pdClient pdapi.PDClient, name string) (bool, error) {
	members, err := pdClient.GetMembers()
	if err != nil {
		return false, err
	}
	for _, member := range members.Members {
		glog.Infof("member name = %s,name=%s", member.Name, name)
		if member.Name == name {
			return true, nil
		}
	}
	return false, nil
}
