# movePod #
This project demonstrates a method that can move pods, which either are created by ReplicationController, or by ReplicaSet(which is created by Deployment).

# Method #
This method first set the schedulerName of the parent object (ReplicationController, or ReplicaSet) of the pod to a **invalidate scheduler**; then second, move the pod by **Copy-Delete-Create** steps, and uses the **Binding-on-Creation** way 
when to create the new the Pod. And third, restore the schedulerName of the parent object.

# why it works #

