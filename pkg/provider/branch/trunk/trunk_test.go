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
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_cooldown "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/cooldown"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	ec2Errors "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/errors"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsEc2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsEc2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
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
		SubnetId:           &SubnetId,
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
			SubnetId:           &SubnetId,
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

	MockError              = fmt.Errorf("mock error")
	MockDuplicateVlanError = fmt.Errorf("associating: %w", &smithy.GenericAPIError{
		Code: ec2Errors.DuplicateVlanID, Message: "VlanId '2' is in use"})
)

// queuedENIIDs returns delete queue IDs.
func queuedENIIDs(trunkENI *trunkENI) []string {
	ids := make([]string, 0, len(trunkENI.deleteQueue))
	for _, eni := range trunkENI.deleteQueue {
		ids = append(ids, eni.ID)
	}
	return ids
}

// assertAllQueuedENIsStamped checks cooldown bookkeeping.
func assertAllQueuedENIsStamped(t *testing.T, trunkENI *trunkENI) {
	t.Helper()
	for _, eni := range trunkENI.deleteQueue {
		assert.False(t, eni.deletionTimeStamp.IsZero(), "eni %s must carry a cool-down timestamp", eni.ID)
	}
}

func getMockHelperInstanceAndTrunkObject(ctrl *gomock.Controller) (*trunkENI, *mock_api.MockEC2APIHelper,
	*mock_ec2.MockEC2Instance,
) {
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
		log:                 log,
		usedVlanIds:         make([]bool, MaxAllocatableVlanIds),
		uidToBranchENIMap:   map[string][]*ENIDetails{},
		branchStateVerified: true,
		nodeIDTag: []awsEc2Types.Tag{
			{
				Key:   aws.String(config.NetworkInterfaceNodeIDKey),
				Value: aws.String(FakeInstance.InstanceID()),
			},
		},
	}
}

func TestPrometheusRegisterConcurrent(t *testing.T) {
	const callers = 32

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			PrometheusRegister()
		}()
	}

	close(start)
	wg.Wait()
}

func TestNewTrunkENI(t *testing.T) {
	trunkENI := NewTrunkENI(zap.New(), FakeInstance, nil)
	assert.NotNil(t, trunkENI)
}

func TestTrunkENI_CreateAndAssociateBranchENIs_QueuesFailedENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	throttleErr := fmt.Errorf("associating: %w", &smithy.GenericAPIError{
		Code: "RequestLimitExceeded", Message: "Request limit exceeded."})

	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, gomock.Any()).
		Return(nil, throttleErr)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.Error(t, err)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI))
}

func TestTrunkENI_CreateAndAssociateBranchENIs_NoReclaimAtCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().Type().Return(InstanceType)

	limit := vpc.Limits[InstanceType].BranchInterface
	for i := 0; i < limit; i++ {
		trunkENI.deleteQueue = append(trunkENI.deleteQueue, &ENIDetails{
			ID: fmt.Sprintf("eni-capacity-%d", i),
		})
	}

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.ErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
}

func TestTrunkENI_canCreateMoreIncludesPendingCreates(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	mockInstance.EXPECT().Type().Return(InstanceType).Times(3)

	limit := vpc.Limits[InstanceType].BranchInterface
	for i := 0; i < limit-1; i++ {
		trunkENI.deleteQueue = append(trunkENI.deleteQueue, &ENIDetails{
			ID: fmt.Sprintf("eni-capacity-%d", i),
		})
	}

	assert.True(t, trunkENI.canCreateMore(1))
	assert.False(t, trunkENI.canCreateMore(1))

	trunkENI.lock.Lock()
	trunkENI.pendingCreate--
	trunkENI.lock.Unlock()

	assert.True(t, trunkENI.canCreateMore(1))
}

func TestTrunkENI_canCreateMoreIsAtomic(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	limit := vpc.Limits[InstanceType].BranchInterface
	mockInstance.EXPECT().Type().Return(InstanceType).Times(limit * 2)

	var wg sync.WaitGroup
	results := make(chan bool, limit*2)
	for i := 0; i < limit*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- trunkENI.canCreateMore(1)
		}()
	}
	wg.Wait()
	close(results)

	reserved := 0
	for result := range results {
		if result {
			reserved++
		}
	}
	assert.Equal(t, limit, reserved)
	assert.Equal(t, limit, trunkENI.pendingCreate)
}

