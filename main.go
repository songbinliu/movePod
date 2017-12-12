package main

import (
	"flag"
	"fmt"
	"github.com/golang/glog"
	mvUtil "movePod/util"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

//global variables
var (
	masterUrl            string
	kubeConfig           string
	nameSpace            string
	podName              string
	noexistSchedulerName string
	nodeName             string
	k8sVersion           string
)

const (
	// a non-exist scheduler: make sure the pods won't be scheduled by default-scheduler during our moving
	DefaultNoneExistSchedulerName = "turbo-none-exist-scheduler"
	defaultRetryLess                    = 2
	highK8sVersion = "1.6"
)

func setFlags() {
	flag.StringVar(&masterUrl, "masterUrl", "", "master url")
	flag.StringVar(&kubeConfig, "kubeConfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&nameSpace, "nameSpace", "default", "kubernetes object namespace")
	flag.StringVar(&podName, "podName", "myschedule-cpu-80", "the podName to be handled")
	flag.StringVar(&noexistSchedulerName, "scheduler-name", DefaultNoneExistSchedulerName, "the name of the none-exist-scheduler")
	flag.StringVar(&nodeName, "nodeName", "", "Destination of move")
	flag.StringVar(&k8sVersion, "k8sVersion", "1.6", "the version of Kubenetes cluster, candidates are 1.5 | 1.6")

	flag.Set("alsologtostderr", "true")
	flag.Parse()
}

func addErrors(prefix string, err1, err2 error) error {
	rerr := fmt.Errorf("%v ", prefix)
	if err1 != nil {
		rerr = fmt.Errorf("%v %v", rerr.Error(), err1.Error())
	}

	if err2 != nil {
		rerr = fmt.Errorf("%v %v", rerr.Error(), err2.Error())
	}

	glog.Errorf("check update faild:%v", rerr.Error())
	return rerr
}

// update the parent's scheduler before moving pod; then restore parent's scheduler
func doSchedulerMove(client *kubernetes.Clientset, pod *v1.Pod, parentKind, parentName, nodeName string) error {
	highver := true
	if mvUtil.CompareVersion(k8sVersion, highK8sVersion) < 0 {
		highver = false
	}

	noexist := noexistSchedulerName
	helper, err := mvUtil.NewMoveHelper(client, pod.Namespace, pod.Name, parentKind, parentName, noexist, highver)
	if err != nil {
		glog.Errorf("move failed: %v", err)
		return err
	}

	//1. invalid the original scheduler
	preScheduler, err := helper.UpdateScheduler(noexist, defaultRetryLess)
	if err != nil {
		glog.Errorf("move failed: %v", err)
		return err
	}
	helper.SetScheduler(preScheduler)
	defer func() {
		helper.CleanUp()
		mvUtil.CleanPendingPod(client, pod.Namespace, noexist, parentKind, parentName, highver)
	}()

	//Is this necessary?
	if flag, err := helper.CheckScheduler(noexist, 1); err != nil || !flag {
		glog.Errorf("move failed: failed to check scheduler.")
		return fmt.Errorf("failed to check scheduler.")
	}

	//2. do the move
	return mvUtil.MovePod(client, pod, nodeName, defaultRetryLess)
}

func movePod(client *kubernetes.Clientset, nameSpace, podName, nodeName string) error {
	podClient := client.CoreV1().Pods(nameSpace)
	id := fmt.Sprintf("%v/%v", nameSpace, podName)

	//1. get original Pod
	getOption := metav1.GetOptions{}
	pod, err := podClient.Get(podName, getOption)
	if err != nil {
		err = fmt.Errorf("move-aborted: get original pod:%v\n%v", id, err.Error())
		glog.Error(err.Error())
		return err
	}

	if pod.Spec.NodeName == nodeName {
		err = fmt.Errorf("move-aborted: pod %v is already on node: %v", id, nodeName)
		glog.Error(err.Error())
		return err
	}

	glog.V(2).Infof("move-pod: begin to move %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	//2. invalidate the schedulerName of parent controller
	parentKind, parentName, err := mvUtil.ParseParentInfo(pod)
	if err != nil {
		return fmt.Errorf("move-abort: cannot get pod-%v parent info: %v", id, err.Error())
	}

	//2.1 if pod is barely standalone pod, move it directly
	if parentKind == "" {
		return mvUtil.MovePod(client, pod, nodeName, defaultRetryLess)
	}

	//2.2 if pod controlled by ReplicationController/ReplicaSet, then need to do more
	return doSchedulerMove(client, pod, parentKind, parentName, nodeName)
}

func main() {
	setFlags()
	defer glog.Flush()

	kubeClient := mvUtil.GetKubeClient(masterUrl, kubeConfig)
	if kubeClient == nil {
		glog.Errorf("failed to get a k8s client for masterUrl=[%v], kubeConfig=[%v]", masterUrl, kubeConfig)
		return
	}

	if nodeName == "" {
		glog.Errorf("nodeName should not be empty.")
		return
	}

	if err := movePod(kubeClient, nameSpace, podName, nodeName); err != nil {
		glog.Errorf("move pod failed: %v/%v, %v", nameSpace, podName, err.Error())
		return
	}

	glog.V(2).Infof("sleep 10 seconds to check the final state")
	time.Sleep(time.Second * 10)
	if err := mvUtil.CheckPodMoveHealth(kubeClient, nameSpace, podName, nodeName); err != nil {
		glog.Errorf("move pod failed: %v", err.Error())
		return
	}

	glog.V(2).Infof("move pod(%v/%v) to node-%v successfully", nameSpace, podName, nodeName)
}
