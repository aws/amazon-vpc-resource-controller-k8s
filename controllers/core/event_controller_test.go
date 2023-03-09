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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	// "errors"
	"testing"
	"time"

	bigcache "github.com/allegro/bigcache/v3"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	// "github.com/stretchr/testify/assert"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	goWatch "k8s.io/client-go/tools/watch"

	// "sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"

	// eventsv1 "k8s.io/api/events/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mockSGPPodEventName    = "aws-node-sgp-event"
	mockCNEventName        = "node-custom-networking-event"
	mockEventNodeNameOne   = "ip-0-0-0-1.us-west-2.compute.internal"
	mockEventNodeNameTwo   = "ip-0-0-0-2.us-west-2.compute.internal"
	mockEventNodeNameThree = "ip-0-0-0-3.us-west-2.compute.internal"
	mockInstanceIdOne      = "i-00000000000000001"
	mockInstanceIdTwo      = "i-00000000000000002"
	mockInstanceIdThree    = "i-00000000000000003"
	EventRegardingKind     = "Node"
	EventRelatedKind       = "Pod"

	namespacedName = types.NamespacedName{
		Name:      mockSGPPodEventName,
		Namespace: config.KubeSystemNamespace,
	}

	oldSgpEvent = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPPodEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 3)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockSGPPodEventName,
		},
		Related: &corev1.ObjectReference{
			Kind: EventRelatedKind,
			Name: mockEventNodeNameOne,
			UID:  types.UID(mockInstanceIdOne),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Note:                config.TrunkNotAttached,
		Action:              config.VpcCNINodeEventActionForTrunk,
	}
	newSgpEventOne = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPPodEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockSGPPodEventName,
		},
		Related: &corev1.ObjectReference{
			Kind: EventRelatedKind,
			Name: mockEventNodeNameTwo,
			UID:  types.UID(mockInstanceIdTwo),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Note:                config.TrunkNotAttached,
		Action:              config.VpcCNINodeEventActionForTrunk,
	}
	newSgpEventTwo = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPPodEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockSGPPodEventName,
		},
		Related: &corev1.ObjectReference{
			Kind: EventRelatedKind,
			Name: mockEventNodeNameThree,
			UID:  types.UID(mockInstanceIdThree),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Note:                config.TrunkNotAttached,
		Action:              config.VpcCNINodeEventActionForTrunk,
	}
	oldEniConfigEvent = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockCNEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 3)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockSGPPodEventName,
		},
		Related: &corev1.ObjectReference{
			Kind: EventRelatedKind,
			Name: mockEventNodeNameOne,
			UID:  types.UID(mockInstanceIdOne),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Action:              config.VpcCNINodeEventActionForEniConfig,
		Note:                config.CustomNetworkingLabel + "=testConfig",
	}
	newEniConfigEventOne = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockCNEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockSGPPodEventName,
		},
		Related: &corev1.ObjectReference{
			Kind: EventRelatedKind,
			Name: mockEventNodeNameTwo,
			UID:  types.UID(mockInstanceIdTwo),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Action:              config.VpcCNINodeEventActionForEniConfig,
		Note:                config.CustomNetworkingLabel + "=testConfig",
	}
	newEniConfigEventTwo = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockCNEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockSGPPodEventName,
		},
		Related: &corev1.ObjectReference{
			Kind: EventRelatedKind,
			Name: mockEventNodeNameThree,
			UID:  types.UID(mockInstanceIdThree),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Action:              config.VpcCNINodeEventActionForEniConfig,
		Note:                config.CustomNetworkingLabel + "=testConfig",
	}
	testCacheExpiry    = 2 * time.Second
	testWaitCacheEvict = 3 * time.Second
	testCacheMiss      = "Entry not found"
)

type TestSGPEvent struct {
	event                 *eventsv1.Event
	namespacedName        types.NamespacedName
	isValidEventForSGP    bool
	successfullyLabelNode bool
	testNodeName          string
	testNodeKey           string
	msg                   string
	hitCache              bool
	missCache             bool
	evictCache            bool
	rv                    string
}