func TestTrunkENI_CreateAndAssociateBranchENIs_ReservesCapacityBeforeEC2Create(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	limit := vpc.Limits[InstanceType].BranchInterface
	for i := 0; i < limit-1; i++ {
		trunkENI.deleteQueue = append(trunkENI.deleteQueue, &ENIDetails{
			ID: fmt.Sprintf("eni-capacity-%d", i),
		})
	}

	mockInstance.EXPECT().Type().Return(InstanceType).Times(2)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)

	createStarted := make(chan struct{})
	allowCreate := make(chan struct{})
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).
		DoAndReturn(func(*string, *string, []string, []awsEc2Types.Tag, *config.IPResourceCount, *string,
			*awsEc2Types.ConnectionTrackingSpecificationRequest,
		) (*awsEc2Types.NetworkInterface, error) {
			close(createStarted)
			<-allowCreate
			return BranchInterface1, nil
		})
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).
		Return(mockAssociationOutput1, nil)

	firstDone := make(chan error)
	go func() {
		_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
		firstDone <- err
	}()
	<-createStarted

	secondPod := MockPod2.DeepCopy()
	secondPod.UID = types.UID("uid-3")
	_, err := trunkENI.CreateAndAssociateBranchENIs(secondPod, SecurityGroups, 1)
	assert.ErrorIs(t, err, ErrCurrentlyAtMaxCapacity)

	close(allowCreate)
	assert.NoError(t, <-firstDone)
	assert.Zero(t, trunkENI.pendingCreate)
	assert.Equal(t, []*ENIDetails{EniDetails1}, trunkENI.uidToBranchENIMap[PodUID2])
}

func TestTrunkENI_InitFromNodeNetworkState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod1})
	assert.NoError(t, err)

	assert.Equal(t, trunkId, trunkENI.trunkENIId)
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
	assert.True(t, trunkENI.usedVlanIds[1])
	assert.True(t, trunkENI.usedVlanIds[2])
	assert.False(t, trunkENI.branchStateVerified)
}

func TestTrunkENI_RecoverBranchState_ReservesTransitionalENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	ownedPod := podWithBranches(PodUID, []*ENIDetails{{ID: Branch1Id, VlanID: VlanId1}})
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{ownedPod}))

	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(
		[]*awsEc2Types.NetworkInterface{
			branchENIWithVlanTag(Branch1Id, VlanId1),
			branchENIWithVlanTag(Branch2Id, VlanId2),
		}, nil)

	listCalls := 0
	err := trunkENI.RecoverBranchState(func() ([]v1.Pod, error) {
		listCalls++
		return []v1.Pod{ownedPod}, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, listCalls)
	assert.True(t, trunkENI.branchStateVerified)
	assert.Equal(t, []string{Branch2Id}, queuedENIIDs(trunkENI))
	assertAllQueuedENIsStamped(t, trunkENI)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])

	newENIID := "eni-after-recovery"
	newMAC := "00:11:22:33:44:55"
	newIPv4 := "192.168.0.17"
	newAssociationID := "trunk-assoc-after-recovery"
	newInterface := &awsEc2Types.NetworkInterface{
		NetworkInterfaceId: &newENIID,
		MacAddress:         &newMAC,
		PrivateIpAddress:   &newIPv4,
	}
	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		gomock.Any(), nil, nil, gomock.Any()).DoAndReturn(
		func(_ *string, _ *string, _ []string, tags []awsEc2Types.Tag, _ *config.IPResourceCount, _ *string,
			_ *awsEc2Types.ConnectionTrackingSpecificationRequest,
		) (*awsEc2Types.NetworkInterface, error) {
			for _, tag := range tags {
				if aws.ToString(tag.Key) == config.VLandIDTag {
					assert.Equal(t, "3", aws.ToString(tag.Value))
				}
			}
			return newInterface, nil
		})
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newENIID, 3).Return(
		&awsEc2.AssociateTrunkInterfaceOutput{
			InterfaceAssociation: &awsEc2Types.TrunkInterfaceAssociation{
				AssociationId: &newAssociationID,
			},
		}, nil)

	allocated, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1)
	assert.NoError(t, err)
	assert.Len(t, allocated, 1)
	assert.Equal(t, 3, allocated[0].VlanID)
	assert.Equal(t, newENIID, allocated[0].ID)
}

