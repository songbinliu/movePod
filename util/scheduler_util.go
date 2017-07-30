package util

import (
	"fmt"
	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclient "k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/pkg/api/v1"
)

func GetRSschedulerName(client *kclient.Clientset, nameSpace, name string) (string, error) {
	option := metav1.GetOptions{}
	if rs, err := client.ExtensionsV1beta1().ReplicaSets(nameSpace).Get(name, option); err == nil {
		return rs.Spec.Template.Spec.SchedulerName, nil
	} else {
		return "", err
	}
}

func GetRCschedulerName(client *kclient.Clientset, nameSpace, name string) (string, error) {
	option := metav1.GetOptions{}
	if rc, err := client.CoreV1().ReplicationControllers(nameSpace).Get(name, option); err == nil {
		return rc.Spec.Template.Spec.SchedulerName, nil
	} else {
		return "", err
	}
}

//update the schedulerName of a ReplicaSet to <schedulerName>
// return the previous schedulerName
func UpdateRSscheduler(client *kclient.Clientset, nameSpace, rsName, schedulerName string) (string, error) {
	currentName := ""

	rsClient := client.ExtensionsV1beta1().ReplicaSets(nameSpace)
	if rsClient == nil {
		return "", fmt.Errorf("failed to get ReplicaSet client in namespace: %v", nameSpace)
	}

	id := fmt.Sprintf("%v/%v", nameSpace, rsName)

	//1. get ReplicaSet
	option := metav1.GetOptions{}
	rs, err := rsClient.Get(rsName, option)
	if err != nil {
		err = fmt.Errorf("failed to get ReplicaSet-%v: %v", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	if rs.Spec.Template.Spec.SchedulerName == schedulerName {
		glog.V(3).Infof("no need to update schedulerName for RS-[%v]", rsName)
		return "", nil
	}

	//2. update schedulerName
	rs.Spec.Template.Spec.SchedulerName = schedulerName
	_, err = rsClient.Update(rs)
	if err != nil {
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	return currentName, nil
}

//update the schedulerName of a ReplicationController
// if condName is not empty, then only current schedulerName is same to condName, then will do the update.
// return the previous schedulerName; or return "" if update failed.
func UpdateRCscheduler(client *kclient.Clientset, nameSpace, rcName, schedulerName string) (string, error) {
	currentName := ""

	id := fmt.Sprintf("%v/%v", nameSpace, rcName)
	rcClient := client.CoreV1().ReplicationControllers(nameSpace)

	//1. get
	option := metav1.GetOptions{}
	rc, err := rcClient.Get(rcName, option)
	if err != nil {
		err = fmt.Errorf("failed to get ReplicationController-%v: %v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	if rc.Spec.Template.Spec.SchedulerName == schedulerName {
		glog.V(3).Infof("no need to update schedulerName for RC-[%v]", rcName)
		return "", nil
	}

	//2. update
	rc.Spec.Template.Spec.SchedulerName = schedulerName
	_, err = rcClient.Update(rc)
	if err != nil {
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	return currentName, nil
}

//-------- for kclient version < 1.6 ------------------
// for Kubernetes version < 1.6, the schedulerName is set in Pod annotations, not in schedulerName field.
const (
	schedulerAnnotationKey = "scheduler.alpha.kubernetes.io/name"
	emptyScheduler         = "None"
)

func parseAnnotatedScheduler(an map[string]string) (string, error) {
	if an == nil {
		return emptyScheduler, nil
	}

	result, ok := an[schedulerAnnotationKey]
	if !ok || result == "" {
		return emptyScheduler, nil
	}

	return result, nil
}

func GetRSschedulerName15(client *kclient.Clientset, nameSpace, name string) (string, error) {
	option := metav1.GetOptions{}
	if rs, err := client.ExtensionsV1beta1().ReplicaSets(nameSpace).Get(name, option); err == nil {
		return parseAnnotatedScheduler(rs.Spec.Template.Annotations)
	} else {
		return "", err
	}
}

func GetRCschedulerName15(client *kclient.Clientset, nameSpace, name string) (string, error) {
	option := metav1.GetOptions{}
	if rc, err := client.CoreV1().ReplicationControllers(nameSpace).Get(name, option); err == nil {
		return parseAnnotatedScheduler(rc.Spec.Template.Annotations)
	} else {
		return "", err
	}
}

// shedulerName is set in pod.Annotations for kubernetes version < 1.6
func updateAnnotatedScheduler(pod *api.PodTemplateSpec, newName string) string {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	defer func() {
		if len(pod.Annotations) == 0 {
			pod.Annotations = nil
		}
	}()

	if newName == emptyScheduler {
		current, ok := pod.Annotations[schedulerAnnotationKey]
		if !ok {
			return emptyScheduler
		}

		delete(pod.Annotations, schedulerAnnotationKey)
		return current
	}

	current, ok := pod.Annotations[schedulerAnnotationKey]
	if !ok {
		pod.Annotations[schedulerAnnotationKey] = newName
		return emptyScheduler
	}
	if current == "" {
		current = emptyScheduler
	}

	pod.Annotations[schedulerAnnotationKey] = newName
	return current
}

//update the schedulerName of a ReplicationController, schedulerName is set in Sepc.Template.Annotations
// return the previous schedulerName, or return "" if update is not necessary or updated failed.
func UpdateRCscheduler15(client *kclient.Clientset, nameSpace, rcName, schedulerName string) (string, error) {
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
	currentName = updateAnnotatedScheduler(p, schedulerName)
	if currentName == schedulerName {
		return "", nil
	}

	_, err = rcClient.Update(rc)
	if err != nil {
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	glog.V(2).Infof("update %v schedulerName [%v] to [%v]", id, currentName, schedulerName)

	return currentName, nil
}

//update the schedulerName of a ReplicaSet to <schedulerName>, schedulerName is set in Sepc.Template.Annotations
// return the previous schedulerName
func UpdateRSscheduler15(client *kclient.Clientset, nameSpace, rsName, schedulerName string) (string, error) {
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
	currentName = updateAnnotatedScheduler(p, schedulerName)
	if currentName == schedulerName {
		return "", nil
	}

	_, err = rsClient.Update(rs)
	if err != nil {
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return currentName, err
	}

	glog.V(2).Infof("update %v schedulerName [%v] to [%v]", id, currentName, schedulerName)

	return currentName, nil
}

func ParsePodSchedulerName(pod *api.Pod, highver bool) string {

	if highver {
		return pod.Spec.SchedulerName
	}

	if pod.Annotations != nil {
		if sname, ok := pod.Annotations[schedulerAnnotationKey]; ok {
			return sname
		}
	}

	return ""
}

