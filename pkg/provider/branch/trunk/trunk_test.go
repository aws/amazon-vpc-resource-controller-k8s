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

package trunk

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_cooldown "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/cooldown"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsEc2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsEc2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	// Instance details
	InstanceId            = "i-00000000000000000"
	InstanceType          = "c5.xlarge"
	SubnetId              = "subnet-00000000000000000"
	SubnetCidrBlock       = "192.168.0.0/16"
	SubnetV6CidrBlock     = "2600::/64"
	NodeName              = "test-node"
	FakeInstance          = ec2.NewEC2Instance(NodeName, InstanceId, config.OSLinux, zap.New())
	InstanceSecurityGroup = []string{"sg-1", "sg-2"}

	// Mock Pod 1
	MockPodName1      = "pod_name"
	MockPodNamespace1 = "pod_namespace"
	// PodNamespacedName1 = "pod_namespace/pod_name"
	PodUID      = "uid-1"
	MockPodUID1 = types.UID(PodUID)
	MockPod1    = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			UID:       MockPodUID1,
			Name:      MockPodName1,
			Namespace: MockPodNamespace1,
			Annotations: map[string]string{config.ResourceNamePodENI: "[{\"eniId\":\"eni-00000000000000000\",\"ifAddress\":\"FF:FF:FF:FF:FF:FF\",\"privateIp\":\"192.168.0.15\"," +
				"\"ipv6Addr\":\"2600::\",\"vlanId\":1,\"subnetCidr\":\"192.168.0.0/16\",\"subnetV6Cidr\":\"2600::/64\",\"AssociationId\":\"trunk-assoc-0000000000000000\"},{\"eniId\":\"eni-00000000000000001\"" +
				",\"ifAddress\":\"FF:FF:FF:FF:FF:F9\",\"privateIp\":\"192.168.0.16\",\"ipv6Addr\":\"2600::1\",\"vlanId\":2,\"subnetCidr\":\"192.168.0.0/16\",\"subnetV6Cidr\":\"2600::/64\"," +
				"\"AssociationId\":\"trunk-assoc-0000000000000001\"}]"},
		},
		Spec:   v1.PodSpec{NodeName: NodeName},
		Status: v1.PodStatus{},
	}

	// Mock Pod 2
	MockPodName2        = "pod_name_2"
	MockPodNamespace2   = ""
	MockNamespacedName2 = "default/pod_name_2"
	PodUID2             = "uid-2"
	MockPodUID2         = types.UID(PodUID2)

	MockPod2 = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			UID:         MockPodUID2,
			Name:        MockPodName2,
			Namespace:   MockPodNamespace2,
			Annotations: make(map[string]string),
		},
		Spec:   v1.PodSpec{NodeName: NodeName},
		Status: v1.PodStatus{},
	}

	// Security Groups
	SecurityGroup1 = "sg-0000000000000"
	SecurityGroup2 = "sg-0000000000000"
	SecurityGroups = []string{SecurityGroup1, SecurityGroup2}

	// Branch Interface 1
	Branch1Id          = "eni-00000000000000000"
	MacAddr1           = "FF:FF:FF:FF:FF:FF"
	BranchIp1          = "192.168.0.15"
	BranchV6Ip1        = "2600::"
	VlanId1            = 1
	MockAssociationID1 = "trunk-assoc-0000000000000000"
	MockAssociationID2 = "trunk-assoc-0000000000000001"

	EniDetails1 = &ENIDetails{
		ID:            Branch1Id,
		MACAdd:        MacAddr1,
		IPV4Addr:      BranchIp1,
		IPV6Addr:      BranchV6Ip1,
		VlanID:        VlanId1,
		SubnetCIDR:    SubnetCidrBlock,
		SubnetV6CIDR:  SubnetV6CidrBlock,
		AssociationID: MockAssociationID1,
	}

	branchENIs1 = []*ENIDetails{EniDetails1}

	BranchInterface1 = &awsEc2Types.NetworkInterface{
		MacAddress:         &MacAddr1,
		NetworkInterfaceId: &Branch1Id,
		PrivateIpAddress:   &BranchIp1,
		Ipv6Address:        &BranchV6Ip1,
	}

	// Branch Interface 2
	Branch2Id   = "eni-00000000000000001"
	MacAddr2    = "FF:FF:FF:FF:FF:F9"
	BranchIp2   = "192.168.0.16"
	BranchV6Ip2 = "2600::1"
	VlanId2     = 2

	EniDetails2 = &ENIDetails{
		ID:            Branch2Id,
		MACAdd:        MacAddr2,
		IPV4Addr:      BranchIp2,
		IPV6Addr:      BranchV6Ip2,
		VlanID:        VlanId2,
		SubnetCIDR:    SubnetCidrBlock,
		SubnetV6CIDR:  SubnetV6CidrBlock,
		AssociationID: MockAssociationID2,
	}

	BranchInterface2 = &awsEc2Types.NetworkInterface{
		MacAddress:         &MacAddr2,
		NetworkInterfaceId: &Branch2Id,
		PrivateIpAddress:   &BranchIp2,
		Ipv6Address:        &BranchV6Ip2,
	}

	// Trunk Interface
	trunkId        = "eni-00000000000000002"
	trunkInterface = &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeTrunk,
		NetworkInterfaceId: &trunkId,
		Attachment: &awsEc2Types.NetworkInterfaceAttachment{
			Status: awsEc2Types.AttachmentStatusAttached,
		},
	}

	trunkIDTag = awsEc2Types.Tag{
		Key:   aws.String(config.TrunkENIIDTag),
		Value: &trunkId,
	}

	vlan1Tag = []awsEc2Types.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(VlanId1)),
	}, trunkIDTag}

	vlan2Tag = []awsEc2Types.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(VlanId2)),
	}, trunkIDTag}

	instanceNwInterfaces = []awsEc2Types.InstanceNetworkInterface{
		{
			InterfaceType:      aws.String("trunk"),
			NetworkInterfaceId: &trunkId,
		},
	}

	branchInterfaces = []*awsEc2Types.NetworkInterface{
		{
			InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
			NetworkInterfaceId: &EniDetails1.ID,
			TagSet:             vlan1Tag,
		},
		{
			InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
			NetworkInterfaceId: &EniDetails2.ID,
			TagSet:             vlan2Tag,
		},
	}

	mockAssociationOutput1 = &awsEc2.AssociateTrunkInterfaceOutput{
		InterfaceAssociation: &awsEc2Types.TrunkInterfaceAssociation{
			AssociationId: &MockAssociationID1,
		},
	}
	mockAssociationOutput2 = &awsEc2.AssociateTrunkInterfaceOutput{
		InterfaceAssociation: &awsEc2Types.TrunkInterfaceAssociation{
			AssociationId: &MockAssociationID2,
		},
	}

	ENIDetailsMissingAssociationID = &ENIDetails{
		ID:           Branch2Id,
		MACAdd:       MacAddr2,
		IPV4Addr:     BranchIp2,
		IPV6Addr:     BranchV6Ip2,
		VlanID:       VlanId2,
		SubnetCIDR:   SubnetCidrBlock,
		SubnetV6CIDR: SubnetV6CidrBlock,
	}

	MockError = fmt.Errorf("mock error")
)

func getMockHelperInstanceAndTrunkObject(ctrl *gomock.Controller) (*trunkENI, *mock_api.MockEC2APIHelper,
	*mock_ec2.MockEC2Instance,
) {
	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true
	trunkENI.ec2ApiHelper = mockHelper
	trunkENI.instance = mockInstance
	// Hand-built trunks mimic the EC2 init path, whose ledger is verified. Gate tests
	// (TestTrunkENI_VerifyBranchLedger_*) construct their own trunk with this left false.
	trunkENI.branchLedgerVerified = true

	// Clean up
	EniDetails1.deletionTimeStamp = time.Time{}
	EniDetails2.deletionTimeStamp = time.Time{}
	EniDetails1.deleteRetryCount = 0
	EniDetails2.deleteRetryCount = 0

	return &trunkENI, mockHelper, mockInstance
}

func getMockTrunk() trunkENI {
	log := zap.New(zap.UseDevMode(true)).WithName("node manager")
	return trunkENI{
		log:               log,
		usedVlanIds:       make([]bool, MaxAllocatableVlanIds),
		uidToBranchENIMap: map[string][]*ENIDetails{},
		pendingCreates:    map[string]struct{}{},
		vlanOwner:         map[int]string{},
		nodeIDTag: []awsEc2Types.Tag{
			{
				Key:   aws.String(config.NetworkInterfaceNodeIDKey),
				Value: aws.String(FakeInstance.InstanceID()),
			},
		},
	}
}

func TestNewTrunkENI(t *testing.T) {
	trunkENI := NewTrunkENI(zap.New(), FakeInstance, nil)
	assert.NotNil(t, trunkENI)
}

