// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// A copy of the License is located at
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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// The real manager reads the same cache as NodeReconciler. Deleting a Node
// must still reach DeleteNode even though that cache no longer contains it.
func TestNodeReconciler_DeleteReachesRealManager(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: mockNodeName,
			UID:  "uid-a",
			Labels: map[string]string{
				config.NodeLabelOS:           config.OSLinux,
				config.HasTrunkAttachedLabel: "true",
			},
		},
		Spec: corev1.NodeSpec{ProviderID: "aws:///us-west-2a/i-0aaaaaaaaaaaaaaaa"},
	}
	cniNode := &v1alpha1.CNINode{
		ObjectMeta: metav1.ObjectMeta{
			Name: mockNodeName,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1", Kind: "Node", Name: mockNodeName, UID: "uid-a",
			}},
		},
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	assert.NoError(t, v1alpha1.AddToScheme(scheme))
	client := fakeClient.NewClientBuilder().WithScheme(scheme).WithObjects(node, cniNode).Build()
	k8sAPI := k8s.NewK8sWrapper(client, client, k8sfake.NewSimpleClientset().CoreV1(), context.Background())

	worker := mock_worker.NewMockWorker(ctrl)
	worker.EXPECT().StartWorkerPool(gomock.Any()).Return(nil)
	submitted := 0
	worker.EXPECT().SubmitJob(gomock.Any()).Do(func(interface{}) { submitted++ }).AnyTimes()
	conditions := mock_condition.NewMockConditions(ctrl)
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true).AnyTimes()

	nodeManager, err := manager.NewNodeManager(zap.New(), mock_resource.NewMockResourceManager(ctrl),
		api.Wrapper{K8sAPI: k8sAPI, EC2API: mock_api.NewMockEC2APIHelper(ctrl)}, worker, conditions,
		"cluster", "v-test", rcHealthz.NewHealthzHandler(5))
	assert.NoError(t, err)

	reconciler := NodeReconciler{
		Scheme: scheme, Client: client, Log: zap.New(), Manager: nodeManager, Conditions: conditions,
	}
	_, err = reconciler.Reconcile(context.Background(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, 1, submitted)

	assert.NoError(t, client.Delete(context.Background(), node))
	_, err = reconciler.Reconcile(context.Background(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, 2, submitted, "Node deletion must submit the manager Delete job")
}
