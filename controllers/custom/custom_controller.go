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

package custom

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Converter for converting k8s object and object list used in watches and list operation
// to the desired format.
type Converter interface {
	// ConvertObject takes an object and returns the modified object which will be
	// stored in the data store
	ConvertObject(originalObj interface{}) (convertedObj interface{}, err error)
	// ConvertList takes an object and returns the modified list of objects which
	// will be returned to the Simple Pager function to aggregate the list pagination
	// response
	ConvertList(originalList interface{}) (convertedList interface{}, err error)
	// Resource returns the K8s resource name to list/watch
	Resource() string
	// ResourceType returns the k8s object to list/watch
	ResourceType() runtime.Object
	// Indexer returns the key for indexing custom converted object
	Indexer(obj interface{}) (string, error)
}

type Reconciler interface {
	Reconcile(request Request) (ctrl.Result, error)
}

// Options contains the configurable parameters of the Custom Controller
type Options struct {
	// Name of the controller used for creating named work queues
	Name string
	// PageLimit is the number of objects returned per page on a list operation
	PageLimit int
	// Namespace to list and watch for
	Namespace string
	// ResyncPeriod how often to sync using list with the API Server
	ResyncPeriod time.Duration
	// MaxConcurrentReconciles to parallelize processing of worker queue
	MaxConcurrentReconciles int
}

// This Controller can be used for any type of K8s object, but is used for Pod Objects
// in this repository. There are two reasons why we are using a wrapper over the low level
// controllers instead of using controllers from controller-runtime.
// 1. We don't want to cache the entire Pod Object because of Memory constraints.
//    We need specific details from metadata and Pod Spec. To do this we intercept
//    the request at List; and watch, optimize it before it's stored in cache.
//    Long term plan is to use MetaData only cache or disable Pod caching altogether
// 2. We want the Deleted Object when Pod is Terminating. Pod Networking should only be deleted
//    once the Pod has deleted or all containers have exited.
//    Long term plan is to consider migrating to using finalizers and delete only when
//    all containers have exited.
// In future, we may be able to switch to upstream controller for reconciling Pods if the
// long term solutions are in place
type CustomController struct {
	// workQueue to store create/update/delete events
	workQueue workqueue.RateLimitingInterface
	// mu is the mutex to allow the controller to sync before
	// other controller start
	mu sync.Mutex
	// log for custom controller
	log logr.Logger
	// Reconciler will be called on all the K8s object events
	Do Reconciler
	// config to create a new client-go controller
	config *cache.Config
	// options is the configurable parameters for creating
	// the controller
	options  Options
	syncFlag *bool
}

// Request for Add/Update only contains the Namespace/Name
// Request for Delete contains the Pod Object as by the time
// Delete Request is reconciled the cache will not have it
type Request struct {
	// Add/Update Request will contain the Namespaced Name only. The
	// item can be retrieved from the indexer for add/update events
	NamespacedName types.NamespacedName
	// Delete Event will contain the DeletedObject only.
	DeletedObject interface{}
}

// Starts the low level controller
func (c *CustomController) Start(ctx context.Context) error {
	// This is important to allow the data store to be synced
	// Before the other controller starts
	c.mu.Lock()
	// Shut down the queue so the worker can stop
	defer c.workQueue.ShutDown()

	err := func() error {
		defer c.mu.Unlock()

		coreController := cache.New(c.config)

		c.log.Info("starting custom controller")
		go coreController.Run(ctx.Done())

		// Wait till cache sync
		c.WaitForCacheSync(coreController)

		c.log.Info("Starting Workers", "worker count",
			c.options.MaxConcurrentReconciles)
		for i := 0; i < c.options.MaxConcurrentReconciles; i++ {
			go wait.Until(c.worker, time.Second, ctx.Done())
		}

		return nil
	}()
	if err != nil {
		return err
	}

	<-ctx.Done()
	c.log.Info("stopping workers")
	return nil
}