type TestCNEvent struct {
	event                           *eventsv1.Event
	namespacedName                  types.NamespacedName
	isValidEventForCustomNetworking bool
	successfullyLabelNode           bool
	testNodeName                    string
	testNodeKey                     string
	msg                             string
	hitCache                        bool
	missCache                       bool
	evictCache                      bool
}

type TestNode struct {
	node     *corev1.Node
	sgpEvent bool
	cnEvent  bool
	labelled bool
	msg      string
}

var sgpEvents = []TestSGPEvent{
	{
		event:                 oldSgpEvent,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: true,
		testNodeName:          mockEventNodeNameOne,
		testNodeKey:           string(oldSgpEvent.Related.UID),
		msg:                   "SGP Cache Test: old event should still work",
		hitCache:              false,
		missCache:             true,
		evictCache:            false,
		rv:                    "1",
	},
	{
		event:                 newSgpEventOne,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: true,
		testNodeName:          mockEventNodeNameTwo,
		testNodeKey:           string(newSgpEventOne.Related.UID),
		msg:                   "SGP Cache Test: Valid event one, label node",
		hitCache:              false,
		missCache:             true,
		evictCache:            false,
		rv:                    "2",
	},
	{
		event:                 newSgpEventTwo,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: false,
		testNodeName:          mockEventNodeNameThree,
		testNodeKey:           string(newSgpEventTwo.Related.UID),
		msg:                   "SGP Cache Test: Valid event two, fail labelling node",
		hitCache:              false,
		missCache:             true,
		evictCache:            false,
		rv:                    "3",
	},
	{
		event:                 newSgpEventTwo,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: true,
		testNodeName:          mockEventNodeNameThree,
		testNodeKey:           string(newSgpEventTwo.Related.UID),
		msg:                   "SGP Cache Test: Valid event two, label node",
		hitCache:              false,
		missCache:             true,
		evictCache:            false,
		rv:                    "4",
	},
	{
		event:                 newSgpEventTwo,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: true,
		testNodeName:          mockEventNodeNameThree,
		testNodeKey:           string(newSgpEventTwo.Related.UID),
		msg:                   "SGP Cache Test: Valid event two, label node, cache hit",
		hitCache:              true,
		missCache:             false,
		evictCache:            false,
		rv:                    "5",
	},
	{
		event:                 newSgpEventTwo,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: true,
		testNodeName:          mockEventNodeNameThree,
		testNodeKey:           string(newSgpEventTwo.Related.UID),
		msg:                   "SGP Cache Test: Valid event, label node, cache expired and should be miss",
		hitCache:              false,
		missCache:             true,
		evictCache:            true,
		rv:                    "6",
	},
	{
		event:                 newSgpEventTwo,
		namespacedName:        namespacedName,
		isValidEventForSGP:    true,
		successfullyLabelNode: true,
		testNodeName:          mockEventNodeNameThree,
		testNodeKey:           string(newSgpEventTwo.Related.UID),
		msg:                   "SGP Cache Test: Valid event, label node, cache expired and should be miss",
		hitCache:              false,
		missCache:             true,
		evictCache:            true,
		rv:                    "7",
	},
}

