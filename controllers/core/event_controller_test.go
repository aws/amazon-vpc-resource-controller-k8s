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
	"testing"
	"time"

	"github.com/allegro/bigcache/v3"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	mockSGPEventName         = "node-sgp-event"
	mockCNEventName          = "node-custom-networking-event"
	mockEventNodeNameOne     = "ip-0-0-0-1.us-west-2.compute.internal"
	mockEventNodeNameTwo     = "ip-0-0-0-2.us-west-2.compute.internal"
	mockEventNodeNameThree   = "ip-0-0-0-3.us-west-2.compute.internal"
	sgpEventReconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      mockSGPEventName,
			Namespace: config.KubeSystemNamespace,
		},
	}
	eniConfigEventReconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      mockCNEventName,
			Namespace: config.KubeSystemNamespace,
		},
	}

	oldSgpEvent = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 3)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeNameOne,
			UID:  types.UID("i-00000000000000001"),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Note:                config.TrunkNotAttached,
		Action:              config.VpcCNINodeEventActionForTrunk,
	}
	newSgpEventOne = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeNameTwo,
			UID:  types.UID("i-00000000000000002"),
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Note:                config.TrunkNotAttached,
		Action:              config.VpcCNINodeEventActionForTrunk,
	}
	newSgpEventTwo = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeNameThree,
			UID:  types.UID("i-00000000000000003"),
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
			Name: mockEventNodeNameOne,
			UID:  types.UID("i-00000000000000001"),
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
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeNameTwo,
			UID:  types.UID("i-00000000000000002"),
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
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeNameThree,
			UID:  types.UID("i-00000000000000003"),
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

type EventMock struct {
	MockK8sAPI *mock_k8s.MockK8sWrapper
	Reconciler EventReconciler
}

func NewEventControllerMock(ctrl *gomock.Controller, mockObjects ...runtime.Object) EventMock {
	scheme := runtime.NewScheme()
	_ = eventsv1.AddToScheme(scheme)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	testCache, _ := bigcache.NewBigCache(bigcache.DefaultConfig(testCacheExpiry))
	return EventMock{
		MockK8sAPI: mockK8sAPI,
		Reconciler: EventReconciler{
			Scheme: scheme,
			Log:    zap.New(),
			K8sAPI: mockK8sAPI,
			cache:  testCache,
		},
	}
}

func TestEventReconciler_Reconcile_SGPEvent(t *testing.T) {
	var events = []struct {
		eventList             *eventsv1.EventList
		isValidEventForSGP    bool
		successfullyLabelNode bool
		testNodeName          string
		testNodeKey           string
		msg                   string
		hitCache              bool
		missCache             bool
		evictCache            bool
	}{
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *oldSgpEvent),
			},
			isValidEventForSGP:    false,
			successfullyLabelNode: false,
			testNodeName:          mockEventNodeNameOne,
			testNodeKey:           string(oldSgpEvent.Regarding.UID),
			msg:                   "SGP Cache Test: Expired event is not valid event",
			hitCache:              false,
			missCache:             true,
			evictCache:            false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEventOne),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: true,
			testNodeName:          mockEventNodeNameTwo,
			testNodeKey:           string(newSgpEventOne.Regarding.UID),
			msg:                   "SGP Cache Test: Valid event one, label node",
			hitCache:              false,
			missCache:             true,
			evictCache:            false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEventTwo),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: false,
			testNodeName:          mockEventNodeNameThree,
			testNodeKey:           string(newSgpEventTwo.Regarding.UID),
			msg:                   "SGP Cache Test: Valid event two, fail labelling node",
			hitCache:              false,
			missCache:             true,
			evictCache:            false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEventTwo),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: true,
			testNodeName:          mockEventNodeNameThree,
			testNodeKey:           string(newSgpEventTwo.Regarding.UID),
			msg:                   "SGP Cache Test: Valid event two, label node",
			hitCache:              false,
			missCache:             true,
			evictCache:            false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEventTwo),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: true,
			testNodeName:          mockEventNodeNameThree,
			testNodeKey:           string(newSgpEventTwo.Regarding.UID),
			msg:                   "SGP Cache Test: Valid event two, label node, cache hit",
			hitCache:              true,
			missCache:             false,
			evictCache:            false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEventTwo),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: true,
			testNodeName:          mockEventNodeNameThree,
			testNodeKey:           string(newSgpEventTwo.Regarding.UID),
			msg:                   "SGP Cache Test: Valid event, label node, cache expired and should be miss",
			hitCache:              false,
			missCache:             true,
			evictCache:            true,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEventTwo),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: true,
			testNodeName:          mockEventNodeNameThree,
			testNodeKey:           string(newSgpEventTwo.Regarding.UID),
			msg:                   "SGP Cache Test: Valid event, label node, cache expired and should be miss",
			hitCache:              false,
			missCache:             true,
			evictCache:            true,
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewEventControllerMock(ctrl)
	ops := []client.ListOption{
		client.MatchingFields{
			EventFilterKey: config.VpcCNIReportingAgent,
		},
	}

	for _, e := range events {
		if e.evictCache {
			time.Sleep(testCacheExpiry + testWaitCacheEvict)
		}
		mock.MockK8sAPI.EXPECT().ListEvents(ops).Return(e.eventList, nil)

		_, cacheErr := mock.Reconciler.cache.Get(e.testNodeKey)

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
			eventNode := &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   e.testNodeName,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux},
				},
			}
			mock.MockK8sAPI.EXPECT().GetNode(e.testNodeName).Return(eventNode, nil).AnyTimes()
			if cacheErr == nil || e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).AnyTimes()
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(false, errors.New("sgp-test"))
			}
		}
		res, err := mock.Reconciler.Reconcile(context.TODO(), sgpEventReconcileRequest)

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

		assert.Equal(t, res, reconcile.Result{}, e.msg)
	}
}

