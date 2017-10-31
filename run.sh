#!/bin/bash
set -x

k8sconf="configs/aws.kubeconfig.yaml"
#k8sconf="configs/en119.kubeconfig.yaml"

nameSpace="default"
#podName="cpu-5-1,limit-mem-256-cpu-20,limit.mem-256-cpu-20,memory-256-2,memory-333,saturated.memory-256-2,under-memory-1024"
#podName="mem-600-group-b3kxg,mem-600-group-csvcs,mem-600-group-wqlm9,turbo-cpu-40"
podName="curl"
master="ip-172-23-1-39.us-west-2.compute.internal"
slave1="ip-172-23-1-92.us-west-2.compute.internal"
slave2="ip-172-23-1-12.us-west-2.compute.internal"
nodeName=$slave1

options="$options --kubeConfig $k8sconf "
options="$options --v 3 "
options="$options --nameSpace $nameSpace"
options="$options --podName $podName "
options="$options --nodeName $nodeName "

#./good.movePod $options
./movePod $options