// TestTrunkENI_assignVlanId tests that Vlan ids are assigned till the Max capacity is reached and after that assign
// call will return an error
func TestTrunkENI_assignVlanId(t *testing.T) {
	trunkENI := getMockTrunk()

	for i := 0; i < MaxAllocatableVlanIds; i++ {
		id, err := trunkENI.assignVlanId()
		assert.NoError(t, err)
		assert.Equal(t, i, id)
	}

	// Try allocating one more Vlan Id after breaching max capacity
	_, err := trunkENI.assignVlanId()
	assert.NotNil(t, err)
}

// TestTrunkENI_freeVlanId tests if a vlan id is freed it can be re assigned
func TestTrunkENI_freeVlanId(t *testing.T) {
	trunkENI := getMockTrunk()

	// Assign single Vlan Id
	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 0, id)

	// Free the vlan Id
	trunkENI.freeVlanId(0, "")

	// Assign single Vlan Id again
	id, err = trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 0, id)
}

func TestTrunkENI_markVlanAssigned(t *testing.T) {
	trunkENI := getMockTrunk()

	// Mark a Vlan as assigned
	trunkENI.markVlanAssigned(0)

	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 1, id)
}

// TestTrunkENI_getBranchFromCache tests branch eni is returned when present in the cache
func TestTrunkENI_getBranchFromCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.uidToBranchENIMap[PodUID] = branchENIs1

	branchFromCache, isPresent := trunkENI.getBranchFromCache(PodUID)

	assert.True(t, isPresent)
	assert.Equal(t, branchENIs1, branchFromCache)
}

// TestTrunkENI_getBranchFromCache_NotPresent tests false is returned if the branch eni is not present in cache
func TestTrunkENI_getBranchFromCache_NotPresent(t *testing.T) {
	trunkENI := getMockTrunk()

	_, isPresent := trunkENI.getBranchFromCache(PodUID)

	assert.False(t, isPresent)
}

// TestTrunkENI_addBranchToCache tests branch is added to the cache
func TestTrunkENI_addBranchToCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.addBranchToCache(PodUID, branchENIs1)

	branchFromCache, ok := trunkENI.uidToBranchENIMap[PodUID]
	assert.True(t, ok)
	assert.Equal(t, branchENIs1, branchFromCache)
}

// TestTrunkENI_pushENIToDeleteQueue tests pushing to delete queue the data is stored in FIFO strategy
func TestTrunkENI_pushENIToDeleteQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.pushENIToDeleteQueue(EniDetails2)

	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[1])
}

// TestTrunkENI_pushENIsToFrontOfDeleteQueue tests ENIs are pushed to the front of the queue instead of the back
func TestTrunkENI_pushENIsToFrontOfDeleteQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.PushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{EniDetails2})

	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[1])
}

// TestTrunkENI_pushENIsToFrontOfDeleteQueue_RemovePodFromCache tests pod is removed from cache and ENI
// are added to delete queue
func TestTrunkENI_pushENIsToFrontOfDeleteQueue_RemovePodFromCache(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails2}

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.PushENIsToFrontOfDeleteQueue(MockPod1, []*ENIDetails{EniDetails2})

	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[1])
	assert.NotContains(t, PodUID, trunkENI.uidToBranchENIMap)
}

// TestTrunkENI_popENIFromDeleteQueue tests if the queue has ENIs it must be removed from the queue on pop operation
func TestTrunkENI_popENIFromDeleteQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	eniDetails, hasENI := trunkENI.popENIFromDeleteQueue()

	assert.True(t, hasENI)
	assert.Equal(t, EniDetails1, eniDetails)

	_, hasENI = trunkENI.popENIFromDeleteQueue()
	assert.False(t, hasENI)
}

// TestTrunkENI_getBranchInterfacesUsedByPod tests that branch interface are returned if present in pod annotation
func TestTrunkENI_getBranchInterfacesUsedByPod(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs := trunkENI.getBranchInterfacesUsedByPod(MockPod1)

	assert.Equal(t, 2, len(branchENIs))
	assert.Equal(t, EniDetails1, branchENIs[0])
	assert.Equal(t, EniDetails2, branchENIs[1])
}

// TestTrunkENI_getBranchInterfacesUsedByPod_MissingAnnotation tests that empty slice is returned if the pod has no branch
// eni annotation
func TestTrunkENI_getBranchInterfacesUsedByPod_MissingAnnotation(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs := trunkENI.getBranchInterfacesUsedByPod(MockPod2)

	assert.Equal(t, 0, len(branchENIs))
}

// TestTrunkENI_getBranchInterfaceMap tests that the branch interface map is returned for the given branch interface slice
func TestTrunkENI_getBranchInterfaceMap(t *testing.T) {
	trunkENI := getMockTrunk()

	branchENIsMap := trunkENI.getBranchInterfaceMap([]*ENIDetails{EniDetails1})
	assert.Equal(t, EniDetails1, branchENIsMap[EniDetails1.ID])
}

// TestTrunkENI_getBranchInterfaceMap_EmptyList tests that empty map is returned if empty list is passed
func TestTrunkENI_getBranchInterfaceMap_EmptyList(t *testing.T) {
	trunkENI := getMockTrunk()

	branchENIsMap := trunkENI.getBranchInterfaceMap([]*ENIDetails{})
	assert.NotNil(t, branchENIsMap)
	assert.Zero(t, len(branchENIsMap))
}

// TestTrunkENI_deleteENI tests deleting branch ENI
func TestTrunkENI_deleteENI(t *testing.T) {
	type args struct {
		eniDetail *ENIDetails
		VlanID    int
	}
	type fields struct {
		mockEC2APIHelper *mock_api.MockEC2APIHelper
		trunkENI         *trunkENI
	}
	testTrunkENI_deleteENI := []struct {
		name    string
		prepare func(f *fields)
		args    args
		wantErr bool
		asserts func(f *fields)
	}{
		{
			name: "Vland_Freed, verifies VLANID is freed when branch ENI is deleted",
			prepare: func(f *fields) {
				f.mockEC2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(nil)
				f.mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(nil)
			},
			args: args{
				eniDetail: EniDetails1,
				VlanID:    VlanId1,
			},
			wantErr: false,
			asserts: func(f *fields) {
				assert.False(t, f.trunkENI.usedVlanIds[VlanId1])
			},
		},
		{
			name: "Vland_NotFreed, verifies VLANID is not freed when branch ENI delete fails",
			prepare: func(f *fields) {
				f.mockEC2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(nil)
				f.mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(MockError)
			},
			args: args{
				eniDetail: EniDetails1,
				VlanID:    VlanId1,
			},
			wantErr: true,
			asserts: func(f *fields) {
				assert.True(t, f.trunkENI.usedVlanIds[VlanId1])
			},
		},
		{
			name: "DisassociateTrunkInterface_Fails, verifies branch ENI is deleted when disassociation fails for backward compatibility",
			prepare: func(f *fields) {
				f.mockEC2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(MockError)
				f.mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(nil)
			},
			args: args{
				eniDetail: EniDetails1,
				VlanID:    VlanId1,
			},
			wantErr: false,
			asserts: func(f *fields) {
				assert.False(t, f.trunkENI.usedVlanIds[VlanId1])
			},
		},
		{
			name: "MissingAssociationID, verifies DisassociateTrunkInterface is skipped when association ID is missing and branch ENI is deleted for backward compatibility",
			prepare: func(f *fields) {
				f.mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&Branch2Id).Return(nil)
			},
			args: args{
				eniDetail: ENIDetailsMissingAssociationID,
				VlanID:    VlanId2,
			},
			wantErr: false,
			asserts: func(f *fields) {
				assert.False(t, f.trunkENI.usedVlanIds[VlanId2])
			},
		},
	}

	for _, tt := range testTrunkENI_deleteENI {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
			trunkENI.markVlanAssigned(tt.args.VlanID)

			f := fields{
				mockEC2APIHelper: ec2APIHelper,
				trunkENI:         trunkENI,
			}
			if tt.prepare != nil {
				tt.prepare(&f)
			}
			err := f.trunkENI.deleteENI(tt.args.eniDetail)
			assert.Equal(t, err != nil, tt.wantErr)
			if tt.asserts != nil {
				tt.asserts(&f)
			}
		})
	}
}

// TestTrunkENI_DeleteCooledDownENIs_NotCooledDown tests that ENIs that have not cooled down are not deleted
func TestTrunkENI_DeleteCooledDownENIs_NotCooledDown(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()

	EniDetails1.deletionTimeStamp = time.Now()
	EniDetails2.deletionTimeStamp = time.Now()
	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))

	trunkENI.DeleteCooledDownENIs()
	assert.Equal(t, 2, len(trunkENI.deleteQueue))
}