var cnEvents = []TestCNEvent{
	{
		event:                           oldEniConfigEvent,
		namespacedName:                  namespacedName,
		isValidEventForCustomNetworking: true,
		successfullyLabelNode:           false,
		testNodeName:                    mockEventNodeNameOne,
		testNodeKey:                     string(oldEniConfigEvent.Related.UID),
		msg:                             "Custom Networking Cache Test: old event is ok for processEvent method",
		hitCache:                        false,
		missCache:                       true,
		evictCache:                      false,
	},
	{
		event:                           newEniConfigEventOne,
		namespacedName:                  namespacedName,
		isValidEventForCustomNetworking: true,
		successfullyLabelNode:           true,
		testNodeName:                    mockEventNodeNameTwo,
		testNodeKey:                     string(newEniConfigEventOne.Related.UID),
		msg:                             "Custom Networking Cache Test: Valid event one, label node",
		hitCache:                        false,
		missCache:                       true,
		evictCache:                      false,
	},
	{
		event:                           newEniConfigEventTwo,
		namespacedName:                  namespacedName,
		isValidEventForCustomNetworking: true,
		successfullyLabelNode:           false,
		testNodeName:                    mockEventNodeNameThree,
		testNodeKey:                     string(newEniConfigEventTwo.Related.UID),
		msg:                             "Custom Networking Cache Test: Valid event two, fail labelling node",
		hitCache:                        false,
		missCache:                       true,
		evictCache:                      false,
	},
	{
		event:                           newEniConfigEventTwo,
		namespacedName:                  namespacedName,
		isValidEventForCustomNetworking: true,
		successfullyLabelNode:           true,
		testNodeName:                    mockEventNodeNameThree,
		testNodeKey:                     string(newEniConfigEventTwo.Related.UID),
		msg:                             "Custom Networking Cache Test: Valid event two, label node",
		hitCache:                        false,
		missCache:                       true,
		evictCache:                      false,
	},
	{
		event:                           newEniConfigEventTwo,
		namespacedName:                  namespacedName,
		isValidEventForCustomNetworking: true,
		successfullyLabelNode:           true,
		testNodeName:                    mockEventNodeNameThree,
		testNodeKey:                     string(newEniConfigEventTwo.Related.UID),
		msg:                             "Custom Networking Cache Test: Valid event two, label node, cache hit",
		hitCache:                        true,
		missCache:                       false,
		evictCache:                      false,
	},
	{
		event:                           newEniConfigEventTwo,
		namespacedName:                  namespacedName,
		isValidEventForCustomNetworking: true,
		successfullyLabelNode:           true,
		testNodeName:                    mockEventNodeNameThree,
		testNodeKey:                     string(newEniConfigEventTwo.Related.UID),
		msg:                             "Custom Networking Cache Test: Valid event, label node, cache expired and should be miss",
		hitCache:                        false,
		missCache:                       true,
		evictCache:                      true,
	},
}

type EventMock struct {
	MockK8sAPI *mock_k8s.MockK8sWrapper
	Controller WatchedEventController
}

func NewEventControllerMock(ctrl *gomock.Controller, mockObjects ...runtime.Object) EventMock {
	scheme := runtime.NewScheme()
	_ = eventsv1.AddToScheme(scheme)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	testCache, _ := bigcache.NewBigCache(bigcache.DefaultConfig(testCacheExpiry))
	return EventMock{
		MockK8sAPI: mockK8sAPI,
		Controller: WatchedEventController{
			logger:         zap.New(),
			k8sAPI:         mockK8sAPI,
			cache:          testCache,
			ctx:            context.TODO(),
			eventWatchTime: 1,
		},
	}
}

