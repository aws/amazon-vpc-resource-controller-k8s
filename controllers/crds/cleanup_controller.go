package crds

import (
	"context"
	"time"

	ec2API "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type NodeCleanupObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	NodeID      string `json:"nodeID"`
	ClusterName string `json:"clusterName"`
}

func (o *NodeCleanupObject) DeepCopyObject() runtime.Object {
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

type ENICleanup struct {
	Events chan event.GenericEvent
}

func SetupENICleanupController(mgr ctrl.Manager, log logr.Logger, vpcID string, ec2Wrapper ec2API.EC2Wrapper) (*ENICleanup, error) {

	enicleanup := &ENICleanup{}
	enicleanup.Events = make(chan event.GenericEvent, 2048)
	nodecleanupHandler := enqueueRequestForNodeIDEvent{
		log: log,
	}
	rec := &ENICleanupReconciler{
		vpcID:      vpcID,
		ec2Wrapper: ec2Wrapper,
		log:        log,
	}
	source.Channel(enicleanup.Events, &nodecleanupHandler)
	err := ctrl.NewControllerManagedBy(mgr).
		Named("NodeENICleaner").
		WatchesRawSource(source.Channel(enicleanup.Events, &nodecleanupHandler)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 5,
		}).
		Complete(rec)
	if err != nil {
		return nil, err
	}

	return enicleanup, nil
}

var _ handler.TypedEventHandler[client.Object, reconcile.Request] = (*enqueueRequestForNodeIDEvent)(nil)

type enqueueRequestForNodeIDEvent struct {
	log logr.Logger
}

func (h *enqueueRequestForNodeIDEvent) Create(ctx context.Context, _ event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *enqueueRequestForNodeIDEvent) Update(ctx context.Context, _ event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *enqueueRequestForNodeIDEvent) Delete(ctx context.Context, _ event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *enqueueRequestForNodeIDEvent) Generic(ctx context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if e.Object == nil {
		return
	}
	nodeCleanup, ok := e.Object.(*NodeCleanupObject)
	if !ok {
		h.log.Error(nil, "Failed to convert object to NodeCleanupObject")
		return
	}
	if nodeCleanup.Name == "" {
		h.log.Error(nil, "NodeID is empty")
		return
	}
	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: nodeCleanup.Name,
		},
	})
}

type ENICleanupReconciler struct {
	vpcID      string
	ec2Wrapper ec2API.EC2Wrapper
	log        logr.Logger
}

func (r *ENICleanupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute*5)
	defer cancel()
	if err := cleanup.NewNodeResourceCleaner(req.NamespacedName.Name, r.ec2Wrapper, r.vpcID, r.log).DeleteLeakedResources(ctx); err != nil {
		return ctrl.Result{}, err

	}
	return ctrl.Result{}, nil
}
