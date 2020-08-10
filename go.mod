module github.com/aws/amazon-vpc-resource-controller-k8s

go 1.13

require (
	github.com/aws/amazon-vpc-cni-k8s v1.6.3
	github.com/aws/aws-sdk-go v1.31.4
	github.com/go-logr/logr v0.1.0
	github.com/golang/mock v1.4.1
	github.com/google/go-cmp v0.3.0
	github.com/jmespath/go-jmespath v0.3.0 // indirect
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.0.0
	github.com/prometheus/common v0.4.1
	github.com/stretchr/testify v1.5.1
	go.uber.org/zap v1.13.0
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
	sigs.k8s.io/controller-runtime v0.5.0
)

replace github.com/aws/aws-sdk-go => ./internal/aws-sdk-go
