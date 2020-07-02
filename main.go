/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	corecontroller "github.com/aws/amazon-vpc-resource-controller-k8s/controllers/core"
	vpcresourcescontroller "github.com/aws/amazon-vpc-resource-controller-k8s/controllers/vpcresources"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch"
	webhookutils "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	webhookcore "github.com/aws/amazon-vpc-resource-controller-k8s/webhook/core"
	// +kubebuilder:scaffold:imports
)

var (
	scheme     = runtime.NewScheme()
	setupLog   = ctrl.Log.WithName("setup")
	syncPeriod = time.Hour
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = corev1.AddToScheme(scheme)
	_ = vpcresourcesv1beta1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	config := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		SyncPeriod:         &syncPeriod,
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "bb6ce178.k8s.aws",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		setupLog.Error(err, "failed to create client set")
		os.Exit(1)
	}

	// creating a cache helper to handle security groups.
	cacheHelper := webhookutils.NewK8sCacheHelper(
		mgr.GetClient(),
		ctrl.Log.WithName("cache helper"))

	// Get the resource providers and handlers
	resourceHandlers, nodeManager := setUpResources(mgr, clientSet, cacheHelper)

	if err = (&corecontroller.PodReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("Pod"),
		Scheme:   mgr.GetScheme(),
		Manager:  nodeManager,
		Handlers: resourceHandlers,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	if err = (&corecontroller.NodeReconciler{
		Client:  mgr.GetClient(),
		Log:     ctrl.Log.WithName("controllers").WithName("Node"),
		Scheme:  mgr.GetScheme(),
		Manager: nodeManager,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Node")
		os.Exit(1)
	}
	if err = (&vpcresourcescontroller.SecurityGroupPolicyReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("SecurityGroupPolicy"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SecurityGroupPolicy")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder
	setupLog.Info("setting up webhook server")
	webhookServer := mgr.GetWebhookServer()

	setupLog.Info("registering webhooks to the webhook server")
	webhookServer.Register("/mutate-v1-pod", &webhook.Admission{Handler: &webhookcore.PodResourceInjector{
		Client:      mgr.GetClient(),
		CacheHelper: cacheHelper,
		Log:         ctrl.Log.WithName("webhook").WithName("Pod Mutating"),
	}})

	// Validating webhook for pod.
	webhookServer.Register("/validate-v1-pod", &webhook.Admission{Handler: &webhookcore.AnnotationValidator{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("webhook").WithName("Annotation Validator"),
	}})

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// setUpResources sets up all resource providers and the node manager
func setUpResources(manager manager.Manager, clientSet *kubernetes.Clientset, cacheHelper *webhookutils.K8sCacheHelper) ([]handler.Handler, node.Manager) {

	var resourceProviders []provider.ResourceProvider

	ec2Wrapper, err := api.NewEC2Wrapper()
	if err != nil {
		setupLog.Error(err, "unable to create ec2 wrapper")
	}

	ec2APIHelper := api.NewEC2APIHelper(ec2Wrapper)
	k8sWrapper := k8s.NewK8sWrapper(manager.GetClient(), clientSet.CoreV1())

	// Load the default resource config
	resourceConfig := config.LoadResourceConfig()

	// Set up on demand handlers
	onDemandProviders := getOnDemandResourceProviders(resourceConfig, k8sWrapper, ec2APIHelper, &resourceProviders, cacheHelper)
	onDemandHandler := handler.NewOnDemandHandler(ctrl.Log.WithName("on demand handler"), onDemandProviders)

	// Set up warm resource handlers

	// Set up the node manager
	nodeManager := node.NewNodeManager(ctrl.Log.WithName("node manager"), resourceProviders, ec2APIHelper)

	return []handler.Handler{onDemandHandler}, nodeManager
}

// getOnDemandResourceProviders returns all the providers for resource type on demand
func getOnDemandResourceProviders(resourceConfig map[string]config.ResourceConfig, k8sWrapper k8s.K8sWrapper,
	ec2APIHelper api.EC2APIHelper, providers *[]provider.ResourceProvider,
	cacheHelper *webhookutils.K8sCacheHelper) map[string]provider.ResourceProvider {

	// Load Branch ENI Config
	branchConfig := resourceConfig[config.ResourceNamePodENI]

	// Create the branch provider and worker pool
	branchWorker := worker.NewDefaultWorkerPool(branchConfig.Name, branchConfig.WorkerCount,
		config.WorkQueueDefaultMaxRetries, ctrl.Log.WithName("branch eni worker"), context.Background())
	branchProvider := branch.NewBranchENIProvider(ctrl.Log.WithName("branch eni provider"),
		k8sWrapper, ec2APIHelper, branchWorker, cacheHelper)

	// Start the branch worker to accept new jobs on the give function
	err := branchWorker.StartWorkerPool(branchProvider.ProcessAsyncJob)
	if err != nil {
		setupLog.Error(err, "unable to start the branch ENI worker")
		os.Exit(1)
	}

	// Add provider to the list of providers
	*providers = append(*providers, branchProvider)

	return map[string]provider.ResourceProvider{branchConfig.Name: branchProvider}
}
