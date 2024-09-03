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
// Source: github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2 (interfaces: EC2Instance)

// Package mock_ec2 is a generated GoMock package.
package mock_ec2

import (
	reflect "reflect"

	api "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	gomock "github.com/golang/mock/gomock"
)

// MockEC2Instance is a mock of EC2Instance interface.
type MockEC2Instance struct {
	ctrl     *gomock.Controller
	recorder *MockEC2InstanceMockRecorder
}

// MockEC2InstanceMockRecorder is the mock recorder for MockEC2Instance.
type MockEC2InstanceMockRecorder struct {
	mock *MockEC2Instance
}

// NewMockEC2Instance creates a new mock instance.
func NewMockEC2Instance(ctrl *gomock.Controller) *MockEC2Instance {
	mock := &MockEC2Instance{ctrl: ctrl}
	mock.recorder = &MockEC2InstanceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockEC2Instance) EXPECT() *MockEC2InstanceMockRecorder {
	return m.recorder
}

// CurrentInstanceSecurityGroups mocks base method.
func (m *MockEC2Instance) CurrentInstanceSecurityGroups() []string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CurrentInstanceSecurityGroups")
	ret0, _ := ret[0].([]string)
	return ret0
}

// CurrentInstanceSecurityGroups indicates an expected call of CurrentInstanceSecurityGroups.
func (mr *MockEC2InstanceMockRecorder) CurrentInstanceSecurityGroups() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CurrentInstanceSecurityGroups", reflect.TypeOf((*MockEC2Instance)(nil).CurrentInstanceSecurityGroups))
}

// FreeDeviceIndex mocks base method.
func (m *MockEC2Instance) FreeDeviceIndex(arg0 int64) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "FreeDeviceIndex", arg0)
}

// FreeDeviceIndex indicates an expected call of FreeDeviceIndex.
func (mr *MockEC2InstanceMockRecorder) FreeDeviceIndex(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "FreeDeviceIndex", reflect.TypeOf((*MockEC2Instance)(nil).FreeDeviceIndex), arg0)
}

// GetCustomNetworkingSpec mocks base method.
func (m *MockEC2Instance) GetCustomNetworkingSpec() (string, []string) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetCustomNetworkingSpec")
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].([]string)
	return ret0, ret1
}

// GetCustomNetworkingSpec indicates an expected call of GetCustomNetworkingSpec.
func (mr *MockEC2InstanceMockRecorder) GetCustomNetworkingSpec() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetCustomNetworkingSpec", reflect.TypeOf((*MockEC2Instance)(nil).GetCustomNetworkingSpec))
}

// GetHighestUnusedDeviceIndex mocks base method.
func (m *MockEC2Instance) GetHighestUnusedDeviceIndex() (int64, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetHighestUnusedDeviceIndex")
	ret0, _ := ret[0].(int64)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetHighestUnusedDeviceIndex indicates an expected call of GetHighestUnusedDeviceIndex.
func (mr *MockEC2InstanceMockRecorder) GetHighestUnusedDeviceIndex() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetHighestUnusedDeviceIndex", reflect.TypeOf((*MockEC2Instance)(nil).GetHighestUnusedDeviceIndex))
}

// InstanceID mocks base method.
func (m *MockEC2Instance) InstanceID() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "InstanceID")
	ret0, _ := ret[0].(string)
	return ret0
}

// InstanceID indicates an expected call of InstanceID.
func (mr *MockEC2InstanceMockRecorder) InstanceID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "InstanceID", reflect.TypeOf((*MockEC2Instance)(nil).InstanceID))
}

// LoadDetails mocks base method.
func (m *MockEC2Instance) LoadDetails(arg0 api.EC2APIHelper) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LoadDetails", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// LoadDetails indicates an expected call of LoadDetails.
func (mr *MockEC2InstanceMockRecorder) LoadDetails(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LoadDetails", reflect.TypeOf((*MockEC2Instance)(nil).LoadDetails), arg0)
}

// Name mocks base method.
func (m *MockEC2Instance) Name() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Name")
	ret0, _ := ret[0].(string)
	return ret0
}

// Name indicates an expected call of Name.
func (mr *MockEC2InstanceMockRecorder) Name() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Name", reflect.TypeOf((*MockEC2Instance)(nil).Name))
}

// Os mocks base method.
func (m *MockEC2Instance) Os() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Os")
	ret0, _ := ret[0].(string)
	return ret0
}

// Os indicates an expected call of Os.
func (mr *MockEC2InstanceMockRecorder) Os() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Os", reflect.TypeOf((*MockEC2Instance)(nil).Os))
}

