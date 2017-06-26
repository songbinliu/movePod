package main

import (
	"fmt"
	//"time"
	"encoding/json"

	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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

func listPod(client *kubernetes.Clientset) {
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

func getParentInfo(pod *v1.Pod) (string, string, error){
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

			jsonstr := []byte(value)
			if err := json.Unmarshal(jsonstr, ref); err != nil {
				err = fmt.Errorf("failed to decode parent annoation:%v", err.Error())
				return "", "", err
			}

			return ref.Reference.Kind, ref.Reference.Name, nil
		}
	}

	glog.V(3).Infof("cannot find pod-%v/%v parent by Annotations.", pod.Namespace, pod.Name)

	return "", "", nil
}

func getKubeClient(masterUrl, kubeConfig *string) *kubernetes.Clientset {
	if *masterUrl == "" && *kubeConfig == "" {
		fmt.Println("must specify masterUrl or kubeConfig.")
		return nil
	}

	var err error
	var config *restclient.Config

	if *kubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeConfig)
	} else {
		config, err = clientcmd.BuildConfigFromFlags(*masterUrl, "")
	}

	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	return clientset
}

//update the schedulerName of a ReplicaSet
// return the previous schedulerName
func updateRSscheduler(client *kubernetes.Clientset, nameSpace, rsName, schedulerName string) (string, error) {
	preSchedulerName := ""
	if schedulerName == "" {
		return "", fmt.Errorf("update failed: schedulerName is empty")
	}

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
		return preSchedulerName, err
	}

	preSchedulerName = rs.Spec.Template.Spec.SchedulerName
	glog.V(3).Infof("ReplicationController-%v:%v, replicaNum:%v\n",
		id,
		rs.Spec.Template.Spec.SchedulerName,
		*rs.Spec.Replicas)

	//2. update schedulerName
	rs.Spec.Template.Spec.SchedulerName = schedulerName
	_, err = rsClient.Update(rs)
	if err != nil {
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return preSchedulerName, err
	}

	//3. check it
	rs, err = rsClient.Get(rsName, option)
	if err != nil {
		err = fmt.Errorf("failed to check ReplicaSet-%v: %v", id, err.Error())
		return preSchedulerName, err
	}

	if rs.Spec.Template.Spec.SchedulerName != schedulerName {
		err = fmt.Errorf("failed to update schedulerName for ReplicaSet-%v: %v", id, err.Error())
		glog.Error(err.Error())
		return "", err
	}

	glog.V(2).Infof("Successfully update ReplicationController:%v scheduler name from [%v] to [%v]",
		id,
		preSchedulerName,
		schedulerName)

	return preSchedulerName, nil
}

//update the schedulerName of a ReplicationController
// return the previous schedulerName
func updateRCscheduler(client *kubernetes.Clientset, nameSpace, rcName, schedulerName string) (string, error) {
	preSchedulerName := ""
	if schedulerName == "" {
		return "", fmt.Errorf("update failed: schedulerName is empty")
	}

	id := fmt.Sprintf("%v/%v", nameSpace,rcName)
	rcClient := client.CoreV1().ReplicationControllers(nameSpace)

	//1. get
	option := metav1.GetOptions{}
	rc, err := rcClient.Get(rcName, option)
	if err != nil {
		err = fmt.Errorf("failed to get ReplicationController-%v: %v\n", id, err.Error())
		glog.Error(err.Error())
		return preSchedulerName, err
	}

	preSchedulerName = rc.Spec.Template.Spec.SchedulerName

	glog.V(3).Infof("ReplicationController-%v:%v, replicaNum:%v\n",
		id,
		rc.Spec.Template.Spec.SchedulerName,
		*rc.Spec.Replicas)

	//2. update
	rc.Spec.Template.Spec.SchedulerName = schedulerName
	rc, err = rcClient.Update(rc)
	if err != nil {
		err = fmt.Errorf("failed to update RC-%v:%v\n", id, err.Error())
		glog.Error(err.Error())
		return preSchedulerName, err
	}

	//3. check it
	rc, err = rcClient.Get(rcName, option)
	if err != nil {
		err = fmt.Errorf("failed to get ReplicationController-%v: %v\n", id, err.Error())
		glog.Error(err.Error())
		return preSchedulerName, err
	}

	if rc.Spec.Template.Spec.SchedulerName != schedulerName {
		err = fmt.Errorf("failed to update schedulerName for ReplicaController-%v: %v", id, err.Error())
		glog.Error(err.Error())
		return "", err
	}

	glog.V(2).Infof("Successfully update ReplicationController:%v scheduler name from [%v] to [%v]",
		id,
		preSchedulerName,
		schedulerName)

	return preSchedulerName, nil
}

// move pod nameSpace/podName to node nodeName
func movePod(client *kubernetes.Clientset, pod *v1.Pod, nodeName string) error {
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
	var grace int64 = 0
	delOption := &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	err := podClient.Delete(pod.Name, delOption)
	if err != nil {
		err = fmt.Errorf("move-failed: failed to delete original pod-%v: %v",
			id, err.Error())
		glog.Error(err.Error())
		return err
	}

	//3. create (and bind) the new Pod
	// time.Sleep(time.Second * 10) // this line is for experiments
	_, err = podClient.Create(npod)
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