// TestTrunkENI_DeleteCooledDownENIs_NoDeletionTimeStamp tests that ENIs are deleted if they don't have any deletion timestamp
func TestTrunkENI_DeleteCooledDownENIs_NoDeletionTimeStamp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	EniDetails1.deletionTimeStamp = time.Time{}
	EniDetails2.deletionTimeStamp = time.Now().Add(-(time.Second * 62))
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.usedVlanIds[VlanId2] = true

	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	ec2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(nil)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(nil)
	ec2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID2).Return(nil)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails2.ID).Return(nil)

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))

	trunkENI.DeleteCooledDownENIs()
	assert.Equal(t, 0, len(trunkENI.deleteQueue))
}

// TestTrunkENI_DeleteCooledDownENIs_CooledDownResource tests that cooled down resources are deleted
func TestTrunkENI_DeleteCooledDownENIs_CooledDownResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	EniDetails1.deletionTimeStamp = time.Now().Add(-time.Second * 60)
	EniDetails2.deletionTimeStamp = time.Now().Add(-time.Second * 24)
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.usedVlanIds[VlanId2] = true

	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	ec2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(nil)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(nil)

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))

	trunkENI.DeleteCooledDownENIs()
	assert.Equal(t, 1, len(trunkENI.deleteQueue))
	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[0])
}

// TestTrunkENI_DeleteCooledDownENIs_DeleteFailed tests that when delete fails item is requeued into the delete queue for
// the retry count
func TestTrunkENI_DeleteCooledDownENIs_DeleteFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	coolDown := mock_cooldown.NewMockCoolDown(ctrl)
	EniDetails1.deletionTimeStamp = time.Now().Add(-time.Second * 61)
	EniDetails2.deletionTimeStamp = time.Now().Add(-time.Second * 62)
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.usedVlanIds[VlanId2] = true

	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(createCoolDownMockCM("60"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))

	coolDown.EXPECT().GetCoolDownPeriod().Return(time.Second * 60).AnyTimes()
	ec2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(nil).Times(MaxDeleteRetries)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(MockError).Times(MaxDeleteRetries)
	ec2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID2).Return(nil)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails2.ID).Return(nil)

	trunkENI.DeleteCooledDownENIs()
	assert.Zero(t, len(trunkENI.deleteQueue))
}

// TestTrunkENI_DeleteCooledDownENIs_ForgottenMetric verifies that when an ENI exhausts MaxDeleteRetries
// and is forgotten (dropped from the delete queue while still attached in EC2), the class-2 orphan
// PRODUCER metric branch_eni_delete_forgotten_total is incremented so orphan production is observable.
func TestTrunkENI_DeleteCooledDownENIs_ForgottenMetric(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	EniDetails1.deletionTimeStamp = time.Now().Add(-time.Second * 61)
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1)

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(createCoolDownMockCM("60"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))

	// Every delete attempt fails, so the ENI is retried MaxDeleteRetries times and then forgotten.
	ec2APIHelper.EXPECT().DisassociateTrunkInterface(&MockAssociationID1).Return(nil).Times(MaxDeleteRetries)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(MockError).Times(MaxDeleteRetries)

	before := testutil.ToFloat64(branchENIDeleteForgottenCount.WithLabelValues("max_delete_retries_exceeded"))

	// A single call retries the ENI in-loop until MaxDeleteRetries is exhausted and it is forgotten.
	trunkENI.DeleteCooledDownENIs()

	after := testutil.ToFloat64(branchENIDeleteForgottenCount.WithLabelValues("max_delete_retries_exceeded"))
	assert.Equal(t, float64(1), after-before, "expected one forgotten branch ENI to be counted")
	assert.Zero(t, len(trunkENI.deleteQueue))
}

// TestTrunkENI_PushBranchENIsToCoolDownQueue tests that ENIs are pushed to the delete queue if the pod is being deleted
func TestTrunkENI_PushBranchENIsToCoolDownQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1, EniDetails2}

	trunkENI.PushBranchENIsToCoolDownQueue(PodUID)
	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]

	assert.Equal(t, 2, len(trunkENI.deleteQueue))
	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[1])
	assert.False(t, isPresent)
}

// TestTrunkENI_Reconcile tests that resources used by  pods that no longer exists are cleaned up
func TestTrunkENI_Reconcile(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1, EniDetails2}

	// Pod 1 doesn't exist anymore
	podList := []v1.Pod{*MockPod2}

	leaked := trunkENI.Reconcile(podList)
	assert.True(t, leaked)
	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]

	assert.Equal(t, []*ENIDetails{EniDetails1, EniDetails2}, trunkENI.deleteQueue)
	assert.False(t, isPresent)
}

// TestTrunkENI_Reconcile_NoStateChange tests that no resources are deleted in case the pod still exist in the API server
func TestTrunkENI_Reconcile_NoStateChange(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1, EniDetails2}

	podList := []v1.Pod{*MockPod1, *MockPod2}

	leaked := trunkENI.Reconcile(podList)
	assert.False(t, leaked)

	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]
	assert.Zero(t, trunkENI.deleteQueue)
	assert.True(t, isPresent)
}

func TestTrunkENI_InitTrunkFromStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	mockInstance.EXPECT().SubnetID().Return(SubnetId)

	err := trunkENI.InitTrunkFromStatus(&rcv1alpha1.TrunkInterface{
		ID:       trunkId,
		SubnetID: SubnetId,
	}, []v1.Pod{*MockPod1, *MockPod2})

	assert.NoError(t, err)
	assert.Equal(t, trunkId, trunkENI.trunkENIId)
	branchENIs, isPresent := trunkENI.uidToBranchENIMap[PodUID]
	assert.True(t, isPresent)
	assert.Equal(t, Branch1Id, branchENIs[0].ID)
	assert.Equal(t, Branch2Id, branchENIs[1].ID)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	assert.Empty(t, trunkENI.deleteQueue)
}

func TestTrunkENI_ReconcileUnassignedBranchENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1}

	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	ec2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil)

	// The orphan-discovery metric must increment once per orphan branch ENI found (here: Branch2).
	before := testutil.ToFloat64(branchENIOrphanReclaimedCount.WithLabelValues("discovered"))

	found, err := trunkENI.ReconcileUnassignedBranchENIs()

	assert.NoError(t, err)
	assert.True(t, found)
	assert.Len(t, trunkENI.deleteQueue, 1)
	assert.Equal(t, Branch2Id, trunkENI.deleteQueue[0].ID)
	assert.True(t, trunkENI.usedVlanIds[VlanId2])

	after := testutil.ToFloat64(branchENIOrphanReclaimedCount.WithLabelValues("discovered"))
	assert.Equal(t, float64(1), after-before, "expected one orphan branch ENI discovery to be counted")
}

// TestTrunkENI_pushUnassignedBranchInterfacesToDeleteQueue_InvalidVlanId verifies that an ENI whose
// VLAN tag is out of the allocatable range is still enqueued for deletion, but with the reserved
// VLAN ID 0 so the later deleteENI -> freeVlanId call does not index out of bounds and panic.
func TestTrunkENI_pushUnassignedBranchInterfacesToDeleteQueue_InvalidVlanId(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	outOfRangeVlan := MaxAllocatableVlanIds + 5
	invalidVlanTag := []awsEc2Types.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(outOfRangeVlan)),
	}}
	interfaces := map[string]*awsEc2Types.NetworkInterface{
		Branch2Id: {
			InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
			NetworkInterfaceId: &Branch2Id,
			TagSet:             invalidVlanTag,
		},
	}

	found := trunkENI.pushUnassignedBranchInterfacesToDeleteQueue(interfaces)

	assert.True(t, found)
	assert.Len(t, trunkENI.deleteQueue, 1)
	assert.Equal(t, Branch2Id, trunkENI.deleteQueue[0].ID)
	// Out-of-range VLAN must be replaced with the reserved sentinel 0 so deleteENI skips freeVlanId.
	assert.Equal(t, 0, trunkENI.deleteQueue[0].VlanID)
	// The out-of-range index must never have been marked as used.
	assert.Equal(t, MaxAllocatableVlanIds, len(trunkENI.usedVlanIds))

	// deleteENI on the queued ENI must not panic even though the tag was invalid: with VlanID 0 it
	// skips freeVlanId entirely (freeVlanId with an out-of-range index would have panicked).
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&Branch2Id).Return(nil)
	assert.NotPanics(t, func() {
		_ = trunkENI.deleteENI(trunkENI.deleteQueue[0])
	})
}