func TestEventReconciler_Reconcile_ENIConfigLabelNodeEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mock := NewEventControllerMock(ctrl)

	ops := []client.ListOption{
		client.MatchingFields{
			EventFilterKey: config.VpcCNIReportingAgent,
		},
	}

	var events = []struct {
		eventList                       *eventsv1.EventList
		isValidEventForCustomNetworking bool
		successfullyLabelNode           bool
		testNodeName                    string
		testNodeKey                     string
		msg                             string
		hitCache                        bool
		missCache                       bool
		evictCache                      bool
	}{
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *oldEniConfigEvent),
			},
			isValidEventForCustomNetworking: false,
			successfullyLabelNode:           false,
			testNodeName:                    mockEventNodeNameOne,
			testNodeKey:                     string(oldEniConfigEvent.Regarding.UID),
			msg:                             "Custom Networking Cache Test: Expired event is not valid event",
			hitCache:                        false,
			missCache:                       true,
			evictCache:                      false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEventOne),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           true,
			testNodeName:                    mockEventNodeNameTwo,
			testNodeKey:                     string(newEniConfigEventOne.Regarding.UID),
			msg:                             "Custom Networking Cache Test: Valid event one, label node",
			hitCache:                        false,
			missCache:                       true,
			evictCache:                      false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEventTwo),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           false,
			testNodeName:                    mockEventNodeNameThree,
			testNodeKey:                     string(newEniConfigEventTwo.Regarding.UID),
			msg:                             "Custom Networking Cache Test: Valid event two, fail labelling node",
			hitCache:                        false,
			missCache:                       true,
			evictCache:                      false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEventTwo),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           true,
			testNodeName:                    mockEventNodeNameThree,
			testNodeKey:                     string(newEniConfigEventTwo.Regarding.UID),
			msg:                             "Custom Networking Cache Test: Valid event two, label node",
			hitCache:                        false,
			missCache:                       true,
			evictCache:                      false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEventTwo),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           true,
			testNodeName:                    mockEventNodeNameThree,
			testNodeKey:                     string(newEniConfigEventTwo.Regarding.UID),
			msg:                             "Custom Networking Cache Test: Valid event two, label node, cache hit",
			hitCache:                        true,
			missCache:                       false,
			evictCache:                      false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEventTwo),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           true,
			testNodeName:                    mockEventNodeNameThree,
			testNodeKey:                     string(newEniConfigEventTwo.Regarding.UID),
			msg:                             "Custom Networking Cache Test: Valid event, label node, cache expired and should be miss",
			hitCache:                        false,
			missCache:                       true,
			evictCache:                      true,
		},
	}

	for _, e := range events {
		if e.evictCache {
			time.Sleep(testCacheExpiry + testWaitCacheEvict)
		}
		mock.MockK8sAPI.EXPECT().ListEvents(ops).Return(e.eventList, nil)

		_, cacheErr := mock.Reconciler.cache.Get(e.testNodeKey)

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
			// if the event is older, these func are not expected to be called.
			eventNode := &corev1.Node{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:   e.testNodeName,
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "true"},
				},
			}
			mock.MockK8sAPI.EXPECT().GetNode(e.testNodeName).Return(eventNode, nil).AnyTimes()
			if cacheErr == nil || e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).AnyTimes()
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(false, errors.New("custom-networking-test"))
			}
		}

		res, err := mock.Reconciler.Reconcile(context.TODO(), eniConfigEventReconcileRequest)

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
		assert.Equal(t, res, reconcile.Result{})
	}
}
