package util

import (
	"fmt"

	"github.com/golang/glog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclient "k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/pkg/api/v1"
)

const (
	kindReplicationController     = "ReplicationController"
	kindReplicaSet                = "ReplicaSet"

	podDeletionGracePeriodDefault int64 = 10
	podDeletionGracePeriodMax     int64 = 10
	defaultSleep                        = time.Second * 30
	defaultTimeOut                      = time.Second * 90
	defaultRetryLess                    = 2
	defaultRetryMore                    = 4
)

func calcGracePeriod(pod *api.Pod) int64 {
	grace := podDeletionGracePeriodDefault
	if pod.Spec.TerminationGracePeriodSeconds != nil {
		grace = *(pod.Spec.TerminationGracePeriodSeconds)
		if grace > podDeletionGracePeriodMax {
			grace = podDeletionGracePeriodMax
		}
	}
	return grace
}

// move pod nameSpace/podName to node nodeName
func MovePod(client *kclient.Clientset, pod *api.Pod, nodeName string, retryNum int) error {
	podClient := client.CoreV1().Pods(pod.Namespace)
	if podClient == nil {
		err := fmt.Errorf("cannot get Pod client for nameSpace:%v", pod.Namespace)
		glog.Error(err)
		return err
	}

	//1. copy the original pod
	id := fmt.Sprintf("%v/%v", pod.Namespace, pod.Name)
	glog.V(2).Infof("move-pod: begin to move %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	npod := &api.Pod{}
	CopyPodInfo(pod, npod)
	npod.Spec.NodeName = nodeName
	glog.V(2).Infof("new pod name: " + npod.Name)

	//2. kill original pod
	grace := calcGracePeriod(pod)
	delOption := &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	err := podClient.Delete(pod.Name, delOption)
	if err != nil {
		err = fmt.Errorf("move-failed: failed to delete original pod-%v: %v",
			id, err)
		glog.Error(err)
		return err
	}

	//3. create (and bind) the new Pod
	time.Sleep(time.Duration(grace+1) * time.Second) //wait for the previous pod to be cleaned up.
	du := time.Duration(grace+3) * time.Second
	err = RetryDuring(retryNum, du*time.Duration(retryNum), defaultSleep, func() error {
		_, inerr := podClient.Create(npod)
		return inerr
	})
	if err != nil {
		err = fmt.Errorf("move-failed: failed to create new pod-%v: %v",
			id, err)
		glog.Error(err)
		return err
	}

	glog.V(2).Infof("move-finished: %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	return nil
}

//---------------Move Helper---------------

type getSchedulerNameFunc func(client *kclient.Clientset, nameSpace, name string) (string, error)
type updateSchedulerFunc func(client *kclient.Clientset, nameSpace, name, scheduler string) (string, error)

type moveHelper struct {
	client    *kclient.Clientset
	nameSpace string
	podName   string

	//parent controller's kind: ReplicationController/ReplicaSet
	kind string
	//parent controller's name
	controllerName string

	//the none-exist scheduler name
	schedulerNone string

	//the original scheduler of the parent controller
	scheduler string
	flag      bool

	//functions to manipulate schedulerName via K8s'API
	getSchedulerName    getSchedulerNameFunc
	updateSchedulerName updateSchedulerFunc

	//for debug
	key string
}

func NewMoveHelper(client *kclient.Clientset, nameSpace, name, kind, parentName, noneScheduler string, highver bool) (*moveHelper, error) {
	p := &moveHelper{
		client:         client,
		nameSpace:      nameSpace,
		podName:        name,
		kind:           kind,
		controllerName: parentName,
		schedulerNone:  noneScheduler,
		flag:           false,
		key:            fmt.Sprintf("%s/%s", nameSpace, name),
	}

	switch p.kind {
	case kindReplicationController:
		p.getSchedulerName = GetRCschedulerName
		p.updateSchedulerName = UpdateRCscheduler
		if !highver {
			p.getSchedulerName = GetRCschedulerName15
			p.updateSchedulerName = UpdateRCscheduler15
		}
	case kindReplicaSet:
		p.getSchedulerName = GetRSschedulerName
		p.updateSchedulerName = UpdateRSscheduler
		if !highver {
			p.getSchedulerName = GetRSschedulerName15
			p.updateSchedulerName = UpdateRSscheduler15
		}
	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}

	return p, nil
}

// check whether the current scheduler is equal to the expected scheduler.
// will renew lock.
func (h *moveHelper) CheckScheduler(expectedScheduler string, retry int) (bool, error) {

	flag := false

	err := RetryDuring(retry, defaultTimeOut, time.Second, func() error {
		scheduler, err := h.getSchedulerName(h.client, h.nameSpace, h.controllerName)
		if err == nil && scheduler == expectedScheduler {
			flag = true
			return nil
		}

		return err
	})

	if err != nil {
		glog.Errorf("failed to check scheduler name for %s: %v", h.key, err)
	}

	return flag, err
}

func (h *moveHelper) UpdateScheduler(schedulerName string, retry int) (string, error) {
	result := ""

	err := RetryDuring(retry, defaultTimeOut, defaultSleep, func() error {
		sname, ierr := h.updateSchedulerName(h.client, h.nameSpace, h.controllerName, schedulerName)
		result = sname
		return ierr
	})

	return result, err
}

func (h *moveHelper) SetScheduler(schedulerName string) {
	if h.flag {
		glog.Warningf("schedulerName has already been set.")
	}

	h.scheduler = schedulerName
	h.flag = true
}

// CleanUp: (1) restore scheduler Name, (2) Release lock
func (h *moveHelper) CleanUp() {
	if !(h.flag) {
		return
	}

	if flag, _ := h.CheckScheduler(h.schedulerNone, defaultRetryLess); !flag {
		return
	}

	h.UpdateScheduler(h.scheduler, defaultRetryMore)
}
