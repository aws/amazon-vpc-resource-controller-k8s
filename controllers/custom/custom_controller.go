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
	"time"

	"github.com/go-logr/logr"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// Converter for converting k8s object and object list used in watches and list operation
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
	log logr.Logger
	// Controller is the K8s Controller
	controller cache.Controller
	// dataStoreSynced when set to true indicates the data store is synced and ready to access
	dataStoreSynced *bool
	// config for creating low level controller
	config *cache.Config
}

// Starts the low level controller
func (c *CustomController) Start(stop <-chan struct{}) error {
	c.log.Info("starting custom controller")
	c.controller = cache.New(c.config)

	go func() {
		c.MarkDataStoreSynced()
		c.log.Info("data store has synced")
	}()

	// Run the controller
	c.controller.Run(stop)

	return nil
}

// MarkDataStoreSynced sets the dataStoreSynced variable to true when controller has
// successfully synced with API Server
func (c *CustomController) MarkDataStoreSynced() {
	for c.controller == nil || (!c.controller.HasSynced() && c.controller.LastSyncResourceVersion() == "") {
		c.log.Info("waiting for controller to sync")
		time.Sleep(time.Second * 5)
	}
	*c.dataStoreSynced = true
}

// newOptimizedListWatcher returns a list watcher with a custom list function that converts the
// response for each page using the converter function and returns a general watcher
func newOptimizedListWatcher(restClient cache.Getter, resource string, namespace string, limit int,
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
			Do().
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
			Watch()
	}
	return &cache.ListWatch{ListFunc: listFunc, WatchFunc: watchFunc}
}

// notifyChannelOnEvent notifies the event on receiving an update on Process Event
func notifyChannelOnEvent(obj interface{}, eventNotificationChannel chan event.GenericEvent) error {
	meta, err := apimeta.Accessor(obj)
	if err != nil {
		return err
	}
	eventNotificationChannel <- event.GenericEvent{
		Meta:   meta,
		Object: obj.(runtime.Object),
	}
	return nil
}
