package pod

import (
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
)

// 1. sync tidb cluster status
// 2. 判断 pd 是否符合预期
// 3. 发现与预期不符合
// 4. 进入PD upgrader 逻辑
// upgrader
// 2.  为True 进入upgrader逻辑

//  PD 模板不同，而Phase为Normal。代表开始进入修改状态
//  PD Phase修改为 EditAble( Upgrading / Scaling) ，退出调和

//  再次进入调和，发现PD Phase是Upgrading 进入 Upgrader
//  PD模板不同，而PD Phase是Upgrading 代表允许开始修改状态
//  修改PD模板

//upgrader
// 1. template相同 version不同
// 2. 根据POD数量版本来设置revision 单步设置

// 进入调和 PD Phase变成 Normal


import (
	"errors"
	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/apis/pingcap.com/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	"github.com/pingcap/tidb-operator/pkg/label"
	pdutil "github.com/pingcap/tidb-operator/pkg/manager/member"
	utils "github.com/pingcap/tidb-operator/pkg/util"
	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

var (
	versionCli   versioned.Interface
	deserializer runtime.Decoder
	pdControl    pdapi.PDControlInterface
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
	if pdControl == nil {
		pdControl = pdapi.NewDefaultPDControl()
	}

	raw := ar.Request.OldObject.Raw
	pod := v1.Pod{}

	if _, _, err := deserializer.Decode(raw, nil, &pod); err != nil {
		glog.Errorf("pod %s/%s, decode request failed, err: %v", namespace, name, err)
		return util.ARFail(err)
	}

	l := label.Label(pod.Labels)

	if !(l.IsPD() || l.IsTiKV() || l.IsTiDB()) {
		// If it is not pod of pd
		return util.ARSuccess()
	}

	tcName := pod.Labels[label.InstanceLabelKey]
	tc, err := versionCli.PingcapV1alpha1().TidbClusters(namespace).Get(tcName, metav1.GetOptions{})

	if err != nil {
		glog.Errorf("get tidbcluster %s/%s failed, statefulset %s, err %v", namespace, tcName, name, err)
		return util.ARFail(err)
	}

	if tc.Status.PD.Phase != v1alpha1.UpgradePhase && tc.Status.PD.Phase != v1alpha1.ScaleInPhase {
		return util.ARFail(errors.New("not upgrading or scaleIn"))
	}

	pdClient := pdControl.GetPDClient(pdapi.Namespace(tc.GetNamespace()), tc.GetName(), tc.Spec.EnableTLSCluster)
	leader, err := pdClient.GetPDLeader()
	if err != nil {
		glog.Errorf("fail to get pd leader %v", err)
		return util.ARFail(err)
	}

	if leader.Name == name {
		ordinal, err := utils.GetOrdinalFromPodName(name)
		if err != nil {
			return util.ARFail(err)
		}
		lastOrdinal := tc.Status.PD.StatefulSet.Replicas - 1
		var targetName string
		if ordinal == lastOrdinal {
			targetName = pdutil.PdPodName(tcName, 0)
		} else {
			targetName = pdutil.PdPodName(tcName, lastOrdinal)
		}
		err = pdClient.TransferPDLeader(targetName)
		if err != nil {
			glog.Errorf("pd upgrader: failed to transfer pd leader to: %s, %v", targetName, err)
			return util.ARFail(err)
		}
		return &v1beta1.AdmissionResponse{
			Allowed: false,
		}
	}

	return util.ARSuccess()
}