// TestEventWatcher_ReceiveAll test if the watcher receives all events
func TestEventWatcher_ReceiveAll(t *testing.T) {
	var watchEvents []watch.Event
	for _, e := range sgpEvents {
		e.event.SetResourceVersion(e.rv)
		watchEvents = append(watchEvents, makeTestEvent(e.event))
	}

	watcher, err := goWatch.NewRetryWatcher("1", &cache.ListWatch{
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return watch.NewProxyWatcher(arrayToChannel(watchEvents)), nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create a RetryWatcher: %v", err)
	}

	// Give the watcher a chance to get to sending events (blocking)
	time.Sleep(10 * time.Millisecond)

	var wg sync.WaitGroup
	eventsCount := len(sgpEvents)
	wg.Add(eventsCount)
	go func() {
	LOOP:
		for {
			select {
			case e, open := <-watcher.ResultChan():
				if open {
					fmt.Println("Result Chan is OPEN")
					if e.Object != nil {
						fmt.Printf("The event type is %v\n", e.Type)
						wg.Done()
					} else {
						fmt.Println("Result Chan has no more event")
						watcher.Stop()
					}
				} else {
					fmt.Println("Result Chan is CLOSE")
					break LOOP
				}
			case <-watcher.Done():
				fmt.Println("Watcher is DONE")
				break LOOP
			}
		}
	}()

	wg.Wait()
	watcher.Stop()
	fmt.Println("Main routine stopped watcher")
	_, open := <-watcher.ResultChan()
	assert.False(t, open, "watcher channel should be closed")
}

// TestEventWatcher_Waiting tests if watcher waits
func TestEventWatcher_Waiting(t *testing.T) {
	var watchEvents []watch.Event
	for _, e := range sgpEvents {
		e.event.SetResourceVersion(e.rv)
		watchEvents = append(watchEvents, makeTestEvent(e.event))
	}

	watcher, err := goWatch.NewRetryWatcher("1", &cache.ListWatch{
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return watch.NewProxyWatcher(arrayToChannel(watchEvents)), nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create a RetryWatcher: %v", err)
	}

	// Give the watcher a chance to get to sending events (blocking)
	time.Sleep(10 * time.Millisecond)

	go func() {
	LOOP:
		for {
			select {
			case e, open := <-watcher.ResultChan():
				if open {
					if e.Object != nil {
						fmt.Printf("The event type is %v\n", e.Type)
					} else {
						fmt.Println("Result Chan has no more event")
						watcher.Stop()
					}
				} else {
					fmt.Println("Result Chan is CLOSE")
					break LOOP
				}
			case <-watcher.Done():
				fmt.Println("Watcher is DONE")
				break LOOP
			}
		}
	}()
	fmt.Println("Waiting for 2 seconds")
	for i := 0; i < 10; i += 1 {
		select {
		case <-watcher.Done():
			t.Fatalf("watcher shouldn't be closed when waiting")
		case _, open := <-watcher.ResultChan():
			fmt.Println("Result Chan is OPEN in waiting window")
			assert.True(t, open, "watcher channel should be open")
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
	watcher.Stop()
	fmt.Println("Main routine stopped watcher")
	_, open := <-watcher.ResultChan()
	assert.False(t, open, "watcher channel should be closed")
}

// TestEventWatcher_WatcherBehaviors tests how watcher works with events
func TestEventWatcher_WatcherBehaviors(t *testing.T) {
	var watchEvents []watch.Event
	testEvents := []*eventsv1.Event{
		oldSgpEvent,
		newSgpEventOne,
		newSgpEventTwo,
	}
	for rv, e := range testEvents {
		e.SetResourceVersion(strconv.Itoa(rv + 1))
		watchEvents = append(watchEvents, makeTestEvent(e))
	}

	watcher, err := goWatch.NewRetryWatcher("1", &cache.ListWatch{
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return watch.NewProxyWatcher(arrayToChannel(watchEvents)), nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create a RetryWatcher: %v", err)
	}

	// Give the watcher a chance to get to sending events (blocking)
	time.Sleep(10 * time.Millisecond)

	// the old event has older creation timestamp and will be skipped
	go checkEvent(t, watcher, len(testEvents)-1)
	time.Sleep(3 * time.Second)
	watcher.Stop()
	_, open := <-watcher.Done()
	assert.True(t, !open)
}

func checkEvent(t *testing.T, watcher *goWatch.RetryWatcher, callCounts int) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewEventControllerMock(ctrl)

	mock.MockK8sAPI.EXPECT().GetRetryWatcher(gomock.Any(), gomock.Any()).Return(watcher, nil)
	eventNode := &corev1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: mockEventNodeNameOne,
		},
	}
	// Don't remove Times(), we need explicitly test how many times they are called
	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(eventNode, nil).Times(callCounts)
	mock.MockK8sAPI.EXPECT().AddLabelToManageNode(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(callCounts)

	assert.Panics(t, func() { mock.Controller.watchEvents() }, "the controller should have panic after another routine stops watcher!")
}

// TestEventController_ProcessSGPEvent tests labelling for SGP with nodes
func TestEventController_ProcessSGPEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)
	for _, e := range sgpEvents {

		if e.evictCache {
			time.Sleep(testCacheExpiry + testWaitCacheEvict)
		}
		callCount := 1
		if e.hitCache {
			callCount = 0
		}

		eventNode := &corev1.Node{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:   mockEventNodeNameOne,
				Labels: make(map[string]string),
			},
		}

		mock.MockK8sAPI.EXPECT().GetNode(e.testNodeName).Return(eventNode, nil).Times(callCount)

		_, cacheErr := mock.Controller.cache.Get(e.testNodeKey)

		// the same instance was added already but waited for expiry + 1 second, we should see cache miss error
		if e.missCache && e.evictCache && !e.hitCache {
			assert.Error(t, cacheErr, e.msg)
			assert.EqualError(t, cacheErr, testCacheMiss, e.msg)
		} else if e.hitCache {
			assert.NoError(t, cacheErr, e.msg)
		} else if e.missCache && !e.evictCache {
			assert.Error(t, cacheErr, e.msg)
		}

		if e.isValidEventForSGP {
			if cacheErr == nil || e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).Times(callCount)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(false, errors.New("sgp-test")).Times(callCount)
			}
		}
		err := mock.Controller.processEvent(*e.event)

		if cacheErr == nil || e.successfullyLabelNode {
			assert.NoError(t, err, e.msg)
		} else if e.isValidEventForSGP && !e.successfullyLabelNode {
			assert.Error(t, err, e.msg)
			assert.EqualError(t, err, "sgp-test", e.msg)
		} else if !e.isValidEventForSGP {
			assert.NoError(t, err, e.msg)
		} else {
			assert.FailNow(t, "Unexpected test case, need to fail test as safeguard")
		}
	}
}