// TestTrunkENI_pushUnassignedBranchInterfacesToDeleteQueue_MissingVlanTag verifies that a discovered
// orphan branch ENI whose VLAN tag is missing/unparseable (getVlanIdFromTag returns an error) is
// STILL enqueued for deletion with the reserved VLAN ID 0, rather than skipped. Skipping would leak a
// real orphan in EC2 indefinitely and make the orphan-discovered metric lie about "pushed to delete
// queue".
func TestTrunkENI_pushUnassignedBranchInterfacesToDeleteQueue_MissingVlanTag(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	// No VLAN tag at all -> getVlanIdFromTag returns an error.
	interfaces := map[string]*awsEc2Types.NetworkInterface{
		Branch2Id: {
			InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
			NetworkInterfaceId: &Branch2Id,
			TagSet:             []awsEc2Types.Tag{},
		},
	}

	found := trunkENI.pushUnassignedBranchInterfacesToDeleteQueue(interfaces)

	// The orphan must be enqueued (not skipped) so it actually gets deleted.
	assert.True(t, found)
	assert.Len(t, trunkENI.deleteQueue, 1)
	assert.Equal(t, Branch2Id, trunkENI.deleteQueue[0].ID)
	// Missing/invalid tag must fall back to the reserved sentinel 0 so deleteENI skips freeVlanId.
	assert.Equal(t, 0, trunkENI.deleteQueue[0].VlanID)

	// deleteENI on the queued ENI must not panic even though the tag was missing.
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&Branch2Id).Return(nil)
	assert.NotPanics(t, func() {
		_ = trunkENI.deleteENI(trunkENI.deleteQueue[0])
	})
}

func TestTrunkENI_InitTrunk(t *testing.T) {
	type args struct {
		instance ec2.EC2Instance
		podList  []v1.Pod
	}
	type fields struct {
		mockInstance     *mock_ec2.MockEC2Instance
		mockEC2APIHelper *mock_api.MockEC2APIHelper
		trunkENI         *trunkENI
	}
	testsTrunkENI_InitTrunk := []struct {
		name    string
		prepare func(f *fields)
		args    args
		wantErr bool
		asserts func(f *fields)
	}{
		{
			name: "TrunkNotExists, verifies trunk is created if it does not exist with no error",
			prepare: func(f *fields) {
				freeIndex := int32(2)
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(SecurityGroups)
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]awsEc2Types.InstanceNetworkInterface{}, nil)
				f.mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(freeIndex, nil)
				f.mockInstance.EXPECT().SubnetID().Return(SubnetId)
				f.mockEC2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&InstanceId, &SubnetId, SecurityGroups, f.trunkENI.nodeIDTag,
					&freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, nil, nil).Return(trunkInterface, nil)
			},
			// Pass nil to set the instance to fields.mockInstance in the function later
			args:    args{instance: nil, podList: []v1.Pod{*MockPod2}},
			wantErr: false,
			asserts: func(f *fields) {
				assert.Equal(t, trunkId, f.trunkENI.trunkENIId)
			},
		},
		{
			name: "ErrWhen_EmptyNWInterfaceResponse, verifies error is returned when interface type is nil",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(
					[]awsEc2Types.InstanceNetworkInterface{{InterfaceType: nil}}, nil)
			},
			args:    args{instance: nil, podList: []v1.Pod{*MockPod2}},
			wantErr: true,
			asserts: nil,
		},
		{
			name: "GetTrunkError, verifies error is returned when get trunkENI call fails",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(nil, MockError)
			},
			args:    args{instance: nil, podList: []v1.Pod{*MockPod2}},
			wantErr: true,
			asserts: nil,
		},
		{
			name: "GetFreeIndexFail, verifies error is returned if no free index exists",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]awsEc2Types.InstanceNetworkInterface{}, nil)
				f.mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int32(0), MockError)
			},
			args:    args{instance: nil, podList: []v1.Pod{*MockPod2}},
			wantErr: true,
			asserts: nil,
		},
		{
			name: "TrunkExists_WithBranches, verifies no error when trunk exists with branches",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{})
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil)
				f.mockInstance.EXPECT().SubnetID().Return(SubnetId)
				f.mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil)
			},
			args:    args{instance: FakeInstance, podList: []v1.Pod{*MockPod1, *MockPod2}},
			wantErr: false,
			asserts: func(f *fields) {
				branchENIs, isPresent := f.trunkENI.uidToBranchENIMap[PodUID]
				assert.True(t, isPresent)
				// Assert eni details are correct
				assert.Equal(t, Branch1Id, branchENIs[0].ID)
				assert.Equal(t, Branch2Id, branchENIs[1].ID)
				assert.Equal(t, VlanId1, branchENIs[0].VlanID)
				assert.Equal(t, VlanId2, branchENIs[1].VlanID)

				// Assert that Vlan ID's are marked as used and if you retry using then you get error
				assert.True(t, f.trunkENI.usedVlanIds[EniDetails1.VlanID])
				assert.True(t, f.trunkENI.usedVlanIds[EniDetails2.VlanID])

				// Assert no entry for pod that didn't have a branch ENI
				_, isPresent = f.trunkENI.uidToBranchENIMap[MockNamespacedName2]
				assert.False(t, isPresent)
			},
		},
		{
			name: "TrunkExists_DanglingENIs, verifies ENIs are pushed to delete queue if no pod exists",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{})
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil)
				f.mockInstance.EXPECT().SubnetID().Return(SubnetId)
				f.mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil)
			},
			args:    args{instance: FakeInstance, podList: []v1.Pod{*MockPod2}},
			wantErr: false,
			asserts: func(f *fields) {
				_, isPresent := f.trunkENI.uidToBranchENIMap[PodUID]
				assert.False(t, isPresent)
				_, isPresent = f.trunkENI.uidToBranchENIMap[MockNamespacedName2]
				assert.False(t, isPresent)

				assert.ElementsMatch(t, []string{EniDetails1.ID, EniDetails2.ID},
					[]string{f.trunkENI.deleteQueue[0].ID, f.trunkENI.deleteQueue[1].ID})
			},
		},
		{
			name: "TrunkExists_NotAttached, verifies error is returned if trunkENI is not attached",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(MockError)
			},
			args:    args{instance: FakeInstance, podList: []v1.Pod{*MockPod1, *MockPod2}},
			wantErr: true,
			asserts: nil,
		},
	}
	for _, tt := range testsTrunkENI_InitTrunk {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
			f := fields{
				mockInstance:     mockInstance,
				mockEC2APIHelper: mockEC2APIHelper,
				trunkENI:         trunkENI,
			}
			if tt.prepare != nil {
				tt.prepare(&f)
			}
			if tt.args.instance == nil {
				tt.args.instance = f.mockInstance
			}
			err := f.trunkENI.InitTrunk(tt.args.instance, tt.args.podList)
			assert.Equal(t, err != nil, tt.wantErr)
			if tt.asserts != nil {
				tt.asserts(&f)
			}
		})
	}
}

// TestTrunkENI_CreateAndAssociateBranchENIs test branch is created and associated with the trunk and valid eni details
// are returned
// withSecurityGroups clones an ENIDetails fixture and sets the in-memory securityGroups field
// that CreateAndAssociateBranchENIs now records on created ENIs (Phase-2 shadow instrumentation).
func withSecurityGroups(eni *ENIDetails, securityGroups []string) *ENIDetails {
	clone := *eni
	clone.securityGroups = securityGroups
	return &clone
}

func TestTrunkENI_CreateAndAssociateBranchENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(2)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, append(vlan2Tag, trunkENI.nodeIDTag...),
		nil, nil, gomock.Any()).Return(BranchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(mockAssociationOutput2, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	expectedENIDetails := []*ENIDetails{withSecurityGroups(EniDetails1, SecurityGroups), withSecurityGroups(EniDetails2, SecurityGroups)}

	assert.NoError(t, err)
	// VLan ID are marked as used
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	// The returned content is as expected
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

// TestTrunkENI_CreateAndAssociateBranchENIs_InstanceSecurityGroup test branch is created and with instance security group
// if no security group is passed.
func TestTrunkENI_CreateAndAssociateBranchENIs_InstanceSecurityGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(2)
	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(InstanceSecurityGroup)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, InstanceSecurityGroup,
		append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, InstanceSecurityGroup,
		append(vlan2Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(mockAssociationOutput2, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, []string{}, 2)
	expectedENIDetails := []*ENIDetails{withSecurityGroups(EniDetails1, InstanceSecurityGroup), withSecurityGroups(EniDetails2, InstanceSecurityGroup)}

	assert.NoError(t, err)
	// VLan ID are marked as used
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	// The returned content is as expected
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate tests if error is returned on associate then the created interfaces
// are pushed to the delete queue
func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorAssociate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	// M3 (design doc section 2.4): the failed association now also triggers one error-driven orphan
	// reclaim describe, which reads the subnet - hence one SubnetID call beyond the two creates.
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(3)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(2)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)

	// The reclaim describe returns the two branch ENIs this very allocation just created. Both are
	// still in pendingCreates, so the M5 G1 guard keeps the reclaim from enqueueing them and the
	// delete-queue assertion below is unchanged - proving M3 cannot cannibalize an in-flight create.
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			append(vlan2Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface2, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, MockError),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, MockError, err)
	assert.Equal(t, []*ENIDetails{withSecurityGroups(EniDetails1, SecurityGroups), withSecurityGroups(ENIDetailsMissingAssociationID, SecurityGroups)}, trunkENI.deleteQueue)
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate tests if error is returned on associate then the created interfaces
// are pushed to the delete queue
func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(1)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(1)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, append(vlan1Tag, trunkENI.nodeIDTag...),
			nil, nil, gomock.Any()).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, append(vlan2Tag, trunkENI.nodeIDTag...),
			nil, nil, gomock.Any()).Return(nil, MockError),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, MockError, err)
	assert.Equal(t, []*ENIDetails{withSecurityGroups(EniDetails1, SecurityGroups)}, trunkENI.deleteQueue)
}

