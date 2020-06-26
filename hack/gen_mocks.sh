# package aws mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_wrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2Wrapper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_metadata.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2MetadataClient
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_apihelper.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2APIHelper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/mock_instance.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2 EC2Instance
# package k8s mcoks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/mock_k8swrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s K8sWrapper
# package worker mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/worker/mock_worker.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker Worker
# package handler mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/handler/mock_handler.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler Handler
# package provider mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/provider/mock_provider.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider ResourceProvider
#package node mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/node/mock_manager.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node Manager
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/node/mock_node.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node Node