// TestEventController_ProcessCNEvent tests labelling for custom networking
func TestEventController_ProcessCNEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)

	for _, e := range cnEvents {
		if e.evictCache {
			time.Sleep(testCacheExpiry + testWaitCacheEvict)
		}

		callCount := 1
		if e.hitCache {
			callCount = 0
		}

		_, cacheErr := mock.Controller.cache.Get(e.testNodeKey)

		// the same instance was added already but waited for expiry + 1 second, we should see cache miss error
		if e.missCache && e.evictCache && !e.hitCache {
			assert.Error(t, cacheErr, e.msg)
			assert.EqualError(t, cacheErr, testCacheMiss, e.msg)
		} else if e.hitCache {
			assert.NoError(t, cacheErr, e.msg)
		} else if e.missCache && !e.evictCache {
			assert.Error(t, cacheErr, e.msg)
		}

		if e.isValidEventForCustomNetworking {
			eventNode := &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   e.testNodeName,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "false"},
				},
			}
			mock.MockK8sAPI.EXPECT().GetNode(e.testNodeName).Return(eventNode, nil).Times(callCount)
			if cacheErr == nil || e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).Times(callCount)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(false, errors.New("custom-networking-test")).Times(callCount)
			}
		}

		err := mock.Controller.processEvent(*e.event)

		if cacheErr == nil || e.successfullyLabelNode {
			assert.NoError(t, err)
		} else if e.isValidEventForCustomNetworking && !e.successfullyLabelNode {
			assert.Error(t, err)
			assert.EqualError(t, err, "custom-networking-test")
		} else if !e.isValidEventForCustomNetworking {
			assert.NoError(t, err, e.msg)
		} else {
			assert.FailNow(t, "Unexpected test case, need to fail test as safeguard")
		}
	}
}

