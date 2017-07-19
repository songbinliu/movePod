#!/bin/bash
set -x

k8sconf="configs/aws.kubeconfig.yaml"
#k8sconf="configs/en119.kubeconfig.yaml"

nameSpace="default"
podName=nginx-deployment-4234284026-0d5tj
slave1="ip-172-23-1-92.us-west-2.compute.internal"
slave2="ip-172-23-1-12.us-west-2.compute.internal"
nodeName=$slave1

options="$options --kubeConfig $k8sconf "
options="$options --v 3 "
options="$options --nameSpace $nameSpace"
options="$options --podName $podName "
options="$options --nodeName $nodeName "

./movePod $options
