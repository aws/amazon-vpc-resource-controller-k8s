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

// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils (interfaces: K8sCacheHelper)

// Package mock_utils is a generated GoMock package.
package mock_utils

import (
	gomock "github.com/golang/mock/gomock"
	v1 "k8s.io/api/core/v1"
	types "k8s.io/apimachinery/pkg/types"
	reflect "reflect"
)

// MockK8sCacheHelper is a mock of K8sCacheHelper interface
type MockK8sCacheHelper struct {
	ctrl     *gomock.Controller
	recorder *MockK8sCacheHelperMockRecorder
}

// MockK8sCacheHelperMockRecorder is the mock recorder for MockK8sCacheHelper
type MockK8sCacheHelperMockRecorder struct {
	mock *MockK8sCacheHelper
}

// NewMockK8sCacheHelper creates a new mock instance
func NewMockK8sCacheHelper(ctrl *gomock.Controller) *MockK8sCacheHelper {
	mock := &MockK8sCacheHelper{ctrl: ctrl}
	mock.recorder = &MockK8sCacheHelperMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockK8sCacheHelper) EXPECT() *MockK8sCacheHelperMockRecorder {
	return m.recorder
}

// GetPodSecurityGroups mocks base method
func (m *MockK8sCacheHelper) GetPodSecurityGroups(arg0 *v1.Pod) ([]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPodSecurityGroups", arg0)
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPodSecurityGroups indicates an expected call of GetPodSecurityGroups
func (mr *MockK8sCacheHelperMockRecorder) GetPodSecurityGroups(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPodSecurityGroups", reflect.TypeOf((*MockK8sCacheHelper)(nil).GetPodSecurityGroups), arg0)
}

// GetSecurityGroupsFromPod mocks base method
func (m *MockK8sCacheHelper) GetSecurityGroupsFromPod(arg0 types.NamespacedName) ([]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetSecurityGroupsFromPod", arg0)
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetSecurityGroupsFromPod indicates an expected call of GetSecurityGroupsFromPod
func (mr *MockK8sCacheHelperMockRecorder) GetSecurityGroupsFromPod(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetSecurityGroupsFromPod", reflect.TypeOf((*MockK8sCacheHelper)(nil).GetSecurityGroupsFromPod), arg0)
}
