# movePod #
This project demonstrates a method that can move pods, which either are created by ReplicationController, or by ReplicaSet(which may be created by Deployment).

# Method #
**1.** set the schedulerName of the parent object (ReplicationController, or ReplicaSet) of the pod to a **invalid scheduler**; 

**2.** move the pod by [**Copy-Delete-Create**](https://github.com/songbinliu/movePod/blob/master/util.go#L284) steps, and uses the **Binding-on-Creation** way by assigning [pod.Spec.NodeName](https://github.com/kubernetes/client-go/blob/master/pkg/api/v1/types.go#L2470) 
when to create the new the Pod. 

(Note: In addition to Binding-on-Creation, **Create**() + **Bind**() API calls can do the same work.)


**3.** restore the schedulerName of the parent object.
It should be noted that, if the pod has no parent object, then only the second step is necessary.

# How it works #

It is difficult to move a Pod controlled by ReplicationController/ReplicaSet, because in the second step of the [**Copy-Delete-Create**] move operation.

## Problem ##
For pods controlled by ReplicationController/ReplicaSet, when one of them is killed, the ReplicationController/ReplicaSet will 
be get notified, and create a new pod. The ReplicationController/ReplicaSet will create a new Pod immediately to make sure there is enough number of Running replicas. However, ReplicationController/ReplicaSet also makes sure that there is no more than desired number of Running replicas. 

Since we will create a new Pod too, so the ControllerManager will decide to delete one of the two Pods: the pod newly created by ControllerManager, and the pod created by our MoveOperation. How to make sure that the pod created by our MoveOperation will survive?

## ControllerManager ##
According to the code of [ReplicationManager](https://github.com/kubernetes/kubernetes/blob/release-1.7/pkg/controller/replication/replication_controller.go#L498), when ReplicationController decides which Pods are to be deleted, it will sort the Pod of the ReplicationController according [some conditions](https://github.com/kubernetes/kubernetes/blob/release-1.7/pkg/controller/controller_utils.go#L726) of the pods. The first condition is to check whether a Pod is assigned a Node or not. If a Pod is not assigned a Node, then it will be deleted first.
```go
// ActivePods type allows custom sorting of pods so a controller can pick the best ones to delete.
type ActivePods []*v1.Pod

func (s ActivePods) Len() int      { return len(s) }
func (s ActivePods) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s ActivePods) Less(i, j int) bool {
	// 1. Unassigned < assigned
	// If only one of the pods is unassigned, the unassigned one is smaller
	if s[i].Spec.NodeName != s[j].Spec.NodeName && (len(s[i].Spec.NodeName) == 0 || len(s[j].Spec.NodeName) == 0) {
		return len(s[i].Spec.NodeName) == 0
	}
	// 2. PodPending < PodUnknown < PodRunning
	m := map[v1.PodPhase]int{v1.PodPending: 0, v1.PodUnknown: 1, v1.PodRunning: 2}
	if m[s[i].Status.Phase] != m[s[j].Status.Phase] {
		return m[s[i].Status.Phase] < m[s[j].Status.Phase]
	}
	// 3. Not ready < ready
	// If only one of the pods is not ready, the not ready one is smaller
	if podutil.IsPodReady(s[i]) != podutil.IsPodReady(s[j]) {
		return !podutil.IsPodReady(s[i])
	}
	// TODO: take availability into account when we push minReadySeconds information from deployment into pods,
	//       see https://github.com/kubernetes/kubernetes/issues/22065
	// 4. Been ready for empty time < less time < more time
	// If both pods are ready, the latest ready one is smaller
	if podutil.IsPodReady(s[i]) && podutil.IsPodReady(s[j]) && !podReadyTime(s[i]).Equal(podReadyTime(s[j])) {
		return afterOrZero(podReadyTime(s[i]), podReadyTime(s[j]))
	}
	// 5. Pods with containers with higher restart counts < lower restart counts
	if maxContainerRestarts(s[i]) != maxContainerRestarts(s[j]) {
		return maxContainerRestarts(s[i]) > maxContainerRestarts(s[j])
	}
	// 6. Empty creation time pods < newer pods < older pods
	if !s[i].CreationTimestamp.Equal(s[j].CreationTimestamp) {
		return afterOrZero(s[i].CreationTimestamp, s[j].CreationTimestamp)
	}
	return false
}
```

## Solution ##
Before the delete operation, we **first** modify the scheduler of the corresponding ReplicationController/ReplicaSet to a none exist scheduler. The consequece of this modification is that, the Pod created by ControllerManager will be waiting for a none exist scheduler to assign a node, and of course this Pod will not get a node because there is no scheduler to assign a node for it.

**Second**, we create a new pod with the node name assigned. Then when ContollerManager decides to delete a Pod, it will choose the one created by ControllerManager.

**Third**, in the end of the move operation, we restore the scheduler name of the ReplicationController/ReplicaSet, to clear everything.


# Test it #

```console
go build

./movePod --kubeConfig configs/aws.kubeconfig.yaml --v 3 --nameSpace default --podName mem-deployment-4234284026-m0j41 --nodeName ip-172-23-1-12.us-west-2.compute.internal

```


# Other info #
Some [experiments](https://gist.github.com/songbinliu/6b28a15ac718a070ab66cff44f0cc056) about Kubernetes 1.6 [advanced scheduling feature](http://blog.kubernetes.io/2017/03/advanced-scheduling-in-kubernetes.html).
