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
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Builder struct {
	// options has the configurable parameters for the custom controller
	options Options
	// converter which will be used to convert a k8s object into desired
	// format before it's stored in the data store
	converter Converter
	// clientSet is the kubernetes client set
	clientSet *kubernetes.Clientset
	// dataStore with the converted k8s object, objects being watched by the
	// controller must be queried using this datastore
	dataStore cache.Indexer
	// mgr is the controller runtime manager
	mgr               manager.Manager
	dataStoreSyncFlag *bool

	log logr.Logger
	ctx context.Context
}

func (b *Builder) Named(name string) *Builder {
	b.options.Name = name
	return b
}

func (b *Builder) UsingConverter(converter Converter) *Builder {
	b.converter = converter
	return b
}

func (b *Builder) WithClientSet(clientSet *kubernetes.Clientset) *Builder {
	b.clientSet = clientSet
	return b
}

func (b *Builder) UsingDataStore(dataStore cache.Indexer) *Builder {
	b.dataStore = dataStore
	return b
}

func (b *Builder) WithLogger(logger logr.Logger) *Builder {
	b.log = logger
	return b
}

func (b *Builder) Options(options Options) *Builder {
	b.options = options
	return b
}

func (b *Builder) DataStoreSyncFlag(flag *bool) *Builder {
	b.dataStoreSyncFlag = flag
	return b
}

func NewControllerManagedBy(ctx context.Context, mgr manager.Manager) *Builder {
	return &Builder{mgr: mgr,
		ctx: ctx}
}

// Complete adds the controller to manager's Runnable. The Controller
// runnable will start when the manager starts
func (b *Builder) Complete(reconciler Reconciler) error {
	if b.log == nil {
		return fmt.Errorf("need to set the logger")
	}
	if b.converter == nil {
		return fmt.Errorf("converter not provided, " +
			"must use high level controller if conversion not required")
	}
	if b.clientSet == nil {
		return fmt.Errorf("need to set kubernetes clienset")
	}
	if b.dataStore == nil {
		return fmt.Errorf("need datastore to start the controller")
	}
	if b.dataStoreSyncFlag == nil {
		return fmt.Errorf("data store sync flag cannot be null")
	}
	b.SetDefaults()

	workQueue := workqueue.NewNamedRateLimitingQueue(
		workqueue.DefaultControllerRateLimiter(), b.options.Name)

	optimizedListWatch := newOptimizedListWatcher(b.ctx, b.clientSet.CoreV1().RESTClient(),
		b.converter.Resource(), b.options.Namespace, b.options.PageLimit, b.converter)

	// Create the config for low level controller with the custom converter
	// list and watch
	config := &cache.Config{
		Queue:            cache.NewDeltaFIFO(b.converter.Indexer, b.dataStore),
		ListerWatcher:    optimizedListWatch,
		ObjectType:       b.converter.ResourceType(),
		FullResyncPeriod: b.options.ResyncPeriod,
		Process: func(obj interface{}) error {
			// from oldest to newest
			for _, d := range obj.(cache.Deltas) {
				// Strip down the pod object and keep only the required details
				convertedObj, err := b.converter.ConvertObject(d.Object)
				if err != nil {
					return err
				}
				switch d.Type {
				case cache.Sync, cache.Added, cache.Updated:
					if _, exists, err := b.dataStore.Get(convertedObj); err == nil && exists {
						if err := b.dataStore.Update(convertedObj); err != nil {
							return err
						}
					} else {
						if err := b.dataStore.Add(convertedObj); err != nil {
							return err
						}
					}
					if err != nil {
						return err
					}
					metaObj, ok := convertedObj.(metav1.Object)
					if !ok {
						return fmt.Errorf("failed to get object meta %v", obj)
					}

					// Add the namespace/name to the queue so multiple
					// duplicate events are processed only once at a time
					workQueue.Add(Request{
						NamespacedName: types.NamespacedName{
							Namespace: metaObj.GetNamespace(),
							Name:      metaObj.GetName(),
						},
					})

				case cache.Deleted:
					if err := b.dataStore.Delete(convertedObj); err != nil {
						return err
					}
					// Add entire object instead of namespace/name as from this
					// point onwards the object will no longer be present in cache
					workQueue.Add(Request{
						DeletedObject: convertedObj,
					})
				}
			}
			return nil
		},
	}

	controller := &CustomController{
		log:       b.log,
		options:   b.options,
		config:    config,
		Do:        reconciler,
		workQueue: workQueue,
		syncFlag:  b.dataStoreSyncFlag,
	}

	// Adds the controller to the manager's Runnable
	return b.mgr.Add(controller)
}

// SetDefaults sets the default options for controller
func (b *Builder) SetDefaults() {
	if b.options.Name == "" {
		b.options.Name = fmt.Sprintf("%s custom controller", b.converter.Resource())
	}
	if b.options.Namespace == "" {
		b.options.Namespace = metav1.NamespaceAll
	}
	if b.options.MaxConcurrentReconciles == 0 {
		b.options.MaxConcurrentReconciles = 1
	}
	if b.options.PageLimit == 0 {
		b.options.PageLimit = 100
	}
	if b.options.ResyncPeriod == 0 {
		b.options.ResyncPeriod = 30 * time.Minute
	}
}
