# package aws mocks
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_wrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2Wrapper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_metadata.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2MetadataClient
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/mock_ec2_apihelper.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api EC2APIHelper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/mock_instance.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2 EC2Instance

mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/worker/mock_worker.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker Worker
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/handler/mock_handler.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler Handler