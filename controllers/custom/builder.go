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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
	mgr manager.Manager
	// dataStoreSyncFlag when set to true means the controller has synced successfully
	// and data store is safe to access
	dataStoreSyncFlag *bool
	log               logr.Logger
	// deleteDataStore with the converted k8s object that are deleted from etcd
	// the reconciler has the onus to clear the object after accessing this data store
	deleteDataStore cache.Indexer
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

func (b *Builder) UsingDeleteDataStore(deleteDataStore cache.Indexer) *Builder {
	b.deleteDataStore = deleteDataStore
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

func NewControllerManagedBy(mgr manager.Manager) *Builder {
	return &Builder{mgr: mgr}
}

// Complete adds the controller to manager's Runnable. The Controller
// runnable will start when the manager starts
func (b *Builder) Complete(sourceEventChanel chan event.GenericEvent) error {
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

	optimizedListWatch := newOptimizedListWatcher(b.clientSet.CoreV1().RESTClient(),
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
					err = notifyChannelOnEvent(convertedObj, sourceEventChanel)
					if err != nil {
						return err
					}

				case cache.Deleted:
					if err = b.dataStore.Delete(convertedObj); err != nil {
						return err
					}
					// Not all controller may need the delete object in Reconciler,
					// only add the object if delete object data store is passed
					if b.deleteDataStore != nil {
						if err = b.deleteDataStore.Add(convertedObj); err != nil {
							return err
						}
					}
					err = notifyChannelOnEvent(convertedObj, sourceEventChanel)
					if err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	controller := &CustomController{
		log:             b.log,
		config:          config,
		dataStoreSynced: b.dataStoreSyncFlag,
	}
	// Adds the controller to the manager's Runnable
	return b.mgr.Add(controller)
}

// SetDefaults sets the default options for controller
func (b *Builder) SetDefaults() {
	if b.options.Namespace == "" {
		b.options.Namespace = metav1.NamespaceAll
	}
	if b.options.PageLimit == 0 {
		b.options.PageLimit = 100
	}
	if b.options.ResyncPeriod == 0 {
		b.options.ResyncPeriod = 30 * time.Minute
	}
}