// TestEventReconciler_Reconcile_DualEvents tests both SGP and CN need to be labeled with nodes
func TestEventReconciler_Reconcile_DualEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewEventControllerMock(ctrl)

	eventList := &eventsv1.EventList{}
	eventList.Items = append([]eventsv1.Event{}, *newSgpEventOne)
	eventList.Items = append(eventList.Items, *newEniConfigEventOne)
	eventList.Items = append(eventList.Items, *newSgpEventOne)
	eventList.Items = append(eventList.Items, *newEniConfigEventOne)

	eventNode := &corev1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: mockEventNodeNameTwo,
		},
	}

	events := []*eventsv1.Event{newSgpEventOne, newEniConfigEventOne}

	times := 2
	var err error
	for i := 0; i < times; i++ {
		for _, value := range events {
			// calls should be made only twice since after one SGP event and one Custom networking event other events for the same instance should be ignored
			// due to cache hit
			expectedCallTimes := times - i - 1
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, gomock.Any(), gomock.Any()).Return(true, nil).Times(expectedCallTimes)
			err = mock.Controller.processEvent(*value)
			assert.NoError(t, err, "Event processor has no error for test event", value)
		}
	}
	assert.NoError(t, err, "Event processor has no error for dual events tests")
	assert.True(t, mock.Controller.cache.Len() == 1)
	cachedEntry, err := mock.Controller.cache.Get(mockInstanceIdTwo)
	assert.NoError(t, err, "Should get entry from test cache successfully")
	assert.True(t, cachedEntry[EnableSGP] == 1, "SGP is cached")
	assert.True(t, cachedEntry[EnableCN] == 1, "Custom networking is cached")
}

// TestEventReconciler_Cache_Works tests cache works when same events are received
func TestEventReconciler_Cache_Works(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)

	events := []*eventsv1.Event{
		newSgpEventOne,
		newSgpEventOne,
		newSgpEventOne,
	}
	eventNode := &corev1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: newSgpEventOne.Related.Name,
		},
	}

	// node cache should skip the other two duplicated events
	validRequest := 1

	mock.MockK8sAPI.EXPECT().GetNode(newSgpEventOne.Related.Name).Return(eventNode, nil).Times(validRequest)
	mock.MockK8sAPI.EXPECT().AddLabelToManageNode(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(validRequest)
	for _, e := range events {
		mock.Controller.processEvent(*e)
	}
	assert.True(t, mock.Controller.cache.Len() == validRequest)
}

func TestEventReconciler_Cache_Works_Wiuth_NodeLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)

	events := []*eventsv1.Event{
		newSgpEventOne,
		newSgpEventTwo,
	}
	eventNode := &corev1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: newSgpEventOne.Related.Name,
			// existing node labels should skip all attempts to label nodes
			Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "false"},
		},
	}

	callGetNode := 2
	callAddLabel := 0
	cacheSize := 2

	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(eventNode, nil).Times(callGetNode)
	mock.MockK8sAPI.EXPECT().AddLabelToManageNode(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(callAddLabel)
	for _, e := range events {
		mock.Controller.processEvent(*e)
	}
	assert.True(t, mock.Controller.cache.Len() == cacheSize)
}

