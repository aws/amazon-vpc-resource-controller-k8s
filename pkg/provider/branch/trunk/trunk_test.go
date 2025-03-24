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

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_cooldown "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/cooldown"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"

	"github.com/aws/aws-sdk-go/aws"
	awsEc2 "github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
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
	FakeInstance          = ec2.NewEC2Instance(NodeName, InstanceId, config.OSLinux)
	InstanceSecurityGroup = []string{"sg-1", "sg-2"}

	// Mock Pod 1
	MockPodName1      = "pod_name"
	MockPodNamespace1 = "pod_namespace"
	//PodNamespacedName1 = "pod_namespace/pod_name"
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
				"\"AssociationId\":\"trunk-assoc-0000000000000001\"}]"}},
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

	BranchInterface1 = &awsEc2.NetworkInterface{
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

	BranchInterface2 = &awsEc2.NetworkInterface{
		MacAddress:         &MacAddr2,
		NetworkInterfaceId: &Branch2Id,
		PrivateIpAddress:   &BranchIp2,
		Ipv6Address:        &BranchV6Ip2,
	}

	// Trunk Interface
	trunkId        = "eni-00000000000000002"
	trunkInterface = &awsEc2.NetworkInterface{
		InterfaceType:      aws.String("trunk"),
		NetworkInterfaceId: &trunkId,
		Attachment: &awsEc2.NetworkInterfaceAttachment{
			Status: aws.String(awsEc2.AttachmentStatusAttached),
		},
	}

	trunkIDTag = &awsEc2.Tag{
		Key:   aws.String(config.TrunkENIIDTag),
		Value: &trunkId,
	}

	vlan1Tag = []*awsEc2.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(VlanId1)),
	}, trunkIDTag}

	vlan2Tag = []*awsEc2.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(VlanId2)),
	}, trunkIDTag}

	instanceNwInterfaces = []*awsEc2.InstanceNetworkInterface{
		{
			InterfaceType:      aws.String("trunk"),
			NetworkInterfaceId: &trunkId,
		},
	}

	branchInterfaces = []*awsEc2.NetworkInterface{
		{
			InterfaceType:      aws.String("branch"),
			NetworkInterfaceId: &EniDetails1.ID,
			TagSet:             vlan1Tag,
		},
		{
			InterfaceType:      aws.String("branch"),
			NetworkInterfaceId: &EniDetails2.ID,
			TagSet:             vlan2Tag,
		},
	}

	mockAssociationOutput1 = &awsEc2.AssociateTrunkInterfaceOutput{
		InterfaceAssociation: &awsEc2.TrunkInterfaceAssociation{
			AssociationId: &MockAssociationID1,
		},
	}
	mockAssociationOutput2 = &awsEc2.AssociateTrunkInterfaceOutput{
		InterfaceAssociation: &awsEc2.TrunkInterfaceAssociation{
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
	*mock_ec2.MockEC2Instance) {
	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true
	trunkENI.ec2ApiHelper = mockHelper
	trunkENI.instance = mockInstance

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
		nodeNameTag: []*awsEc2.Tag{
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
	trunkENI.freeVlanId(0)

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
				freeIndex := int64(2)
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(SecurityGroups)
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]*awsEc2.InstanceNetworkInterface{}, nil)
				f.mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(freeIndex, nil)
				f.mockInstance.EXPECT().SubnetID().Return(SubnetId)
				f.mockEC2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&InstanceId, &SubnetId, SecurityGroups, f.trunkENI.nodeNameTag,
					&freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, nil).Return(trunkInterface, nil)
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
					[]*awsEc2.InstanceNetworkInterface{{InterfaceType: nil}}, nil)

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
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]*awsEc2.InstanceNetworkInterface{}, nil)
				f.mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(0), MockError)
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
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, awsEc2.AttachmentStatusAttached).Return(nil)
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
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, awsEc2.AttachmentStatusAttached).Return(nil)
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
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, awsEc2.AttachmentStatusAttached).Return(MockError)
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
func TestTrunkENI_CreateAndAssociateBranchENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(2)

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		append(vlan1Tag, trunkENI.nodeNameTag...), nil, nil).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, append(vlan2Tag, trunkENI.nodeNameTag...),
		nil, nil).Return(BranchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(mockAssociationOutput2, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

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

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, InstanceSecurityGroup,
		append(vlan1Tag, trunkENI.nodeNameTag...), nil, nil).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, InstanceSecurityGroup,
		append(vlan2Tag, trunkENI.nodeNameTag...), nil, nil).Return(BranchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(mockAssociationOutput2, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, []string{}, 2)
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

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
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(2)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			append(vlan1Tag, trunkENI.nodeNameTag...), nil, nil).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			append(vlan2Tag, trunkENI.nodeNameTag...), nil, nil).Return(BranchInterface2, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, MockError),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, MockError, err)
	assert.Equal(t, []*ENIDetails{EniDetails1, ENIDetailsMissingAssociationID}, trunkENI.deleteQueue)
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate tests if error is returned on associate then the created interfaces
// are pushed to the delete queue
func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(1)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(1)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, append(vlan1Tag, trunkENI.nodeNameTag...),
			nil, nil).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, append(vlan2Tag, trunkENI.nodeNameTag...),
			nil, nil).Return(nil, MockError),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, MockError, err)
	assert.Equal(t, []*ENIDetails{EniDetails1}, trunkENI.deleteQueue)
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
		PodToBranchENI: map[string][]ENIDetails{PodUID: {*EniDetails1}}},
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