func TestTrunkENI_Introspect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.uidToBranchENIMap[PodUID] = branchENIs1

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	response := trunkENI.Introspect()
	assert.Equal(t, response, IntrospectResponse{
		TrunkENIID:     trunkId,
		InstanceID:     InstanceId,
		PodToBranchENI: map[string][]ENIDetails{PodUID: {*EniDetails1}},
	},
	)
}

func createCoolDownMockCM(cooldownTime string) *v1.ConfigMap {
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.VpcCniConfigMapName,
			Namespace: config.KubeSystemNamespace,
		},
		Data: map[string]string{
			config.BranchENICooldownPeriodKey: cooldownTime,
		},
	}
}

func TestTrunkENI_getConnectionTrackingSpec_WithValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	tcp := int32(300)
	udpStream := int32(120)
	udp := int32(30)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(&tcp, &udpStream, &udp)

	spec := trunkENI.getConnectionTrackingSpec()
	assert.NotNil(t, spec)
	assert.Equal(t, &tcp, spec.TcpEstablishedTimeout)
	assert.Equal(t, &udpStream, spec.UdpStreamTimeout)
	assert.Equal(t, &udp, spec.UdpTimeout)
}

func TestTrunkENI_getConnectionTrackingSpec_NilValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)

	spec := trunkENI.getConnectionTrackingSpec()
	assert.Nil(t, spec)
}

// getMockHydratedTrunk returns a trunk initialized through the hydrate path
// (InitTrunkFromStatus with MockPod1 owning VlanId1 and VlanId2), whose branch ledger is
// therefore NOT yet verified against EC2.
func getMockHydratedTrunk(t *testing.T, ctrl *gomock.Controller) (*trunkENI, *mock_api.MockEC2APIHelper, *mock_ec2.MockEC2Instance) {
	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true
	trunkENI.ec2ApiHelper = mockHelper
	trunkENI.instance = mockInstance

	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	err := trunkENI.InitTrunkFromStatus(&rcv1alpha1.TrunkInterface{ID: trunkId, SubnetID: SubnetId},
		[]v1.Pod{*MockPod1})
	assert.NoError(t, err)
	assert.False(t, trunkENI.branchLedgerVerified,
		"hydrate path must leave the branch ledger unverified")

	return &trunkENI, mockHelper, mockInstance
}

// expectAllocationInstanceCalls registers the instance expectations CreateAndAssociateBranchENIs
// makes besides SubnetID (which the caller registers).
func expectAllocationInstanceCalls(mockInstance *mock_ec2.MockEC2Instance) {
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()
}

// TestTrunkENI_VerifyBranchLedger_HydratedTrunkAvoidsVlanCollision is the core VLAN-reuse-race
// regression test: a hydrated trunk defers the EC2 describe until the first allocation, and the
// gate discovers an orphan branch ENI holding the lowest free VLAN, so the new pod's allocation
// must receive a DIFFERENT VLAN and the orphan must be enqueued for deletion.
func TestTrunkENI_VerifyBranchLedger_HydratedTrunkAvoidsVlanCollision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHydratedTrunk(t, ctrl)
	expectAllocationInstanceCalls(mockInstance)

	// MockPod1 owns VLANs 1 and 2, VLAN 0 is reserved: without the gate the next allocation
	// would take VLAN 3. Seed EC2 with an orphan (no owning pod) holding exactly that VLAN.
	orphanVlan := 3
	orphanId := "eni-orphan-lowest-free-vlan"
	orphan := &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: &orphanId,
		TagSet: []awsEc2Types.Tag{{
			Key:   aws.String(config.VLandIDTag),
			Value: aws.String(strconv.Itoa(orphanVlan)),
		}, trunkIDTag},
	}
	attached := append(append([]*awsEc2Types.NetworkInterface{}, branchInterfaces...), orphan)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(attached, nil).Times(1)

	newBranchId := "eni-new-branch"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newBranchId, gomock.Any()).
		Return(mockAssociationOutput1, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)
	assert.Len(t, eniDetails, 1)

	// The allocated VLAN must NOT collide with the orphan's VLAN still occupied in EC2.
	assert.NotEqual(t, orphanVlan, eniDetails[0].VlanID,
		"allocation on a hydrated trunk must not reuse a VLAN occupied by an orphan in EC2")
	assert.True(t, trunkENI.usedVlanIds[orphanVlan], "the orphan's VLAN must be marked used")
	assert.True(t, trunkENI.branchLedgerVerified)

	// The orphan (and only the orphan - Branch1/Branch2 are pod-owned) is enqueued for deletion.
	assert.Len(t, trunkENI.deleteQueue, 1)
	assert.Equal(t, orphanId, trunkENI.deleteQueue[0].ID)
	assert.Equal(t, orphanVlan, trunkENI.deleteQueue[0].VlanID)
}

// TestTrunkENI_VerifyBranchLedger_EC2PathSkipsGate verifies a trunk initialized through the EC2
// path never runs the gate describe: InitTrunk itself lists branch ENIs once, and the subsequent
// allocation performs no further GetBranchNetworkInterface call (gomock would fail on an
// unexpected second call).
func TestTrunkENI_VerifyBranchLedger_EC2PathSkipsGate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true
	trunkENI.ec2ApiHelper = mockHelper
	trunkENI.instance = mockInstance

	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{}).AnyTimes()
	expectAllocationInstanceCalls(mockInstance)

	// EC2 init path: exactly ONE describe, owned by InitTrunk.
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	mockHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil).Times(1)

	err := trunkENI.InitTrunk(mockInstance, []v1.Pod{*MockPod1})
	assert.NoError(t, err)
	assert.True(t, trunkENI.branchLedgerVerified, "EC2 init path must mark the ledger verified")

	newBranchId := "eni-new-branch"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newBranchId, gomock.Any()).
		Return(mockAssociationOutput1, nil)

	// No GetBranchNetworkInterface expectation remains: the allocation must not describe again.
	_, err = trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)
}

// TestTrunkENI_VerifyBranchLedger_DescribeErrorFailsAllocationThenRetries verifies that a failed
// gate describe fails the allocation without marking the ledger verified, and the next allocation
// retries the describe and succeeds.
func TestTrunkENI_VerifyBranchLedger_DescribeErrorFailsAllocationThenRetries(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHydratedTrunk(t, ctrl)
	expectAllocationInstanceCalls(mockInstance)

	gomock.InOrder(
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, MockError),
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil),
	)

	// First allocation: describe fails -> allocation fails, no VLAN assigned, ledger unverified.
	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.Error(t, err)
	assert.False(t, trunkENI.branchLedgerVerified,
		"a failed verification must leave the ledger unverified")
	assert.Empty(t, trunkENI.deleteQueue, "a failed allocation before any VLAN assignment enqueues nothing")

	// Second allocation: describe retried and succeeds -> allocation proceeds.
	newBranchId := "eni-new-branch"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newBranchId, gomock.Any()).
		Return(mockAssociationOutput1, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)
	assert.Len(t, eniDetails, 1)
	assert.True(t, trunkENI.branchLedgerVerified)
}

// TestTrunkENI_VerifyBranchLedger_RunsExactlyOnce verifies the gate describes EC2 only on the
// first allocation: a second allocation on the (now verified) ledger makes no describe call.
func TestTrunkENI_VerifyBranchLedger_RunsExactlyOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHydratedTrunk(t, ctrl)
	expectAllocationInstanceCalls(mockInstance)

	// Exactly one describe across both allocations.
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil).Times(1)

	newBranchId1, newBranchId2 := "eni-new-branch-1", "eni-new-branch-2"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	first := mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId1, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).After(first).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId2, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, gomock.Any(), gomock.Any()).
		Return(mockAssociationOutput1, nil).Times(2)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)

	otherPod := MockPod2.DeepCopy()
	otherPod.UID = "uid-3"
	_, err = trunkENI.CreateAndAssociateBranchENIs(otherPod, SecurityGroups, 1)
	assert.NoError(t, err)
}

