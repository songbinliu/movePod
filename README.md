# movePod #
This project demonstrates a method that can move pods, which either are created by ReplicationController, or by ReplicaSet(which is created by Deployment).

# Method #
**1.** set the schedulerName of the parent object (ReplicationController, or ReplicaSet) of the pod to a **invalidate scheduler**; 

**2.** move the pod by [**Copy-Delete-Create**](https://gist.github.com/songbinliu/7576bd84bab50f4e399d979d7998cdf6#move-pod) steps, and uses the **Binding-on-Creation** way 
when to create the new the Pod. 

**3.** restore the schedulerName of the parent object.

It should be noted that, if the pod has no parent object, then only the second step is necessary.

# Why it works #

It is difficult to move a Pod controlled by ReplicationController/ReplicaSet, because in the second step of the [**Copy-Delete-Create**] move operation, the ReplicationController/ReplicaSet will create a new Pod immediately to make sure there is enough number of Running replicas. However, ReplicationController/ReplicaSet also amkes sure that there is no more than desired number of Running replicas. So the pod created by our move operation have to compete with the pod created by ReplicationController/ReplicaSet: [the first to get to **running** state will survive (see experiment)](https://gist.github.com/songbinliu/7576bd84bab50f4e399d979d7998cdf6#an-experiment).

There are some comunication rounds between  *Kubernetes ControllerManger* and *Kubernetes APIServer* (*Kubernetes scheduler* is also involved), than the comunication between *kubeturbo* and *Kubernetes APIServer*.  



