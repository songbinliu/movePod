package main

import (
	"encoding/json"
	"fmt"
	"time"
	"strings"
	"strconv"

	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	client "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	//"k8s.io/apimachinery/pkg/labels"
)

const (
	// Set the grace period to 0 for deleting the pod immediately.
	DefaultPodGracePeriod int64 = 10
	DefaultRetryLess int = 2
	DefaultRetryMore int = 5
	DefaultTimeOut = time.Second*32
	DefaultSleep = time.Second*5
)

func printPods(pods *v1.PodList) {
	fmt.Printf("api version:%s, kind:%s, r.version:%s\n",
		pods.APIVersion,
		pods.Kind,
		pods.ResourceVersion)

	for _, pod := range pods.Items {
		fmt.Printf("%s/%s, phase:%s, node.Name:%s, host:%s\n",
			pod.Namespace,
			pod.Name,
			pod.Status.Phase,
			pod.Spec.NodeName,
			pod.Status.HostIP)
	}
}

func compareVersion(version1, version2 string) int {
	a1 := strings.Split(version1, ".")
	a2 := strings.Split(version2, ".")

	l1 := len(a1)
	l2 := len(a2)
	mlen := l1
	if mlen < l2 {
		mlen = l2
	}

	for i := 0; i < mlen; i ++ {
		b1 := 0
		if i < l1 {
			if tmp, err := strconv.Atoi(a1[i]); err == nil {
				b1 = tmp
			}
		}

		b2 := 0
		if i < l2 {
			if tmp, err := strconv.Atoi(a2[i]); err == nil {
				b2 = tmp
			}
		}

		if b1 != b2 {
			return b1 - b2
		}

	}

	return 0
}

func listPod(client *client.Clientset) {
	pods, err := client.CoreV1().Pods(v1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))
	printPods(pods)

	glog.V(2).Info("test finish")
}

func copyPodInfoX(oldPod, newPod *v1.Pod) {
	//1. typeMeta
	newPod.TypeMeta = oldPod.TypeMeta

	//2. objectMeta
	newPod.ObjectMeta = oldPod.ObjectMeta
	newPod.SelfLink = ""
	newPod.ResourceVersion = ""
	newPod.Generation = 0
	newPod.CreationTimestamp = metav1.Time{}
	newPod.DeletionTimestamp = nil
	newPod.DeletionGracePeriodSeconds = nil

	//3. podSpec
	spec := oldPod.Spec
	spec.Hostname = ""
	spec.Subdomain = ""
	spec.NodeName = ""

	newPod.Spec = spec
	return
}

func copyPodInfo(oldPod, newPod *v1.Pod) {
	//1. typeMeta -- full copy
	newPod.Kind = oldPod.Kind
	newPod.APIVersion = oldPod.APIVersion

	//2. objectMeta -- partial copy
	newPod.Name = oldPod.Name
	newPod.GenerateName = oldPod.GenerateName
	newPod.Namespace = oldPod.Namespace
	//newPod.SelfLink = oldPod.SelfLink
	newPod.UID = oldPod.UID
	//newPod.ResourceVersion = oldPod.ResourceVersion
	//newPod.Generation = oldPod.Generation
	//newPod.CreationTimestamp = oldPod.CreationTimestamp

	//NOTE: Deletion timestamp and gracePeriod will be set by system when to delete it.
	//newPod.DeletionTimestamp = oldPod.DeletionTimestamp
	//newPod.DeletionGracePeriodSeconds = oldPod.DeletionGracePeriodSeconds

	newPod.Labels = oldPod.Labels
	newPod.Annotations = oldPod.Annotations
	newPod.OwnerReferences = oldPod.OwnerReferences
	newPod.Initializers = oldPod.Initializers
	newPod.Finalizers = oldPod.Finalizers
	newPod.ClusterName = oldPod.ClusterName

	//3. podSpec -- full copy with modifications
	spec := oldPod.Spec
	spec.Hostname = ""
	spec.Subdomain = ""
	spec.NodeName = ""

	newPod.Spec = spec

	//4. status: won't copy status
}