// TestTrunkENI_VerifyBranchLedger_InvalidVlanTagFallsBackToVlan0 verifies that an attached branch
// ENI with a missing or out-of-range VLAN tag does not panic the gate: it falls back to the
// reserved VLAN 0 (never freed on delete) and is still enqueued for deletion when unowned.
func TestTrunkENI_VerifyBranchLedger_InvalidVlanTagFallsBackToVlan0(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHydratedTrunk(t, ctrl)
	expectAllocationInstanceCalls(mockInstance)

	noTagId := "eni-orphan-no-vlan-tag"
	outOfRangeId := "eni-orphan-vlan-out-of-range"
	attached := []*awsEc2Types.NetworkInterface{
		{
			InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
			NetworkInterfaceId: &noTagId,
			TagSet:             []awsEc2Types.Tag{trunkIDTag},
		},
		{
			InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
			NetworkInterfaceId: &outOfRangeId,
			TagSet: []awsEc2Types.Tag{{
				Key:   aws.String(config.VLandIDTag),
				Value: aws.String(strconv.Itoa(MaxAllocatableVlanIds + 5)),
			}, trunkIDTag},
		},
	}
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(attached, nil).Times(1)

	newBranchId := "eni-new-branch"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newBranchId, gomock.Any()).
		Return(mockAssociationOutput1, nil)

	assert.NotPanics(t, func() {
		eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
		assert.NoError(t, err)
		assert.Len(t, eniDetails, 1)
	})

	assert.True(t, trunkENI.branchLedgerVerified)
	// Both un-taggable orphans are enqueued with the reserved VLAN 0.
	assert.Len(t, trunkENI.deleteQueue, 2)
	assert.ElementsMatch(t, []string{noTagId, outOfRangeId},
		[]string{trunkENI.deleteQueue[0].ID, trunkENI.deleteQueue[1].ID})
	assert.Equal(t, 0, trunkENI.deleteQueue[0].VlanID)
	assert.Equal(t, 0, trunkENI.deleteQueue[1].VlanID)
}

// TestTrunkENI_VerifyBranchLedger_MetricRecorded verifies the gate emits the
// branch_ledger_verify_total metric on both outcomes, giving live validation a direct proof the
// gate ran.
func TestTrunkENI_VerifyBranchLedger_MetricRecorded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHydratedTrunk(t, ctrl)
	expectAllocationInstanceCalls(mockInstance)

	verifiedBefore := testutil.ToFloat64(branchLedgerVerifyCount.WithLabelValues(ledgerVerifyResultVerified))
	errorBefore := testutil.ToFloat64(branchLedgerVerifyCount.WithLabelValues(ledgerVerifyResultError))

	gomock.InOrder(
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, MockError),
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.Error(t, err)

	newBranchId := "eni-new-branch"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newBranchId, gomock.Any()).
		Return(mockAssociationOutput1, nil)

	_, err = trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)

	assert.Equal(t, float64(1),
		testutil.ToFloat64(branchLedgerVerifyCount.WithLabelValues(ledgerVerifyResultError))-errorBefore)
	assert.Equal(t, float64(1),
		testutil.ToFloat64(branchLedgerVerifyCount.WithLabelValues(ledgerVerifyResultVerified))-verifiedBefore)
}

// TestTrunkENI_VerifyBranchLedger_OrphanCountMetric verifies the gate increments
// branch_ledger_verify_orphans_total by exactly the number of orphans it discovers (here: one
// orphan among three attached branch ENIs; Branch1/Branch2 are pod-owned).
func TestTrunkENI_VerifyBranchLedger_OrphanCountMetric(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHydratedTrunk(t, ctrl)
	expectAllocationInstanceCalls(mockInstance)

	orphanId := "eni-orphan-gate-metric"
	orphan := &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: &orphanId,
		TagSet: []awsEc2Types.Tag{{
			Key:   aws.String(config.VLandIDTag),
			Value: aws.String("7"),
		}, trunkIDTag},
	}
	attached := append(append([]*awsEc2Types.NetworkInterface{}, branchInterfaces...), orphan)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(attached, nil).Times(1)

	newBranchId := "eni-new-branch"
	mac := "FF:FF:FF:FF:FF:F1"
	ip := "192.168.0.77"
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newBranchId, MacAddress: &mac, PrivateIpAddress: &ip}, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newBranchId, gomock.Any()).
		Return(mockAssociationOutput1, nil)

	before := testutil.ToFloat64(branchLedgerVerifyOrphanCount)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)

	assert.Equal(t, float64(1), testutil.ToFloat64(branchLedgerVerifyOrphanCount)-before,
		"the gate discovered exactly one orphan, so the counter must increase by one")
}

// TestTrunkENI_ShadowReuse_ExactMatchWithinWindow verifies that releasing an ENI and then
// allocating with identical security groups within the shadow window counts one sg_match="exact"
// shadow reuse hit.
func TestTrunkENI_ShadowReuse_ExactMatchWithinWindow(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()
	trunkENI.lock.Lock()
	trunkENI.recordShadowReleaseLocked([]string{SecurityGroup2, SecurityGroup1}) // unsorted on purpose
	trunkENI.lock.Unlock()

	exactBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))
	mismatchBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch))

	trunkENI.observeShadowReuse([]string{SecurityGroup1, SecurityGroup2})

	assert.Equal(t, float64(1),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))-exactBefore)
	assert.Equal(t, float64(0),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch))-mismatchBefore)
}

// TestTrunkENI_ShadowReuse_MismatchLabelOnSGDifference verifies that when only records with
// different security groups are available within the window, the hit is counted with
// sg_match="mismatch" (a reuse would need one ModifyNetworkInterfaceAttribute).
func TestTrunkENI_ShadowReuse_MismatchLabelOnSGDifference(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()
	trunkENI.lock.Lock()
	trunkENI.recordShadowReleaseLocked([]string{"sg-different"})
	trunkENI.lock.Unlock()

	exactBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))
	mismatchBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch))

	trunkENI.observeShadowReuse([]string{SecurityGroup1})

	assert.Equal(t, float64(0),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))-exactBefore)
	assert.Equal(t, float64(1),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch))-mismatchBefore)
}

// TestTrunkENI_ShadowReuse_NoHitOutsideWindow verifies that a record older than the shadow window
// neither counts as a hit nor survives the lazy expiry.
func TestTrunkENI_ShadowReuse_NoHitOutsideWindow(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()
	trunkENI.lock.Lock()
	trunkENI.recordShadowReleaseLocked([]string{SecurityGroup1})
	// Backdate the record beyond the window.
	trunkENI.shadowReleased[0].releasedAt = time.Now().Add(-shadowReuseWindow - time.Minute)
	trunkENI.lock.Unlock()

	exactBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))
	mismatchBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch))

	trunkENI.observeShadowReuse([]string{SecurityGroup1})

	assert.Equal(t, float64(0),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))-exactBefore)
	assert.Equal(t, float64(0),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch))-mismatchBefore)
	assert.Empty(t, trunkENI.shadowReleased, "expired record must be pruned lazily on check")
}

// TestTrunkENI_ShadowReuse_RecordsEvictedBeyondCap verifies the per-trunk record list is bounded:
// pushing more than maxShadowRecordsPerTrunk records FIFO-evicts the oldest.
func TestTrunkENI_ShadowReuse_RecordsEvictedBeyondCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()
	trunkENI.lock.Lock()
	trunkENI.recordShadowReleaseLocked([]string{"sg-oldest"})
	for i := 0; i < maxShadowRecordsPerTrunk; i++ {
		trunkENI.recordShadowReleaseLocked([]string{fmt.Sprintf("sg-%d", i)})
	}
	trunkENI.lock.Unlock()

	assert.Len(t, trunkENI.shadowReleased, maxShadowRecordsPerTrunk)
	for _, rec := range trunkENI.shadowReleased {
		assert.NotEqual(t, []string{"sg-oldest"}, rec.sortedSecurityGroups,
			"the oldest record must have been FIFO-evicted")
	}
}

// TestTrunkENI_ShadowReuse_PodReleaseFeedsShadowRecords verifies the end-to-end shadow flow on the
// release side: PushBranchENIsToCoolDownQueue records the released ENI's security groups, so a
// following same-SG allocation counts an exact shadow hit.
func TestTrunkENI_ShadowReuse_PodReleaseFeedsShadowRecords(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()
	eni := &ENIDetails{ID: Branch1Id, VlanID: VlanId1, securityGroups: []string{SecurityGroup1, SecurityGroup2}}
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{eni}

	trunkENI.PushBranchENIsToCoolDownQueue(PodUID)
	assert.Len(t, trunkENI.shadowReleased, 1)

	exactBefore := testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))
	trunkENI.observeShadowReuse([]string{SecurityGroup1, SecurityGroup2})
	assert.Equal(t, float64(1),
		testutil.ToFloat64(orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact))-exactBefore)
}

