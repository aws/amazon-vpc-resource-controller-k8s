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

package condition

import (
	"testing"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	deployment  = &appsv1.Deployment{}
	notFoundErr = &errors.StatusError{
		ErrStatus: metav1.Status{
			Reason: metav1.StatusReasonNotFound,
		},
	}
	otherErr = &errors.StatusError{
		ErrStatus: metav1.Status{
			Reason: metav1.StatusFailure,
		},
	}
	vpcCNIConfig = &v1.ConfigMap{
		Data: map[string]string{
			config.EnableWindowsIPAMKey: "true",
		},
	}
)

func Test_WaitTillPodDataStoreSynced(t *testing.T) {
	syncVariable := new(bool)
	c := condition{
		log:                zap.New(zap.UseDevMode(true)),
		hasDataStoreSynced: syncVariable,
	}

	var hasSynced bool
	go func() {
		c.WaitTillPodDataStoreSynced()
		hasSynced = true
	}()

	time.Sleep(CheckDataStoreSyncedInterval * 2)
	// If hasSynced is True then the function didn't wait for the
	// cache to be synced
	assert.False(t, hasSynced)

	// Set the variable so the previous go routine stops
	// and next call to the function doesn't block the test
	*syncVariable = true

	// If the code was buggy, this execution would block and
	// timeout the test
	c.WaitTillPodDataStoreSynced()
}

func TestCondition_IsWindowsIPAMEnabled(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
		mock     func(mock *mock_k8s.MockK8sWrapper)
	}{
		{
			name:     "old controller deployment not deleted",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(deployment, nil)
			},
		},
		{
			name:     "getting old controller deployment returns error other than is not found",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(nil, otherErr)
			},
		},
		{
			name:     "get configmap returns error",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.VpcCNIConfigMapNamespace).Return(nil, otherErr)
			},
		},
		{
			name:     "configmap with no data",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				noData := vpcCNIConfig.DeepCopy()
				noData.Data = nil

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.VpcCNIConfigMapNamespace).Return(noData, nil)
			},
		},
		{
			name:     "configmap with flag set to false",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				falseData := vpcCNIConfig.DeepCopy()
				falseData.Data[config.EnableWindowsIPAMKey] = "false"

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.VpcCNIConfigMapNamespace).Return(falseData, nil)
			},
		},
		{
			name:     "configmap with flag set to non boolean value",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				nonParsable := vpcCNIConfig.DeepCopy()
				nonParsable.Data[config.EnableWindowsIPAMKey] = "trued"

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.VpcCNIConfigMapNamespace).Return(nonParsable, nil)
			},
		},
		{
			name:     "configmap with flag set to true",
			expected: true,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.OldVPCControllerDeploymentNS,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.VpcCNIConfigMapNamespace).Return(vpcCNIConfig, nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockK8s := mock_k8s.NewMockK8sWrapper(ctrl)
			conditions := NewControllerConditions(nil, zap.New(), mockK8s)

			test.mock(mockK8s)

			assert.Equal(t, conditions.IsWindowsIPAMEnabled(), test.expected)
		})
	}
}