func TestTrunkENI_RecoverBranchState_ConcurrentCallersShareOneRecovery(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, nil))
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).
		DoAndReturn(func(*string, *string) ([]*awsEc2Types.NetworkInterface, error) {
			time.Sleep(20 * time.Millisecond)
			return nil, nil
		}).Times(1)

	const callers = 16
	var wg sync.WaitGroup
	var listLock sync.Mutex
	listCalls := 0
	errs := make(chan error, callers)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			errs <- trunkENI.RecoverBranchState(func() ([]v1.Pod, error) {
				listLock.Lock()
				listCalls++
				listLock.Unlock()
				return nil, nil
			})
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
	assert.Equal(t, 1, listCalls)
	assert.True(t, trunkENI.branchStateVerified)
}

func TestTrunkENI_RecoverBranchState_VerifiedFastPathDoesNotWaitForAllocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.branchStateVerified = true

	// Model an allocation holding the shared recovery gate.
	trunkENI.branchStateGate.RLock()
	defer trunkENI.branchStateGate.RUnlock()
	recovered := make(chan error, 1)
	go func() {
		recovered <- trunkENI.RecoverBranchState(func() ([]v1.Pod, error) {
			return nil, fmt.Errorf("verified trunk should not recover again")
		})
	}()

	select {
	case err := <-recovered:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("verified recovery blocked behind an allocation")
	}
}

func TestTrunkENI_RecoverBranchState_FailureRemainsRetryable(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, nil))
	gomock.InOrder(
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(nil, MockError),
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(nil, nil),
	)

	listCalls := 0
	listPods := func() ([]v1.Pod, error) {
		listCalls++
		return nil, nil
	}
	assert.ErrorIs(t, trunkENI.RecoverBranchState(listPods), MockError)
	assert.False(t, trunkENI.branchStateVerified)

	assert.NoError(t, trunkENI.RecoverBranchState(listPods))
	assert.True(t, trunkENI.branchStateVerified)
	assert.Equal(t, 2, listCalls)
}

func TestTrunkENI_RecoverBranchState_UnreadableOwnershipQueuesUnownedENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, nil))
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(
		[]*awsEc2Types.NetworkInterface{branchENIWithVlanTag(Branch1Id, VlanId1)}, nil)

	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:         types.UID("pod-with-unreadable-ownership"),
			Annotations: map[string]string{config.ResourceNamePodENI: "not-json"},
		},
		Spec: v1.PodSpec{NodeName: NodeName},
	}
	assert.NoError(t, trunkENI.RecoverBranchState(func() ([]v1.Pod, error) {
		return []v1.Pod{pod}, nil
	}))

	assert.True(t, trunkENI.branchStateVerified)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI))
}

// podWithBranches adds the given ENIs to a Pod annotation.
func podWithBranches(uid string, enis []*ENIDetails) v1.Pod {
	raw, _ := json.Marshal(enis)
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:         types.UID(uid),
			Name:        uid,
			Namespace:   "ns",
			Annotations: map[string]string{config.ResourceNamePodENI: string(raw)},
		},
		Spec: v1.PodSpec{NodeName: NodeName},
	}
}

func TestTrunkENI_InitFromNodeNetworkState_SharedVlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	podA := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-1", VlanID: 7}})
	podB := podWithBranches("uid-b", []*ENIDetails{{ID: "eni-2", VlanID: 7}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{podA, podB})
	assert.NoError(t, err)
	assert.Equal(t, "eni-1", trunkENI.uidToBranchENIMap["uid-a"][0].ID)
	assert.Equal(t, "eni-2", trunkENI.uidToBranchENIMap["uid-b"][0].ID)
	assert.True(t, trunkENI.usedVlanIds[7])
}

func TestTrunkENI_InitFromNodeNetworkState_OutOfRangeVlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-1", VlanID: MaxAllocatableVlanIds}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{pod})
	assert.NoError(t, err)
	assert.Empty(t, trunkENI.uidToBranchENIMap)
}

func TestTrunkENI_InitFromNodeNetworkState_VlanZeroIsInvalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-vlan0", VlanID: 0}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{pod})
	assert.NoError(t, err)
	assert.Empty(t, trunkENI.uidToBranchENIMap)
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
	trunkENI.usedVlanIds[0] = true // reserved, as NewTrunkENI does

	// Assign single Vlan Id
	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 1, id)

	// Free the vlan Id
	trunkENI.freeVlanId(1)

	// Assign single Vlan Id again
	id, err = trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 1, id)
}