// WaitForCacheSync tills the cache has synced, this must be done under
// mutex lock to prevent other controllers from starting at same time
func (c *CustomController) WaitForCacheSync(controller cache.Controller) {
	for !controller.HasSynced() && controller.LastSyncResourceVersion() == "" {
		c.log.Info("waiting for controller to sync")
		time.Sleep(time.Second * 5)
	}
	*c.syncFlag = true
	c.log.Info("cache has synced successfully")
}

// newOptimizedListWatcher returns a list watcher with a custom list function that converts the
// response for each page using the converter function and returns a general watcher
func newOptimizedListWatcher(ctx context.Context, restClient cache.Getter, resource string, namespace string, limit int,
	converter Converter) *cache.ListWatch {

	listFunc := func(options metav1.ListOptions) (runtime.Object, error) {
		list, err := restClient.Get().
			Namespace(namespace).
			Resource(resource).
			// This needs to be done because just setting the limit using option's
			// Limit is being overridden and the response is returned without pagination.
			VersionedParams(&metav1.ListOptions{
				Limit:    int64(limit),
				Continue: options.Continue,
			}, metav1.ParameterCodec).
			Do(ctx).
			Get()
		if err != nil {
			return list, err
		}
		// Strip down the the list before passing the paginated response back to
		// the pager function
		convertedList, err := converter.ConvertList(list)
		return convertedList.(runtime.Object), err
	}

	// We don't need to modify the watcher, we will strip down the k8s object in the ProcessFunc
	// before storing the object in the data store.
	watchFunc := func(options metav1.ListOptions) (watch.Interface, error) {
		options.Watch = true
		return restClient.Get().
			Namespace(namespace).
			Resource(resource).
			VersionedParams(&options, metav1.ParameterCodec).
			Watch(ctx)
	}
	return &cache.ListWatch{ListFunc: listFunc, WatchFunc: watchFunc}
}

func (c *CustomController) worker() {
	for c.processNextWorkItem() {
	}
}

func (c *CustomController) processNextWorkItem() bool {
	obj, shutdown := c.workQueue.Get()
	if shutdown {
		// Stop working
		return false
	}

	// We call Done here so the workqueue knows we have finished
	// processing this item. We also must remember to call Forget if we
	// do not want this work item being re-queued. For example, we do
	// not call Forget if a transient error occurs, instead the item is
	// put back on the workqueue and attempted again after a back-off
	// period.
	defer c.workQueue.Done(obj)

	// The item from the workqueue will be forgotten in the handler, when
	// it's successfully processed.
	return c.reconcileHandler(obj)
}

func (c *CustomController) reconcileHandler(obj interface{}) bool {
	var req Request
	var ok bool
	if req, ok = obj.(Request); !ok {
		// As the item in the workqueue is actually invalid, we call
		// Forget here else we'd go into a loop of attempting to
		// process a work item that is invalid.
		c.workQueue.Forget(obj)
		c.log.Error(nil, "Queue item was not a Request",
			"type", fmt.Sprintf("%T", obj), "value", obj)
		// Return true, don't take a break
		return true
	}
	// RunInformersAndControllers the syncHandler, passing it the namespace/Name string of the
	// resource to be synced.
	if result, err := c.Do.Reconcile(req); err != nil {
		c.workQueue.AddRateLimited(req)
		c.log.Error(err, "Reconciler error", "request", req)
		return false
	} else if result.RequeueAfter > 0 {
		// The result.RequeueAfter request will be lost, if it is returned
		// along with a non-nil error. But this is intended as
		// We need to drive to stable reconcile loops before queuing due
		// to result.RequestAfter
		c.workQueue.Forget(obj)
		c.workQueue.AddAfter(req, result.RequeueAfter)
		return true
	} else if result.Requeue {
		c.workQueue.AddRateLimited(req)
		return true
	}

	// Finally, if no error occurs we Forget this item so it does not
	// get queued again until another change happens.
	c.workQueue.Forget(obj)

	c.log.V(1).Info("Successfully Reconciled", "request", req)

	// Return true, don't take a break
	return true
}
