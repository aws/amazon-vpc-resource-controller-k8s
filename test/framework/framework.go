// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package framework

import (
	eniConfig "github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	sgp "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	ec2Manager "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/controller"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/deployment"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/jobs"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/namespace"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/pod"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/service"
	sgpManager "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Framework struct {
	Options           Options
	K8sClient         client.Client
	ec2Client         *ec2.EC2
	DeploymentManager deployment.Manager
	PodManager        pod.Manager
	EC2Manager        ec2Manager.Manager
	NSManager         namespace.Manager
	SGPManager        sgpManager.Manager
	SVCManager        service.Manager
	JobManager        jobs.Manager
	NodeManager       node.Manager
	ControllerManager controller.Manager
}

func New(options Options) *Framework {
	err := options.Validate()
	Expect(err).NotTo(HaveOccurred())

	config, err := clientcmd.BuildConfigFromFlags("", options.KubeConfig)
	Expect(err).NotTo(HaveOccurred())

	config.QPS = 20
	config.Burst = 50

	k8sSchema := runtime.NewScheme()
	clientgoscheme.AddToScheme(k8sSchema)
	sgp.AddToScheme(k8sSchema)
	eniConfig.AddToScheme(k8sSchema)

	stopChan := ctrl.SetupSignalHandler()
	cache, err := cache.New(config, cache.Options{Scheme: k8sSchema})
	go func() {
		cache.Start(stopChan)
	}()
	cache.WaitForCacheSync(stopChan)

	realClient, err := client.New(config, client.Options{Scheme: k8sSchema})
	Expect(err).NotTo(HaveOccurred())
	k8sClient := client.DelegatingClient{
		Reader: &client.DelegatingReader{
			CacheReader:  cache,
			ClientReader: realClient,
		},
		Writer:       realClient,
		StatusClient: realClient,
	}

	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(options.AWSRegion),
	}))
	ec2 := ec2.New(sess, &aws.Config{Region: aws.String(options.AWSRegion)})

	return &Framework{
		K8sClient:         k8sClient,
		ec2Client:         ec2,
		PodManager:        pod.NewManager(k8sClient),
		DeploymentManager: deployment.NewManager(k8sClient),
		EC2Manager:        ec2Manager.NewManager(ec2, options.AWSVPCID),
		NSManager:         namespace.NewManager(k8sClient),
		SGPManager:        sgpManager.NewManager(k8sClient),
		SVCManager:        service.NewManager(k8sClient),
		JobManager:        jobs.NewManager(k8sClient),
		NodeManager:       node.NewManager(k8sClient),
		ControllerManager: controller.NewManager(k8sClient),
	}
}