func TestTrunkENI_markVlanAssigned(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true // reserved, as NewTrunkENI does

	// Mark a Vlan as assigned
	trunkENI.markVlanAssigned(1)

	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 2, id)
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

func TestTrunkENI_addBranchENIsToLedger(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.addBranchENIsToLedger(PodUID, branchENIs1)

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

func TestTrunkENI_peekENIFromDeleteQueueDoesNotRemove(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	eniDetails, hasENI := trunkENI.peekENIFromDeleteQueue()

	assert.True(t, hasENI)
	assert.Equal(t, EniDetails1, eniDetails)
	assert.Len(t, trunkENI.deleteQueue, 1)

	trunkENI.removeENIFromDeleteQueue(EniDetails1, false)
	_, hasENI = trunkENI.peekENIFromDeleteQueue()
	assert.False(t, hasENI)
}

func TestTrunkENI_decodeBranchInterfacesUsedByPod(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs, usable := trunkENI.decodeBranchInterfacesUsedByPod(MockPod1)

	assert.True(t, usable)
	assert.Equal(t, 2, len(branchENIs))
	assert.Equal(t, EniDetails1, branchENIs[0])
	assert.Equal(t, EniDetails2, branchENIs[1])
}

func TestTrunkENI_decodeBranchInterfacesUsedByPod_MissingAnnotation(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs, usable := trunkENI.decodeBranchInterfacesUsedByPod(MockPod2)

	assert.True(t, usable)
	assert.Equal(t, 0, len(branchENIs))
}

func TestTrunkENI_decodeBranchInterfacesUsedByPod_Unusable(t *testing.T) {
	trunkENI := getMockTrunk()

	for name, annotation := range map[string]string{
		"malformed json": "{not-json",
		"empty eni id":   `[{"eniId":"","vlanId":1}]`,
	} {
		t.Run(name, func(t *testing.T) {
			pod := MockPod2.DeepCopy()
			pod.Annotations = map[string]string{config.ResourceNamePodENI: annotation}

			branchENIs, usable := trunkENI.decodeBranchInterfacesUsedByPod(pod)
			assert.False(t, usable)
			assert.Empty(t, branchENIs)
		})
	}
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
			name: "Vlan_NotFreed, queue removal owns VLAN release",
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
				assert.True(t, f.trunkENI.usedVlanIds[VlanId1])
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
				assert.True(t, f.trunkENI.usedVlanIds[VlanId1])
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
				assert.True(t, f.trunkENI.usedVlanIds[VlanId2])
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

func TestTrunkENI_DeleteCooledDownENIs_FreesVLANAfterLastReference(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	failedCreate := &ENIDetails{ID: "eni-failed-create", VlanID: VlanId1}
	orphan := &ENIDetails{
		ID:                "eni-associated-orphan",
		VlanID:            VlanId1,
		deletionTimeStamp: time.Now(),
	}
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.deleteQueue = []*ENIDetails{failedCreate, orphan}

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).
		Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))

	mockHelper.EXPECT().DeleteNetworkInterface(&failedCreate.ID).Return(nil)
	trunkENI.DeleteCooledDownENIs()

	assert.Equal(t, []*ENIDetails{orphan}, trunkENI.deleteQueue)
	assert.True(t, trunkENI.usedVlanIds[VlanId1],
		"VLAN must remain reserved while the orphan queue entry still references it")

	orphan.deletionTimeStamp = time.Now().Add(-time.Hour)
	mockHelper.EXPECT().DeleteNetworkInterface(&orphan.ID).Return(nil)
	trunkENI.DeleteCooledDownENIs()

	assert.Empty(t, trunkENI.deleteQueue)
	assert.False(t, trunkENI.usedVlanIds[VlanId1],
		"VLAN is released only after its final ledger/deleteQueue reference is removed")
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
				assert.Equal(t, SubnetId, f.trunkENI.TrunkSubnetID())
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
				f.mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(branchInterfaces, nil)
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
				assert.Equal(t, SubnetId, f.trunkENI.TrunkSubnetID())
			},
		},
		{
			name: "TrunkExists_DanglingENIs, verifies ENIs are pushed to delete queue if no pod exists",
			prepare: func(f *fields) {
				f.mockInstance.EXPECT().InstanceID().Return(InstanceId)
				f.mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{})
				f.mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
				f.mockEC2APIHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil)
				f.mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(branchInterfaces, nil)
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
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

	assert.NoError(t, err)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
	assert.Zero(t, trunkENI.pendingCreate)
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
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

	assert.NoError(t, err)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorAssociate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(2)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(mockAssociationOutput1, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			append(vlan2Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface2, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, MockDuplicateVlanError),
	)
	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
	assert.ElementsMatch(t, []string{EniDetails1.ID, ENIDetailsMissingAssociationID.ID}, queuedENIIDs(trunkENI))
	assert.Zero(t, trunkENI.pendingCreate)
}

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
	assert.Equal(t, []string{EniDetails1.ID}, queuedENIIDs(trunkENI))
	assert.False(t, trunkENI.usedVlanIds[VlanId2])
	assert.Zero(t, trunkENI.pendingCreate)
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

