module github.com/aws/amazon-vpc-resource-controller-k8s

go 1.16

require (
	github.com/aws/amazon-vpc-cni-k8s v1.9.0
	github.com/aws/aws-sdk-go v1.40.43
	github.com/go-logr/logr v0.4.0
	github.com/go-logr/zapr v0.4.0
	github.com/golang/mock v1.4.1
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.14.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/stretchr/testify v1.7.0
	go.uber.org/zap v1.18.1
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v0.21.3
	sigs.k8s.io/controller-runtime v0.9.5
)
