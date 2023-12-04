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

package cooldown

import (
	"fmt"
	"testing"
	"time"

	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var log = zap.New(zap.UseDevMode(true)).WithName("cooldown test")
var (
	mockConfigMap30s = &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: config.VpcCniConfigMapName, Namespace: config.KubeSystemNamespace},
		Data:       map[string]string{config.BranchENICooldownPeriodKey: "30"},
	}
	mockConfigMapNilData = &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: config.VpcCniConfigMapName, Namespace: config.KubeSystemNamespace},
		Data:       map[string]string{},
	}
	mockConfigMapErrData = &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: config.VpcCniConfigMapName, Namespace: config.KubeSystemNamespace},
		Data:       map[string]string{config.BranchENICooldownPeriodKey: "aaa"},
	}
)

func TestCoolDown_InitCoolDownPeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	type args struct {
		vpcCniConfigMap *corev1.ConfigMap
	}
	tests := []struct {
		name             string
		args             args
		expectedCoolDown time.Duration
		err              error
	}{
		{
			name:             "VpcCniConfigMap_Exists, verifies cooldown period is set to configmap value when exists",
			args:             args{vpcCniConfigMap: mockConfigMap30s},
			expectedCoolDown: time.Second * 30,
			err:              nil,
		},
		{
			name:             "VpcCniConfigMap_NotExists, verifies cool down period is set to default when configmap does not exist",
			args:             args{},
			expectedCoolDown: time.Second * 60,
			err:              fmt.Errorf("mock error"),
		},
		{
			name:             "VpcCniConfigMap_Exists_NilData, verifies cool period is set to default when configmap data does not exist",
			args:             args{vpcCniConfigMap: mockConfigMapNilData},
			expectedCoolDown: time.Second * 60,
			err:              nil,
		},
		{
			name:             "VpcCniConfigMap_Exists_ErrData, verifies cool period is set to default when configmap data is incorrect",
			args:             args{vpcCniConfigMap: mockConfigMapErrData},
			expectedCoolDown: time.Second * 60,
			err:              nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl = gomock.NewController(t)
			defer ctrl.Finish()
		})
		mockK8sApi := mock_k8s.NewMockK8sWrapper(ctrl)
		mockK8sApi.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(test.args.vpcCniConfigMap, test.err)
		InitCoolDownPeriod(mockK8sApi, log)
		assert.Equal(t, test.expectedCoolDown, coolDown.coolDownPeriod)
	}
}