// branchENIWithVlanTag builds a tagged Branch ENI.
func branchENIWithVlanTag(id string, vlanID int) *awsEc2Types.NetworkInterface {
	return &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: aws.String(id),
		Status:             awsEc2Types.NetworkInterfaceStatusInUse,
		TagSet:             []awsEc2Types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String(strconv.Itoa(vlanID))}},
	}
}

// expectInitTrunkExistingTrunk configures an existing trunk.
func expectInitTrunkExistingTrunk(mockHelper *mock_api.MockEC2APIHelper, mockInstance *mock_ec2.MockEC2Instance,
	branches []*awsEc2Types.NetworkInterface,
) {
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{}).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	mockHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, nil).Return(branches, nil)
}

func TestTrunkENI_InitTrunk_PreservesENIsSharingAnnotationVlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches(PodUID, []*ENIDetails{{ID: Branch1Id, VlanID: 1}, {ID: Branch2Id, VlanID: 1}})
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, 1),
		branchENIWithVlanTag(Branch2Id, 1),
	})

	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
	assert.Equal(t, Branch1Id, trunkENI.uidToBranchENIMap[PodUID][0].ID)
	assert.Equal(t, Branch2Id, trunkENI.uidToBranchENIMap[PodUID][1].ID)
	assert.True(t, trunkENI.usedVlanIds[1])
	assert.Empty(t, queuedENIIDs(trunkENI))
}

func TestTrunkENI_InitTrunk_RejectsOutOfRangeVlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches(PodUID, []*ENIDetails{{ID: Branch1Id, VlanID: MaxAllocatableVlanIds}})
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, MaxAllocatableVlanIds),
	})

	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	assert.Empty(t, trunkENI.uidToBranchENIMap[PodUID])
}

func TestTrunkENI_InitTrunk_UsesEC2VlanTag(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches(PodUID, []*ENIDetails{{ID: Branch1Id, VlanID: 1}})
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, 7),
	})
	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 1)
	assert.Equal(t, 7, trunkENI.uidToBranchENIMap[PodUID][0].VlanID)
	assert.False(t, trunkENI.usedVlanIds[1])
	assert.True(t, trunkENI.usedVlanIds[7])
}

func TestTrunkENI_InitFromNodeNetworkState_SkipsUnusableAnnotation(t *testing.T) {
	for name, annotation := range map[string]string{
		"malformed json": "{not-json",
		"empty eni id":   `[{"eniId":"","vlanId":1}]`,
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)
			pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("uid-bad"), Name: "bad", Namespace: "ns",
				Annotations: map[string]string{config.ResourceNamePodENI: annotation},
			}}

			err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{pod})
			assert.NoError(t, err)
			assert.Empty(t, trunkENI.uidToBranchENIMap)
		})
	}
}

func TestTrunkENI_InitTrunk_UnusableAnnotationQueuesUnownedENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{
		UID: MockPodUID1, Name: MockPodName1, Namespace: MockPodNamespace1,
		Annotations: map[string]string{config.ResourceNamePodENI: "{not-json"},
	}}
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, 1),
	})

	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	assert.Empty(t, trunkENI.uidToBranchENIMap[PodUID])
	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI))
	assert.True(t, trunkENI.usedVlanIds[1])
	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.NotEqual(t, 1, id)
}
