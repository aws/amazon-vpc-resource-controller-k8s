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

package resource

import (
	"context"
	"testing"

	mock_handler "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/handler"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Mock struct {
	Handler *mock_handler.MockHandler
	Wrapper api.Wrapper
}

var healthzHandler = healthz.NewHealthzHandler(5)

func NewMock(controller *gomock.Controller) Mock {
	return Mock{
		Handler: mock_handler.NewMockHandler(controller),
		Wrapper: api.Wrapper{},
	}
}

func Test_NewResourceManager_WinPDFeatureON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl)
	resources := []string{config.ResourceNamePodENI, config.ResourceNameIPAddress, config.ResourceNameIPAddressFromPrefix}

	mockK8s := mock_k8s.NewMockK8sWrapper(ctrl)
	conditions := condition.NewControllerConditions(zap.New(), mockK8s, true)
	manger, err := NewResourceManager(context.TODO(), resources, mock.Wrapper, zap.New(zap.UseDevMode(true)), healthzHandler, conditions)
	assert.NoError(t, err)

	_, ok := manger.GetResourceHandler(config.ResourceNamePodENI)
	assert.True(t, ok)

	_, ok = manger.GetResourceHandler(config.ResourceNameIPAddress)
	assert.True(t, ok)

	_, ok = manger.GetResourceHandler(config.ResourceNameIPAddressFromPrefix)
	assert.True(t, ok)

	providers := manger.GetResourceProviders()
	assert.Equal(t, len(providers), 3)

	_, ok = manger.GetResourceProvider(config.ResourceNamePodENI)
	assert.True(t, ok)

	_, ok = manger.GetResourceProvider(config.ResourceNameIPAddress)
	assert.True(t, ok)

	_, ok = manger.GetResourceProvider(config.ResourceNameIPAddressFromPrefix)
	assert.True(t, ok)
}

func Test_NewResourceManager_Test_NewResourceManager_WinPDFeatureOFF(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl)
	resources := []string{config.ResourceNamePodENI, config.ResourceNameIPAddress}

	mockK8s := mock_k8s.NewMockK8sWrapper(ctrl)
	conditions := condition.NewControllerConditions(zap.New(), mockK8s, false)
	manger, err := NewResourceManager(context.TODO(), resources, mock.Wrapper, zap.New(zap.UseDevMode(true)), healthzHandler, conditions)
	assert.NoError(t, err)

	_, ok := manger.GetResourceHandler(config.ResourceNamePodENI)
	assert.True(t, ok)

	_, ok = manger.GetResourceHandler(config.ResourceNameIPAddress)
	assert.True(t, ok)

	// Resource handler for prefix IP should not exist
	_, ok = manger.GetResourceHandler(config.ResourceNameIPAddressFromPrefix)
	assert.False(t, ok)

	// Resource provider for prefix IP should not exist
	providers := manger.GetResourceProviders()
	assert.Equal(t, len(providers), 2)

	_, ok = manger.GetResourceProvider(config.ResourceNamePodENI)
	assert.True(t, ok)

	_, ok = manger.GetResourceProvider(config.ResourceNameIPAddress)
	assert.True(t, ok)

	_, ok = manger.GetResourceProvider(config.ResourceNameIPAddressFromPrefix)
	assert.False(t, ok)
}
