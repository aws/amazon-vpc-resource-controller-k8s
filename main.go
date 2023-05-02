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

package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	crdv1alpha1 "github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/apps"
	corecontroller "github.com/aws/amazon-vpc-resource-controller-k8s/controllers/core"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	ec2API "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/version"
	asyncWorkers "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	webhookcore "github.com/aws/amazon-vpc-resource-controller-k8s/webhooks/core"

	"github.com/go-logr/zapr"
	zapRaw "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	clientgocache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme     = runtime.NewScheme()
	setupLog   = ctrl.Log.WithName("setup")
	syncPeriod = time.Minute * 30
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = corev1.AddToScheme(scheme)
	_ = vpcresourcesv1beta1.AddToScheme(scheme)
	_ = crdv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

// To Watch for old controller deployments, windows IPAM will not be enabled till the old controller
// deployments are deleted by users
// +kubebuilder:rbac:groups=apps,resources=deployments,namespace=kube-system,resourceNames=vpc-resource-controller,verbs=get;list;watch
// +kubebuilder:rbac:groups=crd.k8s.amazonaws.com,resources=eniconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=vpcresources.k8s.aws,resources=securitygrouppolicies,verbs=get;list;watch

// Migration to leases based leader election
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,namespace=kube-system,verbs=create
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,namespace=kube-system,resourceNames=cp-vpc-resource-controller,verbs=get;update
func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableDevLogging bool
	var roleARN string
	var enableProfiling bool
	var logLevel string
	var clusterName string
	var listPageLimit int
	var leaderLeaseDurationSeconds int
	var leaderLeaseRenewDeadline int
	var leaderLeaseRetryPeriod int
	var outputPath string
	var introspectBindAddr string
	var leaseOnly bool
	var healthCheckTimeout int

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&roleARN, "role-arn", "",
		"Role ARN that will be assumed to make EC2 API calls "+
			"to perform operations on the user's VPC. This parameter is not required if running the "+
			"controller on your worker node.")
	flag.StringVar(&logLevel, "log-level", "info",
		"Set the controller log level - info(default), debug")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&leaderLeaseDurationSeconds, "leader-lease-duration-seconds", 30,
		"Leader lease duration in seconds")
	flag.IntVar(&leaderLeaseRenewDeadline, "leader-lease-renew-deadline", 15,
		"Leader lease renew deadline in seconds")
	flag.IntVar(&leaderLeaseRetryPeriod, "leader-lease-retry-period", 5,
		"Leader lease retry period")
	flag.BoolVar(&enableDevLogging, "enable-dev-logging", false,
		"Enable developer mode logging for the controller."+
			"With dev mode logging, you will get Debug logs and more structured logging with extra details")
	flag.BoolVar(&enableProfiling, "enable-profiling", false,
		"Enable runtime profiling for debugging purposes.")
	flag.StringVar(&clusterName, "cluster-name", "", "The name of the k8s cluster")
	flag.IntVar(&listPageLimit, "page-limit", 100,
		"The page size limiting the number of response for list operation to API Server")
	flag.StringVar(&outputPath, "log-file", "stderr", "The path to redirect controller logs")
	flag.IntVar(&healthCheckTimeout, "health-check-timeout", 10,
		"How long healthz check waits before failing the attempt")
	flag.StringVar(&introspectBindAddr, "introspect-bind-addr", ":22775",
		"Port for serving the introspection API")
	flag.BoolVar(&leaseOnly, "lease-only", false, "Controller uses lease only for leader election")

	flag.Parse()

	// Dev mode logging disabled by default, to enable set the enableDevLogging argument
	logLvl := zapRaw.NewAtomicLevelAt(0)
	if logLevel == "debug" {
		logLvl = zapRaw.NewAtomicLevelAt(-1)
	}

	// Set up log file
	cfg := zapRaw.NewProductionConfig()
	cfg.OutputPaths = []string{
		outputPath,
	}
	cfg.Level = logLvl
	cfg.Development = enableDevLogging

	// Change from the default epoch time to human readable time format
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.CallerKey = ""

	logger, err := cfg.Build()
	if err != nil {
		fmt.Println("Unable to set up logger, cannot start the controller:", err)
		os.Exit(1)
	}

	ctrl.SetLogger(zapr.NewLogger(logger))

	// Variables injected with ldflags on building the binary
	setupLog.Info("version",
		"GitVersion", version.GitVersion,
		"GitCommit", version.GitCommit,
		"BuildDate", version.BuildDate,
	)

	if clusterName == "" {
		setupLog.Error(fmt.Errorf("cluster-name is a required parameter"), "unable to start the controller")
		os.Exit(1)
	}

	// Profiler disabled by default, to enable set the enableProfiling argument
	if enableProfiling {
		// To use the profiler - https://golang.org/pkg/net/http/pprof/
		go func() {
			setupLog.Info("starting profiler",
				"error", http.ListenAndServe("localhost:6060", nil))
		}()
	}

	kubeConfig := ctrl.GetConfigOrDie()
	// Set the API Server QPS and Burst
	kubeConfig.QPS = config.DefaultAPIServerQPS
	kubeConfig.Burst = config.DefaultAPIServerBurst

	setupLog.Info("starting the controller with leadership setting",
		"leader mode enabled", enableLeaderElection,
		"lease duration(s)", leaderLeaseDurationSeconds, "renew deadline(s)",
		leaderLeaseRenewDeadline, "retry period(s)", leaderLeaseRetryPeriod)

	leaseDuration := time.Second * time.Duration(leaderLeaseDurationSeconds)
	renewDeadline := time.Second * time.Duration(leaderLeaseRenewDeadline)
	retryPeriod := time.Second * time.Duration(leaderLeaseRetryPeriod)

	// filter cache to subscribe to events from specific resources
	newCache := cache.BuilderWithOptions(cache.Options{
		SelectorsByObject: cache.SelectorsByObject{
			&corev1.ConfigMap{}: {Field: fields.Set{
				"metadata.name":      config.VpcCniConfigMapName,
				"metadata.namespace": config.KubeSystemNamespace,
			}.AsSelector()},
			&appsv1.Deployment{}: {Field: fields.Set{
				"metadata.name":      config.OldVPCControllerDeploymentName,
				"metadata.namespace": config.KubeSystemNamespace,
			}.AsSelector()},
			&appsv1.DaemonSet{}: {Field: fields.Set{
				"metadata.name":      config.VpcCNIDaemonSetName,
				"metadata.namespace": config.KubeSystemNamespace,
			}.AsSelector(),
			},
		},
	})

	leaderElectionSource := resourcelock.ConfigMapsLeasesResourceLock
	if leaseOnly {
		leaderElectionSource = resourcelock.LeasesResourceLock
	}

	mgr, err := ctrl.NewManager(kubeConfig, ctrl.Options{
		SyncPeriod:                 &syncPeriod,
		Scheme:                     scheme,
		MetricsBindAddress:         metricsAddr,
		Port:                       9443,
		LeaderElection:             enableLeaderElection,
		LeaseDuration:              &leaseDuration,
		RenewDeadline:              &renewDeadline,
		RetryPeriod:                &retryPeriod,
		LeaderElectionID:           config.LeaderElectionKey,
		LeaderElectionNamespace:    config.LeaderElectionNamespace,
		LeaderElectionResourceLock: leaderElectionSource,
		HealthProbeBindAddress:     ":61779", // the liveness endpoint is default to "/healthz"
		NewCache:                   newCache,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	healthzHandler := rcHealthz.NewHealthzHandler(healthCheckTimeout)
	// add root health ping on manager in general
	healthzHandler.AddControllerHealthChecker("health-root-manager-ping", rcHealthz.SimplePing("root manager", setupLog))

	clientSet, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		setupLog.Error(err, "failed to create client set")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	ec2Wrapper, err := ec2API.NewEC2Wrapper(roleARN, setupLog)
	if err != nil {
		setupLog.Error(err, "unable to create ec2 wrapper")
	}
	ec2APIHelper := ec2API.NewEC2APIHelper(ec2Wrapper, clusterName)

	sgpAPI := utils.NewSecurityGroupForPodsAPI(
		mgr.GetClient(),
		ctrl.Log.WithName("sgp api"))

	// Custom data store, with optimized Pod Object. The data store must be
	// accessed only after the Pod Reconciler has started
	podConverter := pod.PodConverter{}
	dataStore := clientgocache.NewIndexer(podConverter.Indexer, pod.NodeNameIndexer())

	k8sApi := k8s.NewK8sWrapper(mgr.GetClient(), clientSet.CoreV1(), ctx)

	apiWrapper := api.Wrapper{
		EC2API: ec2APIHelper,
		K8sAPI: k8sApi,
		PodAPI: pod.NewPodAPIWrapper(dataStore, mgr.GetClient(), clientSet.CoreV1()),
		SGPAPI: sgpAPI,
	}

	supportedResources := []string{config.ResourceNamePodENI, config.ResourceNameIPAddress}
	resourceManager, err := resource.NewResourceManager(
		ctx, supportedResources, apiWrapper, ctrl.Log.WithName("managers").WithName("resource"), healthzHandler)
	if err != nil {
		ctrl.Log.Error(err, "failed to init resources", "resources", supportedResources)
		os.Exit(1)
	}

	// hasPodDataStoreSynced is set to true when the custom controller has synced
	controllerConditions := condition.NewControllerConditions(
		ctrl.Log.WithName("controller conditions"), k8sApi)

	nodeManagerWorkers := asyncWorkers.NewDefaultWorkerPool("node async workers",
		10, 1, ctrl.Log.WithName("node async workers"), ctx)
	nodeManager, err := manager.NewNodeManager(ctrl.Log.WithName("node manager"), resourceManager,
		apiWrapper, nodeManagerWorkers, controllerConditions, healthzHandler)

	if err != nil {
		ctrl.Log.Error(err, "failed to init node manager")
		os.Exit(1)
	}

	// IMPORTANT: The Pod Reconciler must be the first controller to Run. The controller
	// will not allow any other controller to run till the cache has synced.
	if err := (&corecontroller.PodReconciler{
		Log:             ctrl.Log.WithName("controllers").WithName("Pod Reconciler"),
		ResourceManager: resourceManager,
		NodeManager:     nodeManager,
		K8sAPI:          k8sApi,
		DataStore:       dataStore,
		Condition:       controllerConditions,
	}).SetupWithManager(ctx, mgr, clientSet, listPageLimit, syncPeriod, healthzHandler); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "pod")
		os.Exit(1)
	}

	if err := (&ec2API.ENICleaner{
		EC2Wrapper:  ec2Wrapper,
		ClusterName: clusterName,
		Log:         ctrl.Log.WithName("eni cleaner"),
	}).SetupWithManager(ctx, mgr, healthzHandler); err != nil {
		setupLog.Error(err, "unable to start eni cleaner")
		os.Exit(1)
	}

	if err := (&corecontroller.NodeReconciler{
		Client:     mgr.GetClient(),
		Log:        ctrl.Log.WithName("controllers").WithName("Node"),
		Scheme:     mgr.GetScheme(),
		Manager:    nodeManager,
		Conditions: controllerConditions,
		Context:    ctx,
	}).SetupWithManager(mgr, healthzHandler); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Node")
		os.Exit(1)
	}

	if err := (&corecontroller.ConfigMapReconciler{
		Client:      mgr.GetClient(),
		Log:         ctrl.Log.WithName("controllers").WithName("ConfigMap"),
		Scheme:      mgr.GetScheme(),
		NodeManager: nodeManager,
		K8sAPI:      k8sApi,
		Condition:   controllerConditions,
		Context:     ctx,
	}).SetupWithManager(mgr, healthzHandler); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		os.Exit(1)
	}

	if err := (&apps.DeploymentReconciler{
		Log:         ctrl.Log.WithName("controllers").WithName("Deployment"),
		NodeManager: nodeManager,
		K8sAPI:      k8sApi,
		Condition:   controllerConditions,
	}).SetupWithManager(mgr, healthzHandler); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Deployment")
		os.Exit(1)
	}

	if err := (&resource.IntrospectHandler{
		Log:             ctrl.Log.WithName("introspect"),
		BindAddress:     introspectBindAddr,
		ResourceManager: resourceManager,
	}).SetupWithManager(mgr, healthzHandler); err != nil {
		setupLog.Error(err, "unable to create introspect API")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder
	setupLog.Info("setting up webhook server")
	webhookServer := mgr.GetWebhookServer()

	setupLog.Info("registering webhooks to the webhook server")
	podMutationWebhook := webhookcore.NewPodMutationWebHook(
		sgpAPI, ctrl.Log.WithName("resource mutating webhook"), controllerConditions, healthzHandler)
	webhookServer.Register("/mutate-v1-pod", &webhook.Admission{
		Handler: podMutationWebhook,
	})

	nodeValidateWebhook := webhookcore.NewNodeUpdateWebhook(
		controllerConditions, ctrl.Log.WithName("node validating webhook"), k8sApi, healthzHandler)
	webhookServer.Register("/validate-v1-node", &webhook.Admission{
		Handler: nodeValidateWebhook})

	// Validating webhook for pod.
	annotationValidator := webhookcore.NewAnnotationValidator(
		controllerConditions, ctrl.Log.WithName("annotation validating webhook"), healthzHandler)
	webhookServer.Register("/validate-v1-pod", &webhook.Admission{
		Handler: annotationValidator})

	// Enabled each controllers' health check and aggregate them to endpoint /healthz
	// curl localhost:61779/healthz?verbose can list all controllers' healthy status
	err = healthzHandler.AddControllersHealthStatusChecksToManager(mgr)
	setupLog.Info("adding health check for controllers")
	if err != nil {
		setupLog.Error(err, "unable add health check to all controllers")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
