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
// Source: github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api (interfaces: EC2Wrapper)

// Package mock_api is a generated GoMock package.
package mock_api

import (
	reflect "reflect"

	servicequotas "github.com/aws/aws-sdk-go/service/servicequotas"
	ec2 "github.com/aws/aws-sdk-go/service/ec2"
	gomock "github.com/golang/mock/gomock"
)

// MockEC2Wrapper is a mock of EC2Wrapper interface.
type MockEC2Wrapper struct {
	ctrl     *gomock.Controller
	recorder *MockEC2WrapperMockRecorder
}

// MockEC2WrapperMockRecorder is the mock recorder for MockEC2Wrapper.
type MockEC2WrapperMockRecorder struct {
	mock *MockEC2Wrapper
}

// NewMockEC2Wrapper creates a new mock instance.
func NewMockEC2Wrapper(ctrl *gomock.Controller) *MockEC2Wrapper {
	mock := &MockEC2Wrapper{ctrl: ctrl}
	mock.recorder = &MockEC2WrapperMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockEC2Wrapper) EXPECT() *MockEC2WrapperMockRecorder {
	return m.recorder
}

// AssignPrivateIPAddresses mocks base method.
func (m *MockEC2Wrapper) AssignPrivateIPAddresses(arg0 *ec2.AssignPrivateIpAddressesInput) (*ec2.AssignPrivateIpAddressesOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AssignPrivateIPAddresses", arg0)
	ret0, _ := ret[0].(*ec2.AssignPrivateIpAddressesOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// AssignPrivateIPAddresses indicates an expected call of AssignPrivateIPAddresses.
func (mr *MockEC2WrapperMockRecorder) AssignPrivateIPAddresses(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AssignPrivateIPAddresses", reflect.TypeOf((*MockEC2Wrapper)(nil).AssignPrivateIPAddresses), arg0)
}

// AssociateTrunkInterface mocks base method.
func (m *MockEC2Wrapper) AssociateTrunkInterface(arg0 *ec2.AssociateTrunkInterfaceInput) (*ec2.AssociateTrunkInterfaceOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AssociateTrunkInterface", arg0)
	ret0, _ := ret[0].(*ec2.AssociateTrunkInterfaceOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// AssociateTrunkInterface indicates an expected call of AssociateTrunkInterface.
func (mr *MockEC2WrapperMockRecorder) AssociateTrunkInterface(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AssociateTrunkInterface", reflect.TypeOf((*MockEC2Wrapper)(nil).AssociateTrunkInterface), arg0)
}

// AttachNetworkInterface mocks base method.
func (m *MockEC2Wrapper) AttachNetworkInterface(arg0 *ec2.AttachNetworkInterfaceInput) (*ec2.AttachNetworkInterfaceOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AttachNetworkInterface", arg0)
	ret0, _ := ret[0].(*ec2.AttachNetworkInterfaceOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// AttachNetworkInterface indicates an expected call of AttachNetworkInterface.
func (mr *MockEC2WrapperMockRecorder) AttachNetworkInterface(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AttachNetworkInterface", reflect.TypeOf((*MockEC2Wrapper)(nil).AttachNetworkInterface), arg0)
}

// CreateNetworkInterface mocks base method.
func (m *MockEC2Wrapper) CreateNetworkInterface(arg0 *ec2.CreateNetworkInterfaceInput) (*ec2.CreateNetworkInterfaceOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateNetworkInterface", arg0)
	ret0, _ := ret[0].(*ec2.CreateNetworkInterfaceOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateNetworkInterface indicates an expected call of CreateNetworkInterface.
func (mr *MockEC2WrapperMockRecorder) CreateNetworkInterface(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateNetworkInterface", reflect.TypeOf((*MockEC2Wrapper)(nil).CreateNetworkInterface), arg0)
}

// CreateNetworkInterfacePermission mocks base method.
func (m *MockEC2Wrapper) CreateNetworkInterfacePermission(arg0 *ec2.CreateNetworkInterfacePermissionInput) (*ec2.CreateNetworkInterfacePermissionOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateNetworkInterfacePermission", arg0)
	ret0, _ := ret[0].(*ec2.CreateNetworkInterfacePermissionOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateNetworkInterfacePermission indicates an expected call of CreateNetworkInterfacePermission.
func (mr *MockEC2WrapperMockRecorder) CreateNetworkInterfacePermission(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateNetworkInterfacePermission", reflect.TypeOf((*MockEC2Wrapper)(nil).CreateNetworkInterfacePermission), arg0)
}

// CreateTags mocks base method.
func (m *MockEC2Wrapper) CreateTags(arg0 *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateTags", arg0)
	ret0, _ := ret[0].(*ec2.CreateTagsOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateTags indicates an expected call of CreateTags.
func (mr *MockEC2WrapperMockRecorder) CreateTags(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateTags", reflect.TypeOf((*MockEC2Wrapper)(nil).CreateTags), arg0)
}

// DeleteNetworkInterface mocks base method.
func (m *MockEC2Wrapper) DeleteNetworkInterface(arg0 *ec2.DeleteNetworkInterfaceInput) (*ec2.DeleteNetworkInterfaceOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteNetworkInterface", arg0)
	ret0, _ := ret[0].(*ec2.DeleteNetworkInterfaceOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DeleteNetworkInterface indicates an expected call of DeleteNetworkInterface.
func (mr *MockEC2WrapperMockRecorder) DeleteNetworkInterface(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteNetworkInterface", reflect.TypeOf((*MockEC2Wrapper)(nil).DeleteNetworkInterface), arg0)
}

// DescribeInstances mocks base method.
func (m *MockEC2Wrapper) DescribeInstances(arg0 *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DescribeInstances", arg0)
	ret0, _ := ret[0].(*ec2.DescribeInstancesOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DescribeInstances indicates an expected call of DescribeInstances.
func (mr *MockEC2WrapperMockRecorder) DescribeInstances(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DescribeInstances", reflect.TypeOf((*MockEC2Wrapper)(nil).DescribeInstances), arg0)
}

// DescribeNetworkInterfaces mocks base method.
func (m *MockEC2Wrapper) DescribeNetworkInterfaces(arg0 *ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DescribeNetworkInterfaces", arg0)
	ret0, _ := ret[0].(*ec2.DescribeNetworkInterfacesOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DescribeNetworkInterfaces indicates an expected call of DescribeNetworkInterfaces.
func (mr *MockEC2WrapperMockRecorder) DescribeNetworkInterfaces(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DescribeNetworkInterfaces", reflect.TypeOf((*MockEC2Wrapper)(nil).DescribeNetworkInterfaces), arg0)
}

// DescribeNetworkInterfacesPages mocks base method.
func (m *MockEC2Wrapper) DescribeNetworkInterfacesPages(arg0 *ec2.DescribeNetworkInterfacesInput) ([]*ec2.NetworkInterface, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DescribeNetworkInterfacesPages", arg0)
	ret0, _ := ret[0].([]*ec2.NetworkInterface)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DescribeNetworkInterfacesPages indicates an expected call of DescribeNetworkInterfacesPages.
func (mr *MockEC2WrapperMockRecorder) DescribeNetworkInterfacesPages(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DescribeNetworkInterfacesPages", reflect.TypeOf((*MockEC2Wrapper)(nil).DescribeNetworkInterfacesPages), arg0)
}

// DescribeSubnets mocks base method.
func (m *MockEC2Wrapper) DescribeSubnets(arg0 *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DescribeSubnets", arg0)
	ret0, _ := ret[0].(*ec2.DescribeSubnetsOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DescribeSubnets indicates an expected call of DescribeSubnets.
func (mr *MockEC2WrapperMockRecorder) DescribeSubnets(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DescribeSubnets", reflect.TypeOf((*MockEC2Wrapper)(nil).DescribeSubnets), arg0)
}

// DescribeTrunkInterfaceAssociations mocks base method.
func (m *MockEC2Wrapper) DescribeTrunkInterfaceAssociations(arg0 *ec2.DescribeTrunkInterfaceAssociationsInput) (*ec2.DescribeTrunkInterfaceAssociationsOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DescribeTrunkInterfaceAssociations", arg0)
	ret0, _ := ret[0].(*ec2.DescribeTrunkInterfaceAssociationsOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DescribeTrunkInterfaceAssociations indicates an expected call of DescribeTrunkInterfaceAssociations.
func (mr *MockEC2WrapperMockRecorder) DescribeTrunkInterfaceAssociations(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DescribeTrunkInterfaceAssociations", reflect.TypeOf((*MockEC2Wrapper)(nil).DescribeTrunkInterfaceAssociations), arg0)
}

// DetachNetworkInterface mocks base method.
func (m *MockEC2Wrapper) DetachNetworkInterface(arg0 *ec2.DetachNetworkInterfaceInput) (*ec2.DetachNetworkInterfaceOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DetachNetworkInterface", arg0)
	ret0, _ := ret[0].(*ec2.DetachNetworkInterfaceOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DetachNetworkInterface indicates an expected call of DetachNetworkInterface.
func (mr *MockEC2WrapperMockRecorder) DetachNetworkInterface(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DetachNetworkInterface", reflect.TypeOf((*MockEC2Wrapper)(nil).DetachNetworkInterface), arg0)
}

// DisassociateTrunkInterface mocks base method.
func (m *MockEC2Wrapper) DisassociateTrunkInterface(arg0 *ec2.DisassociateTrunkInterfaceInput) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DisassociateTrunkInterface", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// DisassociateTrunkInterface indicates an expected call of DisassociateTrunkInterface.
func (mr *MockEC2WrapperMockRecorder) DisassociateTrunkInterface(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DisassociateTrunkInterface", reflect.TypeOf((*MockEC2Wrapper)(nil).DisassociateTrunkInterface), arg0)
}

// ModifyNetworkInterfaceAttribute mocks base method.
func (m *MockEC2Wrapper) ModifyNetworkInterfaceAttribute(arg0 *ec2.ModifyNetworkInterfaceAttributeInput) (*ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ModifyNetworkInterfaceAttribute", arg0)
	ret0, _ := ret[0].(*ec2.ModifyNetworkInterfaceAttributeOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ModifyNetworkInterfaceAttribute indicates an expected call of ModifyNetworkInterfaceAttribute.
func (mr *MockEC2WrapperMockRecorder) ModifyNetworkInterfaceAttribute(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ModifyNetworkInterfaceAttribute", reflect.TypeOf((*MockEC2Wrapper)(nil).ModifyNetworkInterfaceAttribute), arg0)
}

// UnassignPrivateIPAddresses mocks base method.
func (m *MockEC2Wrapper) UnassignPrivateIPAddresses(arg0 *ec2.UnassignPrivateIpAddressesInput) (*ec2.UnassignPrivateIpAddressesOutput, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UnassignPrivateIPAddresses", arg0)
	ret0, _ := ret[0].(*ec2.UnassignPrivateIpAddressesOutput)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UnassignPrivateIPAddresses indicates an expected call of UnassignPrivateIPAddresses.
func (mr *MockEC2WrapperMockRecorder) UnassignPrivateIPAddresses(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UnassignPrivateIPAddresses", reflect.TypeOf((*MockEC2Wrapper)(nil).UnassignPrivateIPAddresses), arg0)
}

// GetServiceQuota mocks base method.
func (m *MockEC2Wrapper) GetServiceQuota(input *servicequotas.GetServiceQuotaInput) (int, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetServiceQuota", input)
	ret0, _ := ret[0].(int)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetServiceQuota indicates an expected call of GetServiceQuota.
func (mr *MockEC2WrapperMockRecorder) GetServiceQuota(input interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetServiceQuota", reflect.TypeOf((*MockEC2Wrapper)(nil).GetServiceQuota), input)
}
