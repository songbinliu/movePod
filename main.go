package main

import (
	"flag"
	"fmt"
	"github.com/golang/glog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
)

//global variables
var (
	masterUrl  *string
	kubeConfig *string
	nameSpace  *string
	podName    *string
	rcName     *string
	//schedulerName *string
	noexistSchedulerName *string
	uuid                 *string
	nodeName             *string
)

const (
	// a non-exist scheduler: make sure the pods won't be scheduled by default-scheduler during our moving
	DefaultNoneExistSchedulerName = "turbo-none-exist-scheduler"
)

func setFlags() {
	masterUrl = flag.String("masterUrl", "", "master url")
	kubeConfig = flag.String("kubeConfig", "", "absolute path to the kubeconfig file")
	nameSpace = flag.String("nameSpace", "default", "kubernetes object namespace")
	podName = flag.String("podName", "myschedule-cpu-80", "the podName to be handled")
	noexistSchedulerName = flag.String("scheduler-name", DefaultNoneExistSchedulerName, "the name of the none-exist-scheduler")
	nodeName = flag.String("nodeName", "", "Destination of move")

	flag.Set("alsologtostderr", "true")
	flag.Parse()
}

func MovePod(client *kubernetes.Clientset, nameSpace, podName, nodeName string) error {
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
	var f func(*kubernetes.Clientset, string, string, string) (string, error)

	parentKind, parentName, err := getParentInfo(pod)
	switch parentKind {
	case "":
		glog.V(3).Infof("pod-%v is a standalone Pod, move it directly.", id)
		f = func(c *kubernetes.Clientset, ns, pname, sname string) (string, error) { return "", nil }
	case "ReplicationController":
		glog.V(3).Infof("pod-%v parent is a ReplicationController-%v", id, parentName)
		f = updateRCscheduler
	case "ReplicaSet":
		glog.V(2).Infof("pod-%v parent is a ReplicaSet-%v", id, parentName)
		f = updateRSscheduler
	default:
		err = fmt.Errorf("unsupported parent-[%v] Kind-[%v]", parentName, parentKind)
		glog.Warning(err.Error())
		return err
	}

	preScheduler, err := f(client, nameSpace, parentName, *noexistSchedulerName)
	if err != nil {
		err = fmt.Errorf("move-failed: update pod-%v parent-%v scheduler failed:%v", id, parentName, err.Error())
		glog.Error(err.Error())
		return err
	}

	defer f(client, nameSpace, parentName, preScheduler)

	//3. movePod
	err = movePod(client, nameSpace, podName, nodeName)
	if err != nil {
		return err
	}

	return nil
}

func checkPodMoveHealth(client *kubernetes.Clientset, nameSpace, podName, nodeName string) error {
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

func main() {
	setFlags()
	defer glog.Flush()

	kubeClient := getKubeClient(masterUrl, kubeConfig)
	if kubeClient == nil {
		glog.Errorf("failed to get a k8s client for masterUrl=[%v], kubeConfig=[%v]", *masterUrl, *kubeConfig)
		return
	}

	if *nodeName == "" {
		glog.Errorf("nodeName should not be empty.")
		return
	}

	if err := MovePod(kubeClient, *nameSpace, *podName, *nodeName); err != nil {
		glog.Errorf("move pod failed: %v/%v, %v", *nameSpace, *podName, err.Error())
		return
	}

	time.Sleep(time.Second * 10)
	if err := checkPodMoveHealth(kubeClient, *nameSpace, *podName, *nodeName); err != nil {
		glog.Errorf("move pod failed: %v", err.Error())
		return
	}

	glog.V(2).Infof("move pod(%v/%v) to node-%v successfully", *nameSpace, *podName, *nodeName)
}