// PrimaryNetworkInterfaceID mocks base method.
func (m *MockEC2Instance) PrimaryNetworkInterfaceID() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PrimaryNetworkInterfaceID")
	ret0, _ := ret[0].(string)
	return ret0
}

// PrimaryNetworkInterfaceID indicates an expected call of PrimaryNetworkInterfaceID.
func (mr *MockEC2InstanceMockRecorder) PrimaryNetworkInterfaceID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PrimaryNetworkInterfaceID", reflect.TypeOf((*MockEC2Instance)(nil).PrimaryNetworkInterfaceID))
}

// SetNewCustomNetworkingSpec mocks base method.
func (m *MockEC2Instance) SetNewCustomNetworkingSpec(arg0 string, arg1 []string) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "SetNewCustomNetworkingSpec", arg0, arg1)
}

// SetNewCustomNetworkingSpec indicates an expected call of SetNewCustomNetworkingSpec.
func (mr *MockEC2InstanceMockRecorder) SetNewCustomNetworkingSpec(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SetNewCustomNetworkingSpec", reflect.TypeOf((*MockEC2Instance)(nil).SetNewCustomNetworkingSpec), arg0, arg1)
}

// SubnetCidrBlock mocks base method.
func (m *MockEC2Instance) SubnetCidrBlock() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubnetCidrBlock")
	ret0, _ := ret[0].(string)
	return ret0
}

// SubnetCidrBlock indicates an expected call of SubnetCidrBlock.
func (mr *MockEC2InstanceMockRecorder) SubnetCidrBlock() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubnetCidrBlock", reflect.TypeOf((*MockEC2Instance)(nil).SubnetCidrBlock))
}

// SubnetID mocks base method.
func (m *MockEC2Instance) SubnetID() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubnetID")
	ret0, _ := ret[0].(string)
	return ret0
}

// SubnetID indicates an expected call of SubnetID.
func (mr *MockEC2InstanceMockRecorder) SubnetID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubnetID", reflect.TypeOf((*MockEC2Instance)(nil).SubnetID))
}

// SubnetMask mocks base method.
func (m *MockEC2Instance) SubnetMask() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubnetMask")
	ret0, _ := ret[0].(string)
	return ret0
}

// SubnetMask indicates an expected call of SubnetMask.
func (mr *MockEC2InstanceMockRecorder) SubnetMask() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubnetMask", reflect.TypeOf((*MockEC2Instance)(nil).SubnetMask))
}

// SubnetV6CidrBlock mocks base method.
func (m *MockEC2Instance) SubnetV6CidrBlock() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubnetV6CidrBlock")
	ret0, _ := ret[0].(string)
	return ret0
}

// SubnetV6CidrBlock indicates an expected call of SubnetV6CidrBlock.
func (mr *MockEC2InstanceMockRecorder) SubnetV6CidrBlock() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubnetV6CidrBlock", reflect.TypeOf((*MockEC2Instance)(nil).SubnetV6CidrBlock))
}

// SubnetV6Mask mocks base method.
func (m *MockEC2Instance) SubnetV6Mask() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubnetV6Mask")
	ret0, _ := ret[0].(string)
	return ret0
}

// SubnetV6Mask indicates an expected call of SubnetV6Mask.
func (mr *MockEC2InstanceMockRecorder) SubnetV6Mask() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubnetV6Mask", reflect.TypeOf((*MockEC2Instance)(nil).SubnetV6Mask))
}

// Type mocks base method.
func (m *MockEC2Instance) Type() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Type")
	ret0, _ := ret[0].(string)
	return ret0
}

// Type indicates an expected call of Type.
func (mr *MockEC2InstanceMockRecorder) Type() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Type", reflect.TypeOf((*MockEC2Instance)(nil).Type))
}

// UpdateCurrentSubnetAndCidrBlock mocks base method.
func (m *MockEC2Instance) UpdateCurrentSubnetAndCidrBlock(arg0 api.EC2APIHelper) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateCurrentSubnetAndCidrBlock", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateCurrentSubnetAndCidrBlock indicates an expected call of UpdateCurrentSubnetAndCidrBlock.
func (mr *MockEC2InstanceMockRecorder) UpdateCurrentSubnetAndCidrBlock(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateCurrentSubnetAndCidrBlock", reflect.TypeOf((*MockEC2Instance)(nil).UpdateCurrentSubnetAndCidrBlock), arg0)
}