// TestTrunkENI_U5_PendingCreateSkippedByGateAndSweep verifies M5 G1 (design doc section 2.6): an
// ENI in pendingCreates is never classified as an orphan by either the known-set builder shared by
// the gate and the sweep, or by the sweep's own re-check at enqueue time.
func TestTrunkENI_U5_PendingCreateSkippedByGateAndSweep(t *testing.T) {
	trunkENI := getMockTrunk()
	inflightId := "eni-inflight-create"
	trunkENI.pendingCreates[inflightId] = struct{}{}

	knownBranchENIs := trunkENI.knownBranchENIsLocked()
	_, known := knownBranchENIs[inflightId]
	assert.True(t, known, "an in-flight create must be part of the known set")

	orphanBefore := testutil.ToFloat64(branchENIOrphanReclaimedCount.WithLabelValues("discovered"))
	foundUnassigned := trunkENI.pushUnassignedBranchInterfacesToDeleteQueue(
		map[string]*awsEc2Types.NetworkInterface{
			inflightId: {NetworkInterfaceId: &inflightId, TagSet: vlan1Tag},
		})

	assert.False(t, foundUnassigned, "an in-flight create must not be discovered as an orphan")
	assert.Empty(t, trunkENI.deleteQueue, "an in-flight create must never be enqueued for deletion")
	assert.Equal(t, float64(0),
		testutil.ToFloat64(branchENIOrphanReclaimedCount.WithLabelValues("discovered"))-orphanBefore)
}

// TestTrunkENI_U6_DeleteQueueDedup verifies M5 G2 (design doc section 2.6): none of the three
// enqueue paths insert a second entry for an ENI ID already in the delete queue.
func TestTrunkENI_U6_DeleteQueueDedup(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[VlanId1] = true

	dedupBefore := testutil.ToFloat64(branchENIDeleteQueueDedupCount)

	// pushENIToDeleteQueue (used by the pod-delete path).
	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.pushENIToDeleteQueue(EniDetails1)
	assert.Len(t, trunkENI.deleteQueue, 1, "pushENIToDeleteQueue must not insert a duplicate ID")

	// PushENIsToFrontOfDeleteQueue (used by the create-failure path).
	trunkENI.PushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{EniDetails1})
	assert.Len(t, trunkENI.deleteQueue, 1, "PushENIsToFrontOfDeleteQueue must not insert a duplicate ID")

	// pushUnassignedBranchInterfacesToDeleteQueue (used by the gate and the sweep).
	foundUnassigned := trunkENI.pushUnassignedBranchInterfacesToDeleteQueue(
		map[string]*awsEc2Types.NetworkInterface{
			Branch1Id: {NetworkInterfaceId: &Branch1Id, TagSet: vlan1Tag},
		})
	assert.False(t, foundUnassigned,
		"an ENI already in the delete queue must not be re-discovered as an orphan")
	assert.Len(t, trunkENI.deleteQueue, 1,
		"pushUnassignedBranchInterfacesToDeleteQueue must not insert a duplicate ID")

	assert.Equal(t, float64(3),
		testutil.ToFloat64(branchENIDeleteQueueDedupCount)-dedupBefore,
		"all three duplicate attempts must be counted")
}

// TestTrunkENI_U7_FreeVlanIdOwnerAware verifies M5 G3 (design doc section 2.6): freeVlanId only
// frees a VLAN whose recorded owner matches the requesting ENI ID (or has no recorded owner, for
// legacy callers), and reserved VLAN 0 is never handed out for a caller to free in the first place.
func TestTrunkENI_U7_FreeVlanIdOwnerAware(t *testing.T) {
	trunkENI := getMockTrunk()

	// A VLAN with no recorded owner still frees (legacy/unknown-tag fallback paths).
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.freeVlanId(VlanId1, "eni-any")
	assert.False(t, trunkENI.usedVlanIds[VlanId1], "an unowned vlan must still free")

	// A VLAN owned by a different ENI must not be freed.
	trunkENI.usedVlanIds[VlanId2] = true
	trunkENI.vlanOwner[VlanId2] = "eni-owner"
	trunkENI.freeVlanId(VlanId2, "eni-not-the-owner")
	assert.True(t, trunkENI.usedVlanIds[VlanId2], "a vlan owned by another eni must not be freed")
	assert.Equal(t, "eni-owner", trunkENI.vlanOwner[VlanId2])

	// The rightful owner can free it.
	trunkENI.freeVlanId(VlanId2, "eni-owner")
	assert.False(t, trunkENI.usedVlanIds[VlanId2], "the rightful owner must be able to free the vlan")
	_, stillOwned := trunkENI.vlanOwner[VlanId2]
	assert.False(t, stillOwned, "the owner record must be cleared once freed")

	// Reserved VLAN 0 is initialized permanently used and is never handed out by assignVlanId,
	// so no caller ever reaches freeVlanId(0, ...) on a real trunk; markVlanAssignedWithOwnerLocked
	// also never records an owner for it.
	assert.NoError(t, trunkENI.markVlanAssignedWithOwnerLocked(0, "eni-x"))
	_, ownerRecordedForVlan0 := trunkENI.vlanOwner[0]
	assert.False(t, ownerRecordedForVlan0, "reserved vlan 0 must never get an owner")
}

// TestTrunkENI_RegressionHA is the design doc section 4 H-A regression: an ENI created in the
// Associate-to-cache window (present in EC2, attached, but not yet in the pod-owned ledger) must
// never be swept as an orphan by the ledger-verify gate.
func TestTrunkENI_RegressionHA(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, _ := getMockHydratedTrunk(t, ctrl)

	// Simulate CreateAndAssociateBranchENIs having just created and associated a new branch ENI
	// for a different pod, but not yet reached addBranchToCache.
	inflightId := "eni-inflight-associate-window"
	inflightVlan := 5
	trunkENI.pendingCreates[inflightId] = struct{}{}
	trunkENI.usedVlanIds[inflightVlan] = true
	trunkENI.vlanOwner[inflightVlan] = inflightId

	inflight := &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: &inflightId,
		TagSet: []awsEc2Types.Tag{{
			Key:   aws.String(config.VLandIDTag),
			Value: aws.String(strconv.Itoa(inflightVlan)),
		}, trunkIDTag},
	}
	attached := append(append([]*awsEc2Types.NetworkInterface{}, branchInterfaces...), inflight)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(attached, nil).Times(1)

	err := trunkENI.verifyBranchLedger()
	assert.NoError(t, err)
	assert.True(t, trunkENI.branchLedgerVerified)

	assert.Empty(t, trunkENI.deleteQueue,
		"an in-flight create must not be enqueued for deletion by the ledger-verify gate")
	assert.True(t, trunkENI.usedVlanIds[inflightVlan], "the in-flight create's vlan must remain reserved")
}

// TestTrunkENI_RegressionHB is the design doc section 4 H-B regression: an ENI already in the
// delete queue must not be re-enqueued by the sweep (G2), and even if a stale duplicate delete
// somehow still ran, owner-aware freeVlanId (G3) must not release a vlan that has since been
// reassigned to a new pod's branch ENI.
func TestTrunkENI_RegressionHB(t *testing.T) {
	trunkENI := getMockTrunk()
	// Reserve vlan 0 like a real trunk so assignVlanId's lowest-free scan lands on sharedVlan
	// once it is freed, instead of on the mock's otherwise-unreserved index 0.
	trunkENI.usedVlanIds[0] = true

	sharedVlan := 1
	oldENI := &ENIDetails{ID: "eni-old-awaiting-delete", VlanID: sharedVlan}
	trunkENI.usedVlanIds[sharedVlan] = true
	trunkENI.vlanOwner[sharedVlan] = oldENI.ID

	// The orphan sweep discovers the ENI awaiting deletion once...
	trunkENI.pushENIToDeleteQueue(oldENI)
	assert.Len(t, trunkENI.deleteQueue, 1)

	// ...and a second sweep pass (e.g. before EC2 confirms the delete) must not duplicate it (G2).
	foundUnassigned := trunkENI.pushUnassignedBranchInterfacesToDeleteQueue(
		map[string]*awsEc2Types.NetworkInterface{
			oldENI.ID: {NetworkInterfaceId: &oldENI.ID, TagSet: vlan1Tag},
		})
	assert.False(t, foundUnassigned)
	assert.Len(t, trunkENI.deleteQueue, 1, "the awaiting-delete ENI must not be duplicated in the queue")

	// The single queued entry is popped and actually deleted, freeing its vlan.
	popped, hasENI := trunkENI.popENIFromDeleteQueue()
	assert.True(t, hasENI)
	assert.Equal(t, oldENI.ID, popped.ID)
	trunkENI.freeVlanId(sharedVlan, popped.ID)
	assert.False(t, trunkENI.usedVlanIds[sharedVlan])

	// A new pod immediately grabs the now-free vlan.
	newVlan, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, sharedVlan, newVlan)
	trunkENI.addPendingCreate("eni-new-owner", newVlan)

	// If a stale duplicate of the old ENI's delete somehow still ran (the race G2 closes at the
	// source), owner-aware freeVlanId (G3) refuses to release the vlan out from under the new
	// owner.
	trunkENI.freeVlanId(sharedVlan, oldENI.ID)
	assert.True(t, trunkENI.usedVlanIds[sharedVlan],
		"the vlan must remain reserved for the new owner despite the stale duplicate free")
	assert.Equal(t, "eni-new-owner", trunkENI.vlanOwner[sharedVlan])
}

