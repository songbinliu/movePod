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

	podDeletionGracePeriodDefault int64 = 0
	podDeletionGracePeriodMax     int64 = 0
	defaultSleep                        = time.Second * 3
	defaultTimeOut                      = time.Second * 10
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
func MovePod(client *kclient.Clientset, pod *api.Pod, nodeName string, retryNum int) (*api.Pod, error) {
	podClient := client.CoreV1().Pods(pod.Namespace)
	if podClient == nil {
		err := fmt.Errorf("cannot get Pod client for nameSpace:%v", pod.Namespace)
		glog.Error(err)
		return nil, err
	}

	//1. copy the original pod
	id := fmt.Sprintf("%v/%v", pod.Namespace, pod.Name)
	glog.V(2).Infof("move-pod: begin to move %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	npod := &api.Pod{}
	CopyPodInfo(pod, npod)
	npod.Spec.NodeName = nodeName

	//2. kill original pod
	grace := calcGracePeriod(pod)
	delOption := &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	err := podClient.Delete(pod.Name, delOption)
	if err != nil {
		err = fmt.Errorf("move-failed: failed to delete original pod-%v: %v",
			id, err)
		glog.Error(err)
		return nil, err
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
		return nil, err
	}

	glog.V(2).Infof("move-finished: %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	return npod, nil
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

//---------------Move Helper with concurrency control---------------
// move one Pod each time for the same ReplicationController/ReplicaSet

type moveHelper2 struct {
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

	//for the expirationMap
	emap    *ExpirationMap
	key     string
	version int64
}

func NewMoveHelper2(client *kclient.Clientset, nameSpace, name, kind, parentName, noneScheduler string, highver bool) (*moveHelper2, error) {

	p := &moveHelper2{
		client:         client,
		nameSpace:      nameSpace,
		podName:        name,
		kind:           kind,
		controllerName: parentName,
		schedulerNone:  noneScheduler,
		flag:           false,
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

func (h *moveHelper2) SetMap(emap *ExpirationMap) {
	h.emap = emap
	h.key = fmt.Sprintf("%s-%s-%s", h.kind, h.nameSpace, h.controllerName)
}

// check whether the current scheduler is equal to the expected scheduler.
// will renew lock.
func (h *moveHelper2) CheckScheduler(expectedScheduler string, retry int) (bool, error) {

	flag := false

	err := RetryDuring(retry, defaultTimeOut, time.Second, func() error {
		if flag = h.Renewlock(); !flag {
			return nil
		}

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

// need to renew lock
func (h *moveHelper2) UpdateScheduler(schedulerName string, retry int) (string, error) {
	result := ""
	flag := true

	err := RetryDuring(retry, defaultTimeOut, defaultSleep, func() error {
		if flag = h.Renewlock(); !flag {
			return nil
		}

		sname, err := h.updateSchedulerName(h.client, h.nameSpace, h.controllerName, schedulerName)
		result = sname
		return err
	})

	if !flag {
		return result, fmt.Errorf("Timeout")
	}

	if err != nil {
		glog.Errorf("Failed to updateScheduler for pod[%s], parent[%s]", h.podName, h.controllerName)
	}

	return result, err
}

func (h *moveHelper2) SetScheduler(schedulerName string) {
	if h.flag {
		glog.Warningf("schedulerName has already been set.")
	}

	h.scheduler = schedulerName
	h.flag = true
}

// CleanUp: (1) restore scheduler Name, (2) Release lock
func (h *moveHelper2) CleanUp() {
	defer h.Releaselock()

	if !(h.flag) {
		return
	}

	if flag, _ := h.CheckScheduler(h.schedulerNone, defaultRetryLess); !flag {
		return
	}

	if _, err := h.UpdateScheduler(h.scheduler, defaultRetryMore); err != nil {
		glog.Errorf("Clean up failed: failed to updateScheduler for pod[%s], parent[%s]", h.podName, h.controllerName)
	}
}

// acquire a lock before manipulate the scheduler of the parentController
func (h *moveHelper2) Acquirelock() bool {
	version, flag := h.emap.Add(h.key, nil, func(obj interface{}) {
		h.lockCallBack()
	})

	if !flag {
		glog.V(3).Infof("Failed to get lock for pod[%s], parent[%s]", h.podName, h.controllerName)
		return false
	}

	glog.V(3).Infof("Get lock for pod[%s], parent[%s]", h.podName, h.controllerName)
	h.version = version
	return true
}

// update the lock to prevent timeout
func (h *moveHelper2) Renewlock() bool {
	return h.emap.Touch(h.key, h.version)
}

// release the lock of the parentController
func (h *moveHelper2) Releaselock() {
	h.emap.Del(h.key, h.version)
	glog.V(3).Infof("Released lock for pod[%s], parent[%s]", h.podName, h.controllerName)
}

// the call back function, the lock should have already be acquired;
// This callback function should do the minimum thing: restore the original scheduler
// the pending pods should be deleted by other things.
func (h *moveHelper2) lockCallBack() {
	glog.V(3).Infof("localCallBack--Expired lock for pod[%s], parent[%s]", h.podName, h.controllerName)
	// check whether need to do reset scheduler
	if !(h.flag) {
		return
	}

	// check whether the scheduler has been changed.
	scheduler, err := h.getSchedulerName(h.client, h.nameSpace, h.controllerName)
	if err != nil || scheduler != h.schedulerNone {
		return
	}

	// restore the original scheduler
	RetryDuring(defaultRetryMore, defaultTimeOut, defaultSleep, func() error {
		_, err := h.updateSchedulerName(h.client, h.nameSpace, h.controllerName, h.scheduler)
		return err
	})

	return
}
