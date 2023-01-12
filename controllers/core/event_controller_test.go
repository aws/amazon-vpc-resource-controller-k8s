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
	mockEventNodeName        = "ip-0-0-0-0.us-west-2.compute.internal"
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
			Name: mockEventNodeName,
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Note:                config.TrunkNotAttached,
		Action:              config.VpcCNINodeEventActionForTrunk,
	}
	newSgpEvent = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockSGPEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeName,
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
			Name: mockEventNodeName,
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Action:              config.VpcCNINodeEventActionForEniConfig,
		Note:                config.CustomNetworkingLabel + "=testConfig",
	}
	newEniConfigEvent = &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              mockCNEventName,
			Namespace:         config.KubeSystemNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute * 1)},
		},
		Regarding: corev1.ObjectReference{
			Kind: EventRegardingKind,
			Name: mockEventNodeName,
		},
		ReportingController: config.VpcCNIReportingAgent,
		Reason:              config.VpcCNINodeEventReason,
		Action:              config.VpcCNINodeEventActionForEniConfig,
		Note:                config.CustomNetworkingLabel + "=testConfig",
	}

	eventNode = &corev1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   mockEventNodeName,
			Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "true"},
		},
	}
)

type EventMock struct {
	MockK8sAPI *mock_k8s.MockK8sWrapper
	Reconciler EventReconciler
}

func NewEventControllerMock(ctrl *gomock.Controller, mockObjects ...runtime.Object) EventMock {
	scheme := runtime.NewScheme()
	_ = eventsv1.AddToScheme(scheme)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	return EventMock{
		MockK8sAPI: mockK8sAPI,
		Reconciler: EventReconciler{
			Scheme: scheme,
			Log:    zap.New(),
			K8sAPI: mockK8sAPI,
		},
	}
}

func TestEventReconciler_Reconcile_SGPEvent(t *testing.T) {
	var events = []struct {
		eventList             *eventsv1.EventList
		isValidEventForSGP    bool
		successfullyLabelNode bool
	}{
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *oldSgpEvent),
			},
			isValidEventForSGP:    false,
			successfullyLabelNode: false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEvent),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: true,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newSgpEvent),
			},
			isValidEventForSGP:    true,
			successfullyLabelNode: false,
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
		mock.MockK8sAPI.EXPECT().ListEvents(ops).Return(e.eventList, nil)

		if e.isValidEventForSGP {
			// if the event is older, these func are not expected to be called.
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeName).Return(eventNode, nil).AnyTimes()
			if e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(true, nil)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.HasTrunkAttachedLabel, config.BooleanFalse).Return(false, errors.New("sgp-test"))
			}
		}
		res, err := mock.Reconciler.Reconcile(context.TODO(), sgpEventReconcileRequest)

		if e.successfullyLabelNode {
			assert.NoError(t, err)
		} else if e.isValidEventForSGP && !e.successfullyLabelNode {
			assert.Error(t, err)
			assert.EqualError(t, err, "sgp-test")
		} else {
			assert.NoError(t, err)
		}
		assert.Equal(t, res, reconcile.Result{})
	}
}

func TestEventReconciler_Reconcile_ENIConfigLabelNodeEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ops := []client.ListOption{
		client.MatchingFields{
			EventFilterKey: config.VpcCNIReportingAgent,
		},
	}

	var events = []struct {
		eventList                       *eventsv1.EventList
		isValidEventForCustomNetworking bool
		successfullyLabelNode           bool
	}{
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *oldEniConfigEvent),
			},
			isValidEventForCustomNetworking: false,
			successfullyLabelNode:           false,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEvent),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           true,
		},
		{
			eventList: &eventsv1.EventList{
				Items: append([]eventsv1.Event{}, *newEniConfigEvent),
			},
			isValidEventForCustomNetworking: true,
			successfullyLabelNode:           false,
		},
	}

	for _, e := range events {
		mock := NewEventControllerMock(ctrl)
		mock.MockK8sAPI.EXPECT().ListEvents(ops).Return(e.eventList, nil)

		if e.isValidEventForCustomNetworking {
			// if the event is older, these func are not expected to be called.
			mock.MockK8sAPI.EXPECT().GetNode(mockEventNodeName).Return(eventNode, nil)
			if e.successfullyLabelNode {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(true, nil)
			} else {
				mock.MockK8sAPI.EXPECT().AddLabelToManageNode(eventNode, config.CustomNetworkingLabel, "testConfig").Return(false, errors.New("custom-networking-test"))
			}
		}

		res, err := mock.Reconciler.Reconcile(context.TODO(), eniConfigEventReconcileRequest)

		if e.successfullyLabelNode {
			assert.NoError(t, err)
		} else if e.isValidEventForCustomNetworking && !e.successfullyLabelNode {
			assert.Error(t, err)
			assert.EqualError(t, err, "custom-networking-test")
		} else {
			assert.NoError(t, err)
		}
		assert.Equal(t, res, reconcile.Result{})
	}
}
