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
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
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
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(deployment, nil)
			},
		},
		{
			name:     "getting old controller deployment returns error other than is not found",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(nil, otherErr)
			},
		},
		{
			name:     "get configmap returns error",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.KubeSystemNamespace).Return(nil, otherErr)
			},
		},
		{
			name:     "configmap with no data",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				noData := vpcCNIConfig.DeepCopy()
				noData.Data = nil

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.KubeSystemNamespace).Return(noData, nil)
			},
		},
		{
			name:     "configmap with flag set to false",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				falseData := vpcCNIConfig.DeepCopy()
				falseData.Data[config.EnableWindowsIPAMKey] = "false"

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.KubeSystemNamespace).Return(falseData, nil)
			},
		},
		{
			name:     "configmap with flag set to non boolean value",
			expected: false,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				nonParsable := vpcCNIConfig.DeepCopy()
				nonParsable.Data[config.EnableWindowsIPAMKey] = "trued"

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.KubeSystemNamespace).Return(nonParsable, nil)
			},
		},
		{
			name:     "configmap with flag set to true",
			expected: true,
			mock: func(mock *mock_k8s.MockK8sWrapper) {
				mock.EXPECT().GetDeployment(config.KubeSystemNamespace,
					config.OldVPCControllerDeploymentName).Return(nil, notFoundErr)

				mock.EXPECT().GetConfigMap(config.VpcCniConfigMapName,
					config.KubeSystemNamespace).Return(vpcCNIConfig, nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockK8s := mock_k8s.NewMockK8sWrapper(ctrl)
			conditions := NewControllerConditions(zap.New(), mockK8s, false)

			test.mock(mockK8s)

			assert.Equal(t, conditions.IsWindowsIPAMEnabled(), test.expected)
		})
	}
}

// TestCondition_GetPodDataStoreSyncStatus tests two group of routines which are setting (write) the sync flag field
// and are getting (read) the field.
// In real case, pod controller routines keep checking the cache status and set the sync field to true if cache is ready.
// At the same time, node controller routines keep asking if the pod cache is ready by Getting the status.
// Until pod cache is ready, node controllers won't make any process but requeue coming requests exponenially
// To simulate the case, the test cases create a routine as writer (pod routine) and routines as reader (node routines)
// to request the lock on the field. By using these tests, we can preliminarily test the possibility of lock issue or
// racing by safeguarding a processing time window.
func TestCondition_GetPodDataStoreSyncStatus(t *testing.T) {
	tests := []struct {
		name            string
		testBlockedTime int
		// buffer time is used to consider other execution time during the process
		testBufferTime int
		workers        int
	}{
		{
			name:            "worker doesn't wait long enough",
			testBlockedTime: 5,
			testBufferTime:  -1,
			workers:         1,
		},
		{
			name:            "worker waits with a buffer time",
			testBlockedTime: 5,
			testBufferTime:  1,
			workers:         1,
		},
		{
			name:            "three workers check on the flag",
			testBlockedTime: 5,
			testBufferTime:  1,
			workers:         3,
		},
		{
			name:            "large number workers check on the flag",
			testBlockedTime: 5,
			testBufferTime:  1,
			workers:         30, // we want to use an unusual large workers to test routines conflict
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockK8s := mock_k8s.NewMockK8sWrapper(ctrl)
			conditions := NewControllerConditions(zap.New(), mockK8s, false)
			start := time.Now()

			// one routine is keeping the flag as false for 5s and then updates it to true
			go func() {
				waitFor := 0
				for waitFor < test.testBlockedTime {
					time.Sleep(time.Second * 1)
					conditions.SetPodDataStoreSyncStatus(false)
					waitFor++
				}
				conditions.SetPodDataStoreSyncStatus(true)
			}()

			var wg sync.WaitGroup
			wg.Add(test.workers)

			for i := 0; i < test.workers; i++ {
				go func(id int) {
					defer wg.Done()
					worker := "worker-" + strconv.Itoa(id)
					var synced bool
					// this routine is checking if the flag is updated and how long the process took
					// the processing time should be very small longer than required waiting time, otherwise fail the test
					for {
						synced = conditions.GetPodDataStoreSyncStatus()
						if synced {
							duration := int(time.Since(start).Seconds())
							if test.testBufferTime >= 0 {
								assert.GreaterOrEqual(t, test.testBlockedTime+test.testBufferTime, duration, fmt.Sprintf("worker %s Get the cache flag. Set and Get pod cache synced flag were not blocked", worker))
							} else {
								assert.GreaterOrEqual(t, duration, test.testBlockedTime+test.testBufferTime, fmt.Sprintf("worker %s didn't wait long enough, routines are not blocked", worker))
							}
							break
						} else {
							time.Sleep(time.Second * 1)
						}
					}
				}(i)
			}

			wg.Wait()
		})
	}
}

func getAwsNodeDaemonSet(podENIEnabled string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		Spec: appsv1.DaemonSetSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name: "aws-node",
							Env: []v1.EnvVar{
								{
									Name:  "ENABLE_POD_ENI",
									Value: podENIEnabled,
								},
							},
						},
					},
				},
			},
		},
	}
}
