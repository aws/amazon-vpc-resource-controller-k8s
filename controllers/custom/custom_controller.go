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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
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
}

type CustomController struct {
	// ClientSet is the kubernetes client set
	ClientSet *kubernetes.Clientset
	// PageLimit is the number of objects returned per page on a list operation
	PageLimit int64
	// Namespace to list/watch for
	Namespace string
	// Converter is the converter implementation that converts the k8s
	// object before storing in the data store
	Converter Converter
	// ResyncPeriod how often to sync using list with the API Server
	ResyncPeriod time.Duration
	// RetryOnError weather item should be retried on error. Should remain false in usual use case
	RetryOnError bool
	// DataStore with the converted k8s object
	DataStore cache.Store
	// Queue is the Delta FIFO queue
	Queue *cache.DeltaFIFO
	// CreateUpdateEventNotificationChan channel will be notified for all create and update
	// events for the k8s resource. If we don't want memory usage spikes we should
	// process the events as soon as soon as the channel is notified.
	CreateUpdateEventNotificationChan chan event.GenericEvent
	// DeleteEventNotificationChan channel will be notified for all delete events for the
	// k8s resource. If we don't want memory usage spikes we should process the events as
	// soon as soon as the channel is notified.
	DeleteEventNotificationChan chan event.GenericEvent
	// SkipCacheDeletion will not delete the entry from cache on receiving a Delete event.
	// The k8s object should be deleted by the routine listing for delete events on the delete
	// event chanel. This is useful for kube builder controller which provides API with just the
	// namespace + name without returning the entire object from the event
	SkipCacheDeletion bool
}

// StartController starts the custom controller by doing a list and watch on the specified k8s
// resource. The controller would store the converted k8s object in the provided indexer. The
// stop channel should be notified to stop the controller
func (c *CustomController) StartController(stopChanel chan struct{}) {
	config := &cache.Config{
		Queue: c.Queue,
		ListerWatcher: newListWatcher(c.ClientSet.CoreV1().RESTClient(),
			c.Converter.Resource(), c.Namespace, c.PageLimit, c.Converter),
		ObjectType:       c.Converter.ResourceType(),
		FullResyncPeriod: c.ResyncPeriod,
		RetryOnError:     c.RetryOnError,
		Process: func(obj interface{}) error {
			// from oldest to newest
			for _, d := range obj.(cache.Deltas) {
				// Strip down the pod object and keep only the required details
				convertedObj, err := c.Converter.ConvertObject(d.Object)
				if err != nil {
					return err
				}
				switch d.Type {
				case cache.Sync, cache.Added, cache.Updated:
					if _, exists, err := c.DataStore.Get(convertedObj); err == nil && exists {
						if err := c.DataStore.Update(convertedObj); err != nil {
							return err
						}
					} else {
						if err := c.DataStore.Add(convertedObj); err != nil {
							return err
						}
					}

					c.notifyChannelOnCreateUpdate(convertedObj)
					if err != nil {
						return err
					}

				case cache.Deleted:
					// Prevent removing from cache on delete. The notified channel should take
					// care of removing the entry.
					if !c.SkipCacheDeletion {
						if err := c.DataStore.Delete(convertedObj); err != nil {
							return err
						}
					}
					err = c.notifyChannelOnDelete(convertedObj)
					if err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	defer close(stopChanel)
	cache.New(config).Run(stopChanel)
}

// newListWatcher returns a list watcher with a custom list function that converts the
// response for each page using the converter function and returns a general watcher
func newListWatcher(restClient cache.Getter, resource string, namespace string, limit int64,
	converter Converter) *cache.ListWatch {

	listFunc := func(options metav1.ListOptions) (runtime.Object, error) {
		list, err := restClient.Get().
			Namespace(namespace).
			Resource(resource).
			// This needs to be done because just setting the limit using option's
			// Limit is being overridden and the response is returned without pagination.
			VersionedParams(&metav1.ListOptions{
				Limit:    limit,
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

// notifyChannelOnCreateUpdate notifies the add/update event on the appropriate channel
func (c *CustomController) notifyChannelOnCreateUpdate(obj interface{}) error {
	meta, err := apimeta.Accessor(obj)
	if err != nil {
		return err
	}
	c.CreateUpdateEventNotificationChan <- event.GenericEvent{
		Meta:   meta,
		Object: obj.(runtime.Object),
	}
	return nil
}

// notifyChannelOnCreateUpdate notifies the delete event on the appropriate channel
func (c *CustomController) notifyChannelOnDelete(obj interface{}) error {
	meta, err := apimeta.Accessor(obj)
	if err != nil {
		return err
	}
	c.DeleteEventNotificationChan <- event.GenericEvent{
		Meta:   meta,
		Object: obj.(runtime.Object),
	}
	return nil
}
