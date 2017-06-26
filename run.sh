#!/bin/bash
set -x

k8sconf="configs/aws.kubeconfig.yaml"

nameSpace="default"
podName=mem-deployment-4234284026-m0j41
slave1="ip-172-23-1-92.us-west-2.compute.internal"
slave2="ip-172-23-1-12.us-west-2.compute.internal"
nodeName=$slave1

options="$options --kubeConfig $k8sconf "
options="$options --v 3 "
options="$options --nameSpace $nameSpace"
options="$options --podName $podName "
options="$options --nodeName $nodeName "

./movePod $options
