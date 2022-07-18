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
// Source: github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider (interfaces: ResourceProvider)

// Package mock_provider is a generated GoMock package.
package mock_provider

import (
	ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/ipam"
	pool "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	gomock "github.com/golang/mock/gomock"
	reflect "reflect"
	reconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MockResourceProvider is a mock of ResourceProvider interface.
type MockResourceProvider struct {
	ctrl     *gomock.Controller
	recorder *MockResourceProviderMockRecorder
}

// MockResourceProviderMockRecorder is the mock recorder for MockResourceProvider.
type MockResourceProviderMockRecorder struct {
	mock *MockResourceProvider
}

// NewMockResourceProvider creates a new mock instance.
func NewMockResourceProvider(ctrl *gomock.Controller) *MockResourceProvider {
	mock := &MockResourceProvider{ctrl: ctrl}
	mock.recorder = &MockResourceProviderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockResourceProvider) EXPECT() *MockResourceProviderMockRecorder {
	return m.recorder
}

// DeInitResource mocks base method.
func (m *MockResourceProvider) DeInitResource(arg0 ec2.EC2Instance) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeInitResource", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeInitResource indicates an expected call of DeInitResource.
func (mr *MockResourceProviderMockRecorder) DeInitResource(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeInitResource", reflect.TypeOf((*MockResourceProvider)(nil).DeInitResource), arg0)
}

// GetPool mocks base method.
func (m *MockResourceProvider) GetPool(arg0 string) (pool.Pool, bool) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPool", arg0)
	ret0, _ := ret[0].(pool.Pool)
	ret1, _ := ret[1].(bool)
	return ret0, ret1
}

// GetIPAM
func (m *MockResourceProvider) GetIPAM(arg0 string) (ipam.Ipam, bool) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetIPAM", arg0)
	ret0, _ := ret[0].(ipam.Ipam)
	ret1, _ := ret[1].(bool)
	return ret0, ret1
}

// GetPool indicates an expected call of GetPool.
func (mr *MockResourceProviderMockRecorder) GetPool(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPool", reflect.TypeOf((*MockResourceProvider)(nil).GetPool), arg0)
}

// InitResource mocks base method.
func (m *MockResourceProvider) InitResource(arg0 ec2.EC2Instance) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "InitResource", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// InitResource indicates an expected call of InitResource.
func (mr *MockResourceProviderMockRecorder) InitResource(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "InitResource", reflect.TypeOf((*MockResourceProvider)(nil).InitResource), arg0)
}

// Introspect mocks base method.
func (m *MockResourceProvider) Introspect() interface{} {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Introspect")
	ret0, _ := ret[0].(interface{})
	return ret0
}

// Introspect indicates an expected call of Introspect.
func (mr *MockResourceProviderMockRecorder) Introspect() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Introspect", reflect.TypeOf((*MockResourceProvider)(nil).Introspect))
}

// IntrospectNode mocks base method.
func (m *MockResourceProvider) IntrospectNode(arg0 string) interface{} {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IntrospectNode", arg0)
	ret0, _ := ret[0].(interface{})
	return ret0
}

// IntrospectNode indicates an expected call of IntrospectNode.
func (mr *MockResourceProviderMockRecorder) IntrospectNode(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IntrospectNode", reflect.TypeOf((*MockResourceProvider)(nil).IntrospectNode), arg0)
}

// IsInstanceSupported mocks base method.
func (m *MockResourceProvider) IsInstanceSupported(arg0 ec2.EC2Instance) bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IsInstanceSupported", arg0)
	ret0, _ := ret[0].(bool)
	return ret0
}

// IsInstanceSupported indicates an expected call of IsInstanceSupported.
func (mr *MockResourceProviderMockRecorder) IsInstanceSupported(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IsInstanceSupported", reflect.TypeOf((*MockResourceProvider)(nil).IsInstanceSupported), arg0)
}

// ProcessAsyncJob mocks base method.
func (m *MockResourceProvider) ProcessAsyncJob(arg0 interface{}) (reconcile.Result, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ProcessAsyncJob", arg0)
	ret0, _ := ret[0].(reconcile.Result)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ProcessAsyncJob indicates an expected call of ProcessAsyncJob.
func (mr *MockResourceProviderMockRecorder) ProcessAsyncJob(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ProcessAsyncJob", reflect.TypeOf((*MockResourceProvider)(nil).ProcessAsyncJob), arg0)
}

// SubmitAsyncJob mocks base method.
func (m *MockResourceProvider) SubmitAsyncJob(arg0 interface{}) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "SubmitAsyncJob", arg0)
}

// SubmitAsyncJob indicates an expected call of SubmitAsyncJob.
func (mr *MockResourceProviderMockRecorder) SubmitAsyncJob(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubmitAsyncJob", reflect.TypeOf((*MockResourceProvider)(nil).SubmitAsyncJob), arg0)
}

// UpdateResourceCapacity mocks base method.
func (m *MockResourceProvider) UpdateResourceCapacity(arg0 ec2.EC2Instance) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateResourceCapacity", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateResourceCapacity indicates an expected call of UpdateResourceCapacity.
func (mr *MockResourceProviderMockRecorder) UpdateResourceCapacity(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateResourceCapacity", reflect.TypeOf((*MockResourceProvider)(nil).UpdateResourceCapacity), arg0)
}
