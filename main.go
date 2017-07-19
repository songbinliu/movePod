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
	masterUrl            *string
	kubeConfig           *string
	nameSpace            string
	podName              string
	noexistSchedulerName string
	nodeName             string
	k8sVersion           string
)

const (
	// a non-exist scheduler: make sure the pods won't be scheduled by default-scheduler during our moving
	DefaultNoneExistSchedulerName = "turbo-none-exist-scheduler"
	KindReplicationController     = "ReplicationController"
	KindReplicaSet                = "ReplicaSet"
)

func setFlags() {
	masterUrl = flag.String("masterUrl", "", "master url")
	kubeConfig = flag.String("kubeConfig", "", "absolute path to the kubeconfig file")
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
	id := fmt.Sprintf("%v/%v", pod.Namespace, pod.Name)
	//2. update the schedulerName
	var update func(*kubernetes.Clientset, string, string, string, int) (string, error)
	switch parentKind {
	case KindReplicationController:
		glog.V(3).Infof("pod-%v parent is a ReplicationController-%v", id, parentName)
		update = updateRCscheduler
	case KindReplicaSet:
		glog.V(2).Infof("pod-%v parent is a ReplicaSet-%v", id, parentName)
		update = updateRSscheduler
	default:
		err := fmt.Errorf("unsupported parent-[%v] Kind-[%v]", parentName, parentKind)
		glog.Warning(err.Error())
		return err
	}

	noexist := noexistSchedulerName
	check := checkSchedulerName
	nameSpace := pod.Namespace

	preScheduler, err := update(client, nameSpace, parentName, noexist, 1)
	if flag, err2 := check(client, parentKind, nameSpace, parentName, noexist); !flag {
		prefix := fmt.Sprintf("move-failed: pod-[%v], parent-[%v]", id, parentName)
		return addErrors(prefix, err, err2)
	}

	restore := func() {
		//check it again in case somebody has changed it back.
		if flag, _ := check(client, parentKind, nameSpace, parentName, noexist); flag {
			update(client, nameSpace, parentName, preScheduler, DefaultRetryMore)
		}

		err := cleanPendingPod(client, nameSpace, noexist, parentKind, parentName)
		if err != nil {
			glog.Error("failed to clean pending pod for MoveAction:%v", err.Error())
		}
	}
	defer restore()

	//3. movePod
	return  movePod(client, pod, nodeName, DefaultRetryLess)
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
	parentKind, parentName, err := getParentInfo(pod)
	if err != nil {
		return fmt.Errorf("move-abort: cannot get pod-%v parent info: %v", id, err.Error())
	}

	//2.1 if pod is barely standalone pod, move it directly
	if parentKind == "" {
		return movePod(client, pod, nodeName, DefaultRetryLess)
	}

	//2.2 if pod controlled by ReplicationController/ReplicaSet, then need to do more
	if k8sVersion == "1.5" {
		return doSchedulerMove15(client, pod, parentKind, parentName, nodeName)
	} else {
		return doSchedulerMove(client, pod, parentKind, parentName, nodeName)
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

	if nodeName == "" {
		glog.Errorf("nodeName should not be empty.")
		return
	}

	if err := MovePod(kubeClient, nameSpace, podName, nodeName); err != nil {
		glog.Errorf("move pod failed: %v/%v, %v", nameSpace, podName, err.Error())
		return
	}

	glog.V(2).Infof("sleep 10 seconds to check the final state")
	time.Sleep(time.Second * 10)
	if err := checkPodMoveHealth(kubeClient, nameSpace, podName, nodeName); err != nil {
		glog.Errorf("move pod failed: %v", err.Error())
		return
	}

	glog.V(2).Infof("move pod(%v/%v) to node-%v successfully", nameSpace, podName, nodeName)
}
