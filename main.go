package main

import (
	"flag"
	"fmt"
	"strings"
	"time"
	"sync"
	"github.com/golang/glog"
	mvUtil "movePod/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
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
	highK8sVersion = "1.6"
	defaultRetryLess                    = 2
	defaultSleep = time.Second * 10
	defaultWaitLockTimeOut = time.Second * 100

	defaultTTL = time.Second * 10
)

//global var
var (
	lockMap *mvUtil.ExpirationMap
)

func setFlags() {
	flag.StringVar(&masterUrl, "masterUrl", "", "master url")
	flag.StringVar(&kubeConfig, "kubeConfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&nameSpace, "nameSpace", "default", "kubernetes object namespace")
	flag.StringVar(&podName, "podName", "myschedule-cpu-80", "the podNames to be handled, split by ','")
	flag.StringVar(&noexistSchedulerName, "scheduler-name", DefaultNoneExistSchedulerName, "the name of the none-exist-scheduler")
	flag.StringVar(&nodeName, "nodeName", "", "Destination of move")
	flag.StringVar(&k8sVersion, "k8sVersion", "1.6", "the version of Kubenetes cluster, candidates are 1.5 | 1.6")

	flag.Set("alsologtostderr", "true")
	flag.Parse()
}

// update the parent's scheduler before moving pod; then restore parent's scheduler
func doSchedulerMove(client *kclient.Clientset, pod *v1.Pod, parentKind, parentName, nodeName string) (*v1.Pod, error) {
	highver := true
	if mvUtil.CompareVersion(k8sVersion, highK8sVersion) < 0 {
		highver = false
	}

	noexist := noexistSchedulerName
	helper, err := mvUtil.NewMoveHelper(client, pod.Namespace, pod.Name, parentKind, parentName, noexist, highver)
	if err != nil {
		glog.Errorf("move failed: %v", err)
		return nil, err
	}

	//1. invalid the original scheduler
	preScheduler, err := helper.UpdateScheduler(noexist, defaultRetryLess)
	if err != nil {
		glog.Errorf("move failed: %v", err)
		return nil, err
	}
	helper.SetScheduler(preScheduler)
	defer func() {
		helper.CleanUp()
		mvUtil.CleanPendingPod(client, pod.Namespace, noexist, parentKind, parentName, highver)
	}()

	//Is this necessary?
	if flag, err := helper.CheckScheduler(noexist, 1); err != nil || !flag {
		glog.Errorf("move failed: failed to check scheduler.")
		return nil, fmt.Errorf("failed to check scheduler.")
	}

	//2. do the move
	return mvUtil.MovePod(client, pod, nodeName, defaultRetryLess)
}


// move the pods controlled by ReplicationController/ReplicaSet with concurrent control
func doSchedulerMove2(client *kclient.Clientset, pod *v1.Pod, parentKind, parentName, nodeName string) (*v1.Pod, error) {
	highver := true
	if mvUtil.CompareVersion(k8sVersion, highK8sVersion) < 0 {
		highver = false
	}

	//1. set up
	noexist := noexistSchedulerName
	helper, err := mvUtil.NewMoveHelper2(client, pod.Namespace, pod.Name, parentKind, parentName, noexist, highver)
	if err != nil {
		return nil, err
	}
	helper.SetMap(lockMap)

	//2. wait to get a lock
	err = mvUtil.RetryDuring(1000, defaultWaitLockTimeOut, defaultSleep*5, func() error {
		if !helper.Acquirelock() {
			glog.V(2).Infof("failed to get lock for pod[%s], parent[%s]", pod.Name, parentName)
			return fmt.Errorf("TryLater")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		helper.CleanUp()
		mvUtil.CleanPendingPod(client, pod.Namespace, noexist, parentKind, parentName, highver)
	}()
	glog.V(3).Infof("Got lock for pod[%s] from parent[%s]", pod.Name, parentName)

	//3. invalidate the scheduler of the parentController
	preScheduler, err := helper.UpdateScheduler(noexist, 1)
	if err != nil {
		glog.Errorf("failed to invalidate schedulerName when moving pod: %s/%s", pod.Namespace, pod.Name)
		return nil, fmt.Errorf("Failed")
	}

	//4. set the original scheduler for restore
	helper.SetScheduler(preScheduler)

	return mvUtil.MovePod(client, pod, nodeName, defaultRetryLess)
}

func movePod(client *kclient.Clientset, nameSpace, podName, nodeName string) (*v1.Pod, error) {
	podClient := client.CoreV1().Pods(nameSpace)
	id := fmt.Sprintf("%v/%v", nameSpace, podName)

	//1. get original Pod
	getOption := metav1.GetOptions{}
	pod, err := podClient.Get(podName, getOption)
	if err != nil {
		err = fmt.Errorf("move-aborted: get original pod:%v\n%v", id, err.Error())
		glog.Error(err.Error())
		return nil, err
	}

	if pod.Spec.NodeName == nodeName {
		err = fmt.Errorf("move-aborted: pod %v is already on node: %v", id, nodeName)
		glog.Error(err.Error())
		return nil, err
	}

	glog.V(2).Infof("move-pod: begin to move %v from %v to %v",
		id, pod.Spec.NodeName, nodeName)

	//2. invalidate the schedulerName of parent controller
	parentKind, parentName, err := mvUtil.ParseParentInfo(pod)
	if err != nil {
		return nil, fmt.Errorf("move-abort: cannot get pod-%v parent info: %v", id, err.Error())
	}

	//2.1 if pod is barely standalone pod, move it directly
	if parentKind == "" {
		return mvUtil.MovePod(client, pod, nodeName, defaultRetryLess)
	}

	//2.2 if pod controlled by ReplicationController/ReplicaSet, then need to do more
	return doSchedulerMove2(client, pod, parentKind, parentName, nodeName)
}

func movePods(client *kclient.Clientset, nameSpace, podNames, nodeName string) error {
	names := strings.Split(podNames, ",")
	var wg sync.WaitGroup

	for _, pname := range names {
		podName := strings.TrimSpace(pname)
		if len(podName) == 0 {
			continue
		}
		wg.Add(1)

		go func() {
			defer wg.Done()
			if _, err := movePod(client, nameSpace, podName, nodeName); err != nil {
				glog.Errorf("move pod[%s] failed: %v", podName, err)
				return
			}

			glog.V(2).Infof("sleep 10 seconds to check the final state")
			time.Sleep(time.Second * 10)
			if err := mvUtil.CheckPodMoveHealth(client, nameSpace, podName, nodeName); err != nil {
				glog.Errorf("move pod[%s] failed: %v", podName, err)
				return
			}
			glog.V(2).Infof("move pod(%v/%v) to node-%v successfully", nameSpace, podName, nodeName)
		}()
	}

	wg.Wait()
	return nil
}

func main() {
	setFlags()
	defer glog.Flush()

	lockMap = mvUtil.NewExpirationMap(defaultTTL)
	stop := make(chan struct{})
	go lockMap.Run(stop)
	defer close(stop)

	kubeClient := mvUtil.GetKubeClient(masterUrl, kubeConfig)
	if kubeClient == nil {
		glog.Errorf("failed to get a k8s client for masterUrl=[%v], kubeConfig=[%v]", masterUrl, kubeConfig)
		return
	}

	if nodeName == "" {
		glog.Errorf("nodeName should not be empty.")
		return
	}

	if err := movePods(kubeClient, nameSpace, podName, nodeName); err != nil {
		glog.Errorf("move pod failed: %v/%v, %v", nameSpace, podName, err.Error())
		return
	}


}
