alias mockgen='mockgen -copyright_file=templates/copyright.txt'
# package aws mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_wrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2Wrapper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_apihelper.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2APIHelper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/mock_instance.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2 EC2Instance
# package k8s mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/mock_k8swrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s K8sWrapper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/pod/mock_podapiwrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod PodClientAPIWrapper
# package worker mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/worker/mock_worker.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker Worker
# package handler mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/handler/mock_handler.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler Handler
# package provider mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/provider/mock_provider.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider ResourceProvider
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/trunk/mock_trunk.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk TrunkENI
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/provider/ip/eni/mock_eni.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/ip/eni ENIManager
# package node mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager/mock_manager.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager Manager
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/node/mock_node.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node Node
# package utils mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/utils/mock_k8shelper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils SecurityGroupForPodsAPI
# package pool mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/pool/mock_pool.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool Pool
# package resource mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/resource/mock_resources.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource ResourceManager
# package condition mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/condition/mock_condition.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition Conditions
