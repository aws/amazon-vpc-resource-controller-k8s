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

package pool

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
)

func TestGetWinWarmPoolConfig_PDDisabledAndAPICallSuccess_ReturnsConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := zap.New(zap.UseDevMode(true)).WithName("provider test")

	configMapToReturn := &v1.ConfigMap{
		Data: map[string]string{
			config.EnableWindowsIPAMKey:             "true",
			config.EnableWindowsPrefixDelegationKey: "true",
			config.WinWarmIPTarget:                  strconv.Itoa(config.IPv4DefaultWinWarmIPTarget),
			config.WinMinimumIPTarget:               strconv.Itoa(config.IPv4DefaultWinMinIPTarget),
		},
	}
	expectedWarmPoolConfig := &config.WarmPoolConfig{
		WarmIPTarget: config.IPv4DefaultWinWarmIPTarget,
		MinIPTarget:  config.IPv4DefaultWinMinIPTarget,
		DesiredSize:  config.IPv4DefaultWinWarmIPTarget,
	}

	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sWrapper.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(configMapToReturn, nil)
	apiWrapperMock := api.Wrapper{K8sAPI: mockK8sWrapper}

	actualWarmPoolConfig := GetWinWarmPoolConfig(log, apiWrapperMock, false)
	assert.Equal(t, expectedWarmPoolConfig, actualWarmPoolConfig)
}

func TestGetWinWarmPoolConfig_PDEnabledAndAPICallSuccess_ReturnsConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := zap.New(zap.UseDevMode(true)).WithName("provider test")

	configMapToReturn := &v1.ConfigMap{
		Data: map[string]string{
			config.EnableWindowsIPAMKey:             "true",
			config.EnableWindowsPrefixDelegationKey: "true",
			config.WinWarmIPTarget:                  strconv.Itoa(config.IPv4PDDefaultWarmIPTargetSize),
			config.WinWarmPrefixTarget:              strconv.Itoa(config.IPv4PDDefaultWarmPrefixTargetSize),
			config.WinMinimumIPTarget:               strconv.Itoa(config.IPv4PDDefaultMinIPTargetSize),
		},
	}
	expectedWarmPoolConfig := &config.WarmPoolConfig{
		WarmIPTarget:     config.IPv4PDDefaultWarmIPTargetSize,
		WarmPrefixTarget: config.IPv4PDDefaultWarmPrefixTargetSize,
		MinIPTarget:      config.IPv4PDDefaultMinIPTargetSize,
		DesiredSize:      config.IPv4PDDefaultWarmIPTargetSize,
	}

	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sWrapper.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(configMapToReturn, nil)
	apiWrapperMock := api.Wrapper{K8sAPI: mockK8sWrapper}

	actualWarmPoolConfig := GetWinWarmPoolConfig(log, apiWrapperMock, true)
	assert.Equal(t, expectedWarmPoolConfig, actualWarmPoolConfig)
}

func TestGetWinWarmPoolConfig_PDDisabledAndAPICallFailure_ReturnsDefaultConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := zap.New(zap.UseDevMode(true)).WithName("provider test")

	var configMapToReturn *v1.ConfigMap = nil
	errorToReturn := fmt.Errorf("Some error occurred while fetching config map")
	expectedWarmPoolConfig := &config.WarmPoolConfig{
		WarmIPTarget: config.IPv4DefaultWinWarmIPTarget,
		MinIPTarget:  config.IPv4DefaultWinMinIPTarget,
		DesiredSize:  config.IPv4DefaultWinWarmIPTarget,
	}

	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sWrapper.EXPECT().GetConfigMap(
		config.VpcCniConfigMapName,
		config.KubeSystemNamespace,
	).Return(
		configMapToReturn,
		errorToReturn,
	)
	apiWrapperMock := api.Wrapper{K8sAPI: mockK8sWrapper}

	actualWarmPoolConfig := GetWinWarmPoolConfig(log, apiWrapperMock, false)
	assert.Equal(t, expectedWarmPoolConfig, actualWarmPoolConfig)
}