func getParentInfo(pod *v1.Pod) (string, string, error) {
	//1. check ownerReferences:
	if pod.OwnerReferences != nil && len(pod.OwnerReferences) > 0 {
		for _, owner := range pod.OwnerReferences {
			if *owner.Controller {
				return owner.Kind, owner.Name, nil
			}
		}
	}

	glog.V(3).Infof("cannot find pod-%v/%v parent by OwnerReferences.", pod.Namespace, pod.Name)

	//2. check annotations:
	if pod.Annotations != nil && len(pod.Annotations) > 0 {
		key := "kubernetes.io/created-by"
		if value, ok := pod.Annotations[key]; ok {
			var ref v1.SerializedReference

			if err := json.Unmarshal([]byte(value), &ref); err != nil {
				err = fmt.Errorf("failed to decode parent annoation:%v\n[%v]", err.Error(), value)
				glog.Error(err.Error())
				return "", "", err
			}

			return ref.Reference.Kind, ref.Reference.Name, nil
		}
	}

	glog.V(3).Infof("cannot find pod-%v/%v parent by Annotations.", pod.Namespace, pod.Name)

	return "", "", nil
}

func getKubeClient(masterUrl, kubeConfig string) *client.Clientset {
	if masterUrl == "" && kubeConfig == "" {
		fmt.Println("must specify masterUrl or kubeConfig.")
		return nil
	}

	var err error
	var config *restclient.Config

	if kubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
	} else {
		config, err = clientcmd.BuildConfigFromFlags(masterUrl, "")
	}

	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := client.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	return clientset
}

func getSchedulerName(client *client.Clientset, kind, nameSpace, name string) (string, error) {
	rerr := fmt.Errorf("unsupported kind:[%v]", kind)

	option := metav1.GetOptions{}
	switch kind {
	case KindReplicationController:
		if rc, err := client.CoreV1().ReplicationControllers(nameSpace).Get(name, option); err == nil {
			return rc.Spec.Template.Spec.SchedulerName, nil
		} else {
			rerr = err
		}
	case KindReplicaSet:
		if rs, err := client.ExtensionsV1beta1().ReplicaSets(nameSpace).Get(name, option); err == nil {
			return rs.Spec.Template.Spec.SchedulerName, nil
		} else {
			rerr = err
		}
	}

	return "", rerr
}

func checkSchedulerName(client *client.Clientset, kind, nameSpace, name, expectedScheduler string) (bool, error) {
	currentName, err := getSchedulerName(client, kind, nameSpace, name)
	if err != nil {
		return false, err
	}

	if currentName == expectedScheduler {
		return true, nil
	}

	return false, nil
}

//update the schedulerName of a ReplicaSet to <schedulerName>
// return the previous schedulerName
func updateRSscheduler(client *client.Clientset, nameSpace, rsName, schedulerName string) (string, error) {
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
func updateRCscheduler(client *client.Clientset, nameSpace, rcName, schedulerName string) (string, error) {
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
		glog.Warning(err.Error())
		return currentName, err
	}

	return currentName, nil
}

func retryDuring(attempts int, timeout time.Duration, sleep time.Duration, myfunc func() error) error {
	t0 := time.Now()

	var err error
	for i := 0; ; i ++ {
		if err = myfunc(); err == nil {
			glog.V(2).Infof("[retry-%d/%d] success", i+1, attempts)
			return nil
		}

		glog.V(2).Infof("[retry-%d/%d] %s", i+1, attempts, err.Error())
		if i >= (attempts-1) {
			break
		}

		if timeout > 1 {
			if delta := time.Now().Sub(t0); delta > timeout {
				err = fmt.Errorf("after %d attepmts (during %d) last error: %s", i, delta, err.Error())
				glog.Error(err.Error())
				return err
			}
		}

		if(sleep > 1) {
			time.Sleep(sleep)
		}
	}

	err = fmt.Errorf("after %d attepmts last error: %s", attempts, err.Error())
	glog.Error(err.Error())
	return err
}

