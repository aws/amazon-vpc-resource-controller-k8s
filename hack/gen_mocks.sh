mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/mock_ec2_wrapper.go github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2 EC2Wrapper
mockgen -destination=../mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/mock_ec2_metadata.go  github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2 EC2MetadataClient
