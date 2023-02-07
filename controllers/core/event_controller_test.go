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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	mockNodeEventReqName   = "ip-192-168-0-0.us-west-2.compute.internal.1741bdaa24dc9119"
	mockSGPEventName       = "node-sgp-event"
	mockCNEventName        = "node-custom-networking-event"
	mockEventNodeNameOne   = "ip-0-0-0-1.us-west-2.compute.internal"
	mockEventNodeNameTwo   = "ip-0-0-0-2.us-west-2.compute.internal"
	mockEventNodeNameThree = "ip-0-0-0-3.us-west-2.compute.internal"
	mockInstanceIdOne      = "i-00000000000000001"
	mockInstanceIdTwo      = "i-00000000000000002"
	mockInstanceIdThree    = "i-00000000000000003"

	nodeEventReconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      mockNodeEventReqName,
			Namespace: corev1.NamespaceDefault,
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
			UID:  types.UID(mockInstanceIdOne),
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
			UID:  types.UID(mockInstanceIdTwo),
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
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
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
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
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
	}{
		{
			event:                 oldSgpEvent,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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
			event:                 newSgpEventOne,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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
			event:                 newSgpEventTwo,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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
			event:                 newSgpEventTwo,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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
			event:                 newSgpEventTwo,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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
			event:                 newSgpEventTwo,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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
			event:                 newSgpEventTwo,
			namespacedName:        nodeEventReconcileRequest.NamespacedName,
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

	for _, e := range events {
		if e.evictCache {
			time.Sleep(testCacheExpiry + testWaitCacheEvict)
		}
		mock.MockK8sAPI.EXPECT().GetEvent(nodeEventReconcileRequest.NamespacedName).Return(e.event, nil)

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
		res, err := mock.Reconciler.Reconcile(context.TODO(), nodeEventReconcileRequest)

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

	var events = []struct {
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
	}{
		{
			event:                           oldEniConfigEvent,
			namespacedName:                  nodeEventReconcileRequest.NamespacedName,
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
			event:                           newEniConfigEventOne,
			namespacedName:                  nodeEventReconcileRequest.NamespacedName,
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
			event:                           newEniConfigEventTwo,
			namespacedName:                  nodeEventReconcileRequest.NamespacedName,
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
			event:                           newEniConfigEventTwo,
			namespacedName:                  nodeEventReconcileRequest.NamespacedName,
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
			event:                           newEniConfigEventTwo,
			namespacedName:                  nodeEventReconcileRequest.NamespacedName,
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
			event:                           newEniConfigEventTwo,
			namespacedName:                  nodeEventReconcileRequest.NamespacedName,
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
		mock.MockK8sAPI.EXPECT().GetEvent(nodeEventReconcileRequest.NamespacedName).Return(e.event, nil)

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
					Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "false"},
				},
			}
			mock.MockK8sAPI.EXPECT().GetNode(e.testNodeName).Return(eventNode, nil).AnyTimes()
			if cacheErr == nil || e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).AnyTimes()
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(false, errors.New("custom-networking-test"))
			}
		}

		res, err := mock.Reconciler.Reconcile(context.TODO(), nodeEventReconcileRequest)

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
			Name:   mockEventNodeNameTwo,
			Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "false"},
		},
	}

	events := []*eventsv1.Event{newSgpEventOne, newEniConfigEventOne}

	times := 2
	var err error
	var res reconcile.Result
	for i := 0; i < times; i++ {
		for _, value := range events {
			// calls should be made only twice since after one SGP event and one Custom networking event other events for the same instance should be ignored
			// due to cache hit
			expectedCallTimes := times - i - 1
			mock.MockK8sAPI.EXPECT().GetEvent(nodeEventReconcileRequest.NamespacedName).Return(value, nil).Times(1)
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, gomock.Any(), gomock.Any()).Return(true, nil).Times(expectedCallTimes)
			res, err = mock.Reconciler.Reconcile(context.TODO(), nodeEventReconcileRequest)
			assert.NoError(t, err, "Reconcile has no error for test event", value)
		}
	}
	assert.NoError(t, err, "Reconcile has no error for dual events tests")
	assert.True(t, res.Requeue == false, "Reconcile has no requeue for dual events tests")
	assert.True(t, mock.Reconciler.cache.Len() == 1)
	cachedEntry, err := mock.Reconciler.cache.Get(mockInstanceIdTwo)
	assert.NoError(t, err, "Should get entry from test cache successfully")
	assert.True(t, cachedEntry[EnableSGP] == 1, "SGP is cached")
	assert.True(t, cachedEntry[EnableCN] == 1, "Custom networking is cached")
}

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
			nn:    nodeEventReconcileRequest.NamespacedName,
			sgp:   true,
			msg:   "SGP event one",
		},
		{
			event: newEniConfigEventOne,
			nn:    nodeEventReconcileRequest.NamespacedName,
			cn:    true,
			msg:   "Custom networking event one",
		},
		{
			event: newSgpEventOne,
			nn:    nodeEventReconcileRequest.NamespacedName,
			sgp:   true,
			msg:   "SGP event one",
		},
		{
			event: newEniConfigEventOne,
			nn:    nodeEventReconcileRequest.NamespacedName,
			cn:    true,
			msg:   "Custom networking event one",
		},
	}

	var expectedCallTimes int
	for idx, e := range events {
		eventNode := &corev1.Node{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:   mockEventNodeNameTwo,
				Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "false"},
			},
		}
		mock.MockK8sAPI.EXPECT().GetEvent(e.nn).Return(e.event, nil)
		switch idx {
		case 0:
			expectedCallTimes = 1
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			if e.sgp {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).Times(expectedCallTimes)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).Times(expectedCallTimes)
			}
			_, err := mock.Reconciler.cache.Get(mockInstanceIdTwo)
			assert.Error(t, err)
			assert.True(t, mock.Reconciler.cache.Len() == 0)
		case 1:
			expectedCallTimes = 1
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeNameTwo).Return(eventNode, nil).Times(expectedCallTimes)
			if e.sgp {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil).Times(expectedCallTimes)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil).Times(expectedCallTimes)
			}
			entry, err := mock.Reconciler.cache.Get(mockInstanceIdTwo)
			assert.NoError(t, err)
			assert.True(t, mock.Reconciler.cache.Len() == 1)
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
			entry, err := mock.Reconciler.cache.Get(mockInstanceIdTwo)
			assert.NoError(t, err)
			assert.True(t, mock.Reconciler.cache.Len() == 1)
			assert.True(t, entry[EnableSGP] == 1 && entry[EnableCN] == 1, "Cache hit")
		}

		mock.Reconciler.Reconcile(context.TODO(), nodeEventReconcileRequest)
	}
}