// TestEventReconciler_Reconcile_DualEventsCacheStatus tests code labels node cache behavior for both SGP and CN enabled
func TestEventReconciler_Reconcile_DualEventsCacheStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)

	var events = []struct {
		event *eventsv1.Event
		nn    types.NamespacedName
		sgp   bool
		cn    bool
		msg   string
	}{
		{
			event: newSgpEventOne,
			nn:    namespacedName,
			sgp:   true,
			msg:   "SGP event one",
		},
		{
			event: newEniConfigEventOne,
			nn:    namespacedName,
			cn:    true,
			msg:   "Custom networking event one",
		},
		{
			event: newSgpEventOne,
			nn:    namespacedName,
			sgp:   true,
			msg:   "SGP event one",
		},
		{
			event: newEniConfigEventOne,
			nn:    namespacedName,
			cn:    true,
			msg:   "Custom networking event one",
		},
	}

	var expectedCallTimes int
	for idx, e := range events {
		eventNode := &corev1.Node{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: mockEventNodeNameTwo,
			},
		}
		switch idx {
		case 0:
			expectedCallTimes = 1
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			if e.sgp {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).Times(expectedCallTimes)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).Times(expectedCallTimes)
			}
			_, err := mock.Controller.cache.Get(mockInstanceIdTwo)
			assert.Error(t, err)
			assert.True(t, mock.Controller.cache.Len() == 0)
		case 1:
			expectedCallTimes = 1
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			if e.sgp {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).Times(expectedCallTimes)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).Times(expectedCallTimes)
			}
			entry, err := mock.Controller.cache.Get(mockInstanceIdTwo)
			assert.NoError(t, err)
			assert.True(t, mock.Controller.cache.Len() == 1)
			assert.True(t, entry[EnableSGP] == 1 && entry[EnableCN] == 0, "Cache miss with entry")
		default:
			// at this moment, the cache should have updated for the key (instance id) with both features flagged as {1, 1}
			// cache should be hit no matter how many events for this instance are added into this test
			expectedCallTimes = 0
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			if e.sgp {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).Times(expectedCallTimes)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).Times(expectedCallTimes)
			}
			entry, err := mock.Controller.cache.Get(mockInstanceIdTwo)
			assert.NoError(t, err)
			assert.True(t, mock.Controller.cache.Len() == 1)
			assert.True(t, entry[EnableSGP] == 1 && entry[EnableCN] == 1, "Cache hit")
		}
		mock.Controller.processEvent(*e.event)
	}
}

// TestEventController_LabelledNod tests if code skip labelled node to avoid race condition
func TestEventController_LabelledNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)

	events := []TestNode{
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   mockEventNodeNameTwo,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "false"},
				},
			},
			sgpEvent: true,
			labelled: true,
			msg:      "node has been labelled with false for SGP",
		},
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   mockEventNodeNameTwo,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "true"},
				},
			},
			sgpEvent: true,
			labelled: true,
			msg:      "node has been labelled with true for SGP",
		},
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   mockEventNodeNameTwo,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "not-supported"},
				},
			},
			sgpEvent: true,
			labelled: true,
			msg:      "node has been labelled with not-supported for SGP",
		},
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   mockEventNodeNameTwo,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "testConfig"},
				},
			},
			cnEvent:  true,
			labelled: true,
			msg:      "node has been labelled with config for custom networking",
		},
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   mockEventNodeNameTwo,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux},
				},
			},
			sgpEvent: true,
			labelled: false,
			msg:      "node has no valid label",
		},
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   mockEventNodeNameTwo,
					Labels: make(map[string]string),
				},
			},
			sgpEvent: true,
			labelled: false,
			msg:      "node has empty label map",
		},
		{
			node: &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: mockEventNodeNameTwo,
				},
			},
			sgpEvent: true,
			labelled: false,
			msg:      "node has nil label map",
		},
	}

	for _, e := range events {
		callCount := 1
		if e.labelled {
			callCount = 0
		}
		mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(e.node, nil).Times(1)
		mock.MockK8sAPI.EXPECT().AddLabelToManageNode(e.node, gomock.Any(), gomock.Any()).Return(true, nil).Times(callCount)
		err := mock.Controller.processEvent(*updateTestNodeUID(*newSgpEventOne))
		assert.NoError(t, err, "")
	}
}

func updateTestNodeUID(event eventsv1.Event) *eventsv1.Event {
	copy := event.DeepCopy()
	copy.Related.UID = types.UID(uuid.New().String())
	return copy
}

func arrayToChannel(array []watch.Event) chan watch.Event {
	ch := make(chan watch.Event, len(array))

	for _, event := range array {
		ch <- event
	}

	return ch
}

func makeTestEvent(e *eventsv1.Event) watch.Event {
	return watch.Event{
		Type:   watch.Added,
		Object: e,
	}
}