// --- M3: error-driven orphan reclaim (design doc section 2.4) ---------------------------------

// newReclaimTrunk returns a trunk wired with mocks and a trunk ID, ready for the error-driven
// reclaim path (which describes the trunk's branch ENIs).
func newReclaimTrunk(ctrl *gomock.Controller) (*trunkENI, *mock_api.MockEC2APIHelper) {
	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	return trunkENI, mockHelper
}

// TestTrunkENI_U4_ErrorDrivenReclaim_EnqueuesOrphan proves the reactive floor: after a failed
// branch-ENI addition, one describe runs and an attached-but-unknown ENI is enqueued for deletion.
func TestTrunkENI_U4_ErrorDrivenReclaim_EnqueuesOrphan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	orphanID := "eni-orphan-holding-slot"
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(
		[]*awsEc2Types.NetworkInterface{
			{NetworkInterfaceId: &orphanID, TagSet: vlan1Tag},
		}, nil).Times(1)

	orphanBefore := testutil.ToFloat64(branchENIErrorDrivenOrphanCount)

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("InsufficientCapacityOnTrunk: no capacity"))

	assert.Len(t, trunkENI.deleteQueue, 1, "the orphan wedging the trunk must be enqueued")
	assert.Equal(t, orphanID, trunkENI.deleteQueue[0].ID)
	assert.Equal(t, float64(1), testutil.ToFloat64(branchENIErrorDrivenOrphanCount)-orphanBefore)
	assert.Equal(t, float64(1), testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultReclaimed, reclaimErrorClassCapacity)))
}

// TestTrunkENI_U4_ErrorDrivenReclaim_VlanInUseClass proves a VLAN-collision failure also triggers
// the reclaim (design section 2.4: the trigger is ANY addition failure, not just capacity).
func TestTrunkENI_U4_ErrorDrivenReclaim_VlanInUseClass(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	orphanID := "eni-orphan-holding-vlan"
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(
		[]*awsEc2Types.NetworkInterface{
			{NetworkInterfaceId: &orphanID, TagSet: vlan2Tag},
		}, nil).Times(1)

	before := testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultReclaimed, reclaimErrorClassVlanInUse))

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("VlanId 2 is already in use on the trunk"))

	assert.Len(t, trunkENI.deleteQueue, 1)
	assert.Equal(t, float64(1), testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultReclaimed, reclaimErrorClassVlanInUse))-before)
}

// TestTrunkENI_U4_ErrorDrivenReclaim_CleanWhenEC2Agrees proves that when EC2 agrees with the ledger
// nothing is enqueued: the failure was not caused by an orphan.
func TestTrunkENI_U4_ErrorDrivenReclaim_CleanWhenEC2Agrees(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	owned := &ENIDetails{ID: "eni-owned-by-pod", VlanID: VlanId1}
	trunkENI.uidToBranchENIMap["pod-uid"] = []*ENIDetails{owned}
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(
		[]*awsEc2Types.NetworkInterface{
			{NetworkInterfaceId: &owned.ID, TagSet: vlan1Tag},
		}, nil).Times(1)

	before := testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultClean, reclaimErrorClassOther))

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("some unrelated transient failure"))

	assert.Empty(t, trunkENI.deleteQueue, "a pod-owned ENI must never be reclaimed")
	assert.Equal(t, float64(1), testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultClean, reclaimErrorClassOther))-before)
}

// TestTrunkENI_U4_ErrorDrivenReclaim_RateWindow proves the failure-path describe is bounded: a
// second failure inside errorDrivenReclaimWindow must not issue another describe.
func TestTrunkENI_U4_ErrorDrivenReclaim_RateWindow(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	// Exactly one describe for two back-to-back failures.
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(
		[]*awsEc2Types.NetworkInterface{}, nil).Times(1)

	skipBefore := testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultSkippedWindow, reclaimErrorClassCapacity))

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("capacity exceeded"))
	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("capacity exceeded"))

	assert.Equal(t, float64(1), testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultSkippedWindow, reclaimErrorClassCapacity))-skipBefore,
		"the second failure inside the window must be skipped, not describe again")
}

// TestTrunkENI_U4_ErrorDrivenReclaim_DescribeFailure proves a failed reclaim describe never panics
// and never mutates ledger state - the caller's original allocation error stands.
func TestTrunkENI_U4_ErrorDrivenReclaim_DescribeFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, MockError).Times(1)

	before := testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultDescribeError, reclaimErrorClassCapacity))

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("capacity exceeded"))

	assert.Empty(t, trunkENI.deleteQueue, "a failed describe must not change ledger state")
	assert.Equal(t, float64(1), testutil.ToFloat64(
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultDescribeError, reclaimErrorClassCapacity))-before)
}

// TestTrunkENI_ErrorDrivenReclaim_RespectsM5KnownSet proves the reclaim reuses the shared M5 known
// set: an in-flight create (hazard H-A) and an ENI already awaiting deletion (hazard H-B) are never
// reclaimed, so M3 cannot reintroduce either hazard.
func TestTrunkENI_ErrorDrivenReclaim_RespectsM5KnownSet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	inflightID := "eni-inflight-create"
	queuedID := "eni-awaiting-delete"
	trunkENI.pendingCreates[inflightID] = struct{}{}
	trunkENI.deleteQueue = append(trunkENI.deleteQueue, &ENIDetails{ID: queuedID, VlanID: VlanId2})

	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(
		[]*awsEc2Types.NetworkInterface{
			{NetworkInterfaceId: &inflightID, TagSet: vlan1Tag},
			{NetworkInterfaceId: &queuedID, TagSet: vlan2Tag},
		}, nil).Times(1)

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("capacity exceeded"))

	assert.Len(t, trunkENI.deleteQueue, 1, "no duplicate entry and no in-flight create enqueued")
	assert.Equal(t, queuedID, trunkENI.deleteQueue[0].ID)
}

// TestTrunkENI_RegressionE4 replays design hazard E4: orphans the ledger does not know occupy the
// trunk's real branch slots, so canCreateMore wrongly allows an allocation that then fails to
// associate. Without the reclaim the node loops create/delete forever. This asserts the wedge is
// broken - after the failure the orphans are queued for deletion, which is what frees the slots.
func TestTrunkENI_RegressionE4(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper := newReclaimTrunk(ctrl)
	orphan1 := "eni-orphan-1"
	orphan2 := "eni-orphan-2"
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(
		[]*awsEc2Types.NetworkInterface{
			{NetworkInterfaceId: &orphan1, TagSet: vlan1Tag},
			{NetworkInterfaceId: &orphan2, TagSet: vlan2Tag},
		}, nil).Times(1)

	// The ledger is empty: it believes the trunk is idle while EC2 has two attached orphans.
	assert.Empty(t, trunkENI.uidToBranchENIMap)

	trunkENI.reclaimOrphansAfterAddFailure(fmt.Errorf("InsufficientCapacityOnTrunk"))

	assert.Len(t, trunkENI.deleteQueue, 2, "both slot-holding orphans must be queued for deletion")
	queued := map[string]bool{}
	for _, eni := range trunkENI.deleteQueue {
		queued[eni.ID] = true
	}
	assert.True(t, queued[orphan1] && queued[orphan2])
}

// TestReclaimErrorClass documents that classification is best-effort observability and that an
// unrecognized error is still eligible for reclaim (reported as "other", never excluded).
func TestReclaimErrorClass(t *testing.T) {
	assert.Equal(t, reclaimErrorClassCapacity, reclaimErrorClass(fmt.Errorf("InsufficientCapacityOnTrunk")))
	assert.Equal(t, reclaimErrorClassCapacity, reclaimErrorClass(fmt.Errorf("ResourceLimitExceeded")))
	assert.Equal(t, reclaimErrorClassVlanInUse, reclaimErrorClass(fmt.Errorf("VlanId already in use")))
	assert.Equal(t, reclaimErrorClassOther, reclaimErrorClass(fmt.Errorf("RequestLimitExceeded throttle")))
	assert.Equal(t, reclaimErrorClassOther, reclaimErrorClass(nil))
}