// move pod nameSpace/podName to node nodeName
func movePod(client *client.Clientset, pod *v1.Pod, nodeName string, retryNum int) error {
	podClient := client.CoreV1().Pods(pod.Namespace)
	if podClient == nil {
		err := fmt.Errorf("cannot get Pod client for nameSpace:%v", pod.Namespace)
		glog.Errorf(err.Error())
		return err
	}

	//1. copy the original pod
	id := fmt.Sprintf("%v/%v", pod.Namespace, pod.Name)
	glog.V(2).Infof("move-pod: begin to move %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	npod := &v1.Pod{}
	copyPodInfoX(pod, npod)
	npod.Spec.NodeName = nodeName

	//2. kill original pod
	var grace int64 = 10
	delOption := &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	err := podClient.Delete(pod.Name, delOption)
	if err != nil {
		err = fmt.Errorf("move-failed: failed to delete original pod-%v: %v",
			id, err.Error())
		glog.Error(err.Error())
		return err
	}

	//3. create (and bind) the new Pod
	//glog.V(2).Infof("sleep 10 seconds to test the behaivor of quicker ReplicationController")
	time.Sleep(time.Duration(grace) * time.Second) // this line is for experiments
	du := time.Duration(grace) * time.Second
	err = retryDuring(retryNum, du*time.Duration(retryNum), (du - 2), func() error {
		_, inerr := podClient.Create(npod)
		return inerr
	})
	if err != nil {
		err = fmt.Errorf("move-failed: failed to create new pod-%v: %v",
			id, err.Error())
		glog.Error(err.Error())
		return err
	}

	glog.V(2).Infof("move-finished: %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	return nil
}

func checkPodMoveHealth(client *client.Clientset, nameSpace, podName, nodeName string) error {
	podClient := client.CoreV1().Pods(nameSpace)

	id := fmt.Sprintf("%v/%v", nameSpace, podName)

	getOption := metav1.GetOptions{}
	pod, err := podClient.Get(podName, getOption)
	if err != nil {
		err = fmt.Errorf("failed ot get Pod-%v: %v", id, err.Error())
		glog.Error(err.Error())
		return err
	}

	if pod.Status.Phase != v1.PodRunning {
		err = fmt.Errorf("pod-%v is not running: %v", id, pod.Status.Phase)
		glog.Error(err.Error())
		return err
	}

	if pod.Spec.NodeName != nodeName {
		err = fmt.Errorf("pod-%v is running on another Node (%v Vs. %v)",
			id, pod.Spec.NodeName, nodeName)
		glog.Error(err.Error())
		return err
	}

	return nil
}

//clean the Pods created by Controller during move
func cleanPendingPod(client *client.Clientset, nameSpace, schedulerName, parentKind, parentName string) error {

	podClient := client.CoreV1().Pods(nameSpace)

	option := metav1.ListOptions{
		FieldSelector: "status.phase=" + string(v1.PodPending),
	}

	pods, err := podClient.List(option);
	if err != nil {
		glog.Error("failed to cleanPendingPod: %s", err.Error())
		return err
	}

	var grace int64 = 0
	delOption := &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	for i := range pods.Items {
		pod := &(pods.Items[i])

		if pod.Spec.SchedulerName != schedulerName {
			continue
		}

		kind, pname, err1 := getParentInfo(pod);
		if err1 != nil || pname == "" {
			continue
		}

		//clean all the pending Pod, not only for this operation.
		if parentKind != kind {//&& parentName != pname {
			continue
		}

		glog.V(3).Infof("Begin to delete Pending pod:%s/%s", nameSpace, pod.Name)
		err2 := podClient.Delete(pod.Name, delOption)
		if err2 != nil {
			glog.Error("failed ot delete pending pod:%s/%s: %s", nameSpace, pod.Name, err2.Error())
			err = fmt.Errorf("%s; %s", err.Error(), err2.Error())
		}
	}

	return err
}