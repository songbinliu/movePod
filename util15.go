package main

import (
	"fmt"
	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	client "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
)

//provides some utilities for Kubernetes 1.5 and older
const (
	SchedulerAnnotationKey = "scheduler.alpha.kubernetes.io/name"
	EmptyScheduler         = "None"
)

func getSchedulerName15(kclient *client.Clientset, kind, nameSpace, name string) (string, error) {
	var annotes *map[string]string = nil

	option := metav1.GetOptions{}
	switch kind {
	case KindReplicationController:
		if rc, err := kclient.CoreV1().ReplicationControllers(nameSpace).Get(name, option); err == nil {
			annotes = &(rc.Spec.Template.Annotations)
		} else {
			return "", err
		}
	case KindReplicaSet:
		if rs, err := kclient.ExtensionsV1beta1().ReplicaSets(nameSpace).Get(name, option); err == nil {
			annotes = &(rs.Spec.Template.Annotations)
		} else {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsported kind:[%v]", kind)
	}

	if *annotes == nil {
		return EmptyScheduler, nil
	}

	result, ok := (*annotes)[SchedulerAnnotationKey]
	if !ok || result == "" {
		return EmptyScheduler, nil
	}

	return result, nil
}

func checkSchedulerName15(client *client.Clientset, kind, nameSpace, name, expectedScheduler string) (bool, error) {
	currentName, err := getSchedulerName15(client, kind, nameSpace, name)
	if err != nil {
		return false, err
	}

	if currentName == expectedScheduler {
		return true, nil
	}

	return false, nil
}

func updateAnnotedScheduler2(pod *v1.PodTemplateSpec, newName string) string {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	defer func() {
		if len(pod.Annotations) == 0 {
			pod.Annotations = nil
		}
	}()

	if newName == EmptyScheduler {
		current, ok := pod.Annotations[SchedulerAnnotationKey]
		if !ok {
			return EmptyScheduler
		}

		delete(pod.Annotations, SchedulerAnnotationKey)
		return current
	}

	current, ok := pod.Annotations[SchedulerAnnotationKey]
	if !ok {
		pod.Annotations[SchedulerAnnotationKey] = newName
		return EmptyScheduler
	}
	if current == "" {
		current = EmptyScheduler
	}

	pod.Annotations[SchedulerAnnotationKey] = newName
	return current
}

func updateAnnotedScheduler(annotes map[string]string, value string) string {
	if value == EmptyScheduler {
		if annotes == nil {
			return EmptyScheduler
		}

		current, ok := annotes[SchedulerAnnotationKey]
		if !ok {
			return EmptyScheduler
		}

		delete(annotes, SchedulerAnnotationKey)
		return current
	}

	if annotes == nil {
		annotes = make(map[string]string)
		annotes[SchedulerAnnotationKey] = value
		return EmptyScheduler
	}

	current, ok := annotes[SchedulerAnnotationKey]
	if !ok {
		annotes[SchedulerAnnotationKey] = value
		return EmptyScheduler
	}
	if current == "" {
		current = EmptyScheduler
	}

	annotes[SchedulerAnnotationKey] = value

	return current
}

func doSchedulerMove15(kclient *client.Clientset, pod *v1.Pod, parentKind, parentName, nodeName string) error {
	id := fmt.Sprintf("%v/%v", pod.Namespace, pod.Name)
	//2. update the schedulerName
	var update func(*client.Clientset, string, string, string, int) (string, error)
	switch parentKind {
	case KindReplicationController:
		glog.V(3).Infof("pod-%v parent is a ReplicationController-%v", id, parentName)
		update = updateRCscheduler15
	case KindReplicaSet:
		glog.V(2).Infof("pod-%v parent is a ReplicaSet-%v", id, parentName)
		update = updateRSscheduler15
	default:
		err := fmt.Errorf("unsupported parent-[%v] Kind-[%v]", parentName, parentKind)
		glog.Warning(err.Error())
		return err
	}

	noexist := noexistSchedulerName
	check := checkSchedulerName15
	nameSpace := pod.Namespace

	preScheduler, err := update(kclient, nameSpace, parentName, noexist, 1)
	if flag, err2 := check(kclient, parentKind, nameSpace, parentName, noexist); !flag {
		prefix := fmt.Sprintf("move-failed: pod-[%v], parent-[%v]", pod.Name, parentName)
		return addErrors(prefix, err, err2)
	}

	restore := func() {
		//check it again in case somebody has changed it to something else.
		if flag, _ := check(kclient, parentKind, nameSpace, parentName, noexist); flag {
			update(kclient, nameSpace, parentName, preScheduler, DefaultRetryMore)
		}

		err := cleanPendingPod(kclient, nameSpace, noexist, parentKind, parentName)
		if err != nil {
			glog.Errorf("failed to cleanPendingPod for MoveAction:%s", err.Error())
		}
	}
	defer restore()

	//3. movePod
	return movePod(kclient, pod, nodeName, DefaultRetryLess)
}

//update the schedulerName of a ReplicationController
// return the previous schedulerName, or return "" if update is not necessary or updated failed.
func updateRCscheduler15(client *client.Clientset, nameSpace, rcName, schedulerName string, retryNum int) (string, error) {
	currentName := ""

	id := fmt.Sprintf("%v/%v", nameSpace, rcName)
	rcClient := client.CoreV1().ReplicationControllers(nameSpace)
	if rcClient == nil {
		return "", fmt.Errorf("failed to get ReplicaSet client in namespace: %v", nameSpace)
	}

	//1. get
	option := metav1.GetOptions{}
	rc, err := rcClient.Get(rcName, option)
	if err != nil {
		err = fmt.Errorf("failed to get ReplicationController-%v: %v\n", id, err.Error())
		glog.Error(err.Error())
		return "", err
	}

	//2. update
	p := rc.Spec.Template
	currentName = updateAnnotedScheduler2(p, schedulerName)
	if currentName == schedulerName {
		return "", nil
	}

	//_, err = rcClient.Update(rc)
	err = retryDuring(retryNum, DefaultTimeOut, DefaultSleep, func() error {
		_, inerr := rcClient.Update(rc)
		return inerr
	})
	if err != nil {
		//TODO: check whether to retry
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	glog.V(2).Infof("update %v schedulerName [%v] to [%v]", id, currentName, schedulerName)

	return currentName, nil
}

//update the schedulerName of a ReplicaSet to <schedulerName>
// return the previous schedulerName
func updateRSscheduler15(client *client.Clientset, nameSpace, rsName, schedulerName string, retryNum int) (string, error) {
	currentName := ""

	id := fmt.Sprintf("%v/%v", nameSpace, rsName)
	rsClient := client.ExtensionsV1beta1().ReplicaSets(nameSpace)
	if rsClient == nil {
		return "", fmt.Errorf("failed to get ReplicaSet client in namespace: %v", nameSpace)
	}

	//1. get ReplicaSet
	option := metav1.GetOptions{}
	rs, err := rsClient.Get(rsName, option)
	if err != nil {
		err = fmt.Errorf("failed to get ReplicaSet-%v: %v", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	//2. update schedulerName
	p := &(rs.Spec.Template)
	//currentName = updateAnnotedScheduler(p, schedulerName)
	currentName = updateAnnotedScheduler2(p, schedulerName)
	if currentName == schedulerName {
		return "", nil
	}

	err = retryDuring(retryNum, DefaultTimeOut, DefaultSleep, func() error{
		_, err = rsClient.Update(rs)
		return err
	})
	_, err = rsClient.Update(rs)
	if err != nil {
		//TODO: check whether to retry
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	glog.V(2).Infof("update %v schedulerName [%v] to [%v]", id, currentName, schedulerName)

	return currentName, nil
}
