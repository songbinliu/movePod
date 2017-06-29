# movePod #
This project demonstrates a method that can move pods, which either are created by ReplicationController, or by ReplicaSet(which may be created by Deployment).

# Method #
**1.** set the schedulerName of the parent object (ReplicationController, or ReplicaSet) of the pod to a **invalid scheduler**; 

**2.** move the pod by [**Copy-Delete-Create**](https://github.com/songbinliu/movePod/blob/master/util.go#L284) steps, and uses the **Binding-on-Creation** way by assigning [pod.Spec.NodeName](https://github.com/kubernetes/client-go/blob/master/pkg/api/v1/types.go#L2470) (alternatively, Binding-on-Creation is not necessary, but it can save a Bind() API call.)
when to create the new the Pod. 

**3.** restore the schedulerName of the parent object.

It should be noted that, if the pod has no parent object, then only the second step is necessary.

# How it works #

It is difficult to move a Pod controlled by ReplicationController/ReplicaSet, because in the second step of the [**Copy-Delete-Create**] move operation, the ReplicationController/ReplicaSet will create a new Pod immediately to make sure there is enough number of Running replicas. However, ReplicationController/ReplicaSet also amkes sure that there is no more than desired number of Running replicas. So the pod created by our move operation have to compete with the pod created by ReplicationController/ReplicaSet: [the first to get to **running** state will survive (see experiment)](https://gist.github.com/songbinliu/7576bd84bab50f4e399d979d7998cdf6#an-experiment).

If we can make sure that the pod created by ReplicationController/ReplicaSet is scheduled **later than** the pod 
created by our move operation, then our pod will almost alway be quicker to get to **running** state. We achive this by assigning an none-exist scheduler name to the ReplictionController/ReplicaSet before the **Delete** step: which makes sure 
the pod created by ReplicationController/ReplicaSet won't be scheduled. And because our pod don't need to be scheduled, and bind to the new node directly. So our pod will get to the **running** state first. (But if the new node is too slow to run the pod, or failed to run the pod, then our pod will be deleted.)

In the end of the move operation, we restore the scheduler name of the ReplicationController/ReplicaSet, to clear everything.


# Test it #

```console
go build

./movePod --kubeConfig configs/aws.kubeconfig.yaml --v 3 --nameSpace default --podName mem-deployment-4234284026-m0j41 --nodeName ip-172-23-1-12.us-west-2.compute.internal

```


# Other info #
Some [experiments](https://gist.github.com/songbinliu/6b28a15ac718a070ab66cff44f0cc056) about Kubernetes 1.6 [advanced scheduling feature](http://blog.kubernetes.io/2017/03/advanced-scheduling-in-kubernetes.html).
