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
	// MockDuplicateVlanError carries the EC2 error code that marks a ledger
	// contradiction and therefore triggers reactive orphan reclaim in tests.
	MockDuplicateVlanError = fmt.Errorf("associating: %w", &smithy.GenericAPIError{
		Code: ec2Errors.DuplicateVlanID, Message: "VlanId '2' is in use"})
)

// queuedENIIDs returns the delete queue contents by id, so assertions do not depend
// on mutable bookkeeping fields like the cool-down timestamp.
func queuedENIIDs(trunkENI *trunkENI) []string {
	ids := make([]string, 0, len(trunkENI.deleteQueue))
	for _, eni := range trunkENI.deleteQueue {
		ids = append(ids, eni.ID)
	}
	return ids
}

// assertAllQueuedENIsStamped asserts every queued ENI carries a deletion timestamp,
// which is what keeps its capacity slot accounted for while it cools down.
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
		log:               log,
		usedVlanIds:       make([]bool, MaxAllocatableVlanIds),
		uidToBranchENIMap: map[string][]*ENIDetails{},
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

// TestIsLedgerContradictionError verifies only the duplicate-VLAN code counts
// as a contradiction; throttling and plain errors must not trigger reclaim.
func TestIsLedgerContradictionError(t *testing.T) {
	assert.True(t, isLedgerContradictionError(MockDuplicateVlanError))
	assert.False(t, isLedgerContradictionError(MockError))
	assert.False(t, isLedgerContradictionError(fmt.Errorf("wrap: %w",
		&smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "Request limit exceeded."})))
	assert.False(t, isLedgerContradictionError(fmt.Errorf("wrap: %w",
		&smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "not authorized"})))
	assert.False(t, isLedgerContradictionError(nil))
}

// TestTrunkENI_CreateAndAssociateBranchENIs_NoReclaimOnThrottle verifies a
// throttled association does not spend a describe: no GetBranchNetworkInterface
// expectation is registered, so a reclaim attempt would fail the mock.
func TestTrunkENI_CreateAndAssociateBranchENIs_NoReclaimOnThrottle(t *testing.T) {
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

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.Error(t, err)
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

	// No EC2 helper expectation is registered: capacity is a normal retry
	// signal, not a reason to Describe or rebuild the trunk ledger.
	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.ErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
	assert.False(t, trunkENI.isOrphanCheckCompleted())
}

// TestTrunkENI_reclaimOrphans_SkipsUnassociatedENI verifies reclaim never
// touches an ENI that is not associated to the trunk: it holds no VLAN, and it
// may be a concurrent create whose RPC response has not returned yet.
func TestTrunkENI_reclaimOrphans_SkipsUnassociatedENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().SubnetID().Return(SubnetId)

	creating := branchENIWithVlanTag(Branch1Id, VlanId1)
	creating.Status = awsEc2Types.NetworkInterfaceStatusAvailable
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		Return([]*awsEc2Types.NetworkInterface{creating}, nil)

	reclaimedBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))
	trunkENI.reclaimOrphansOnAssociateFailure()

	assert.Empty(t, trunkENI.deleteQueue)
	assert.False(t, trunkENI.usedVlanIds[VlanId1])
	assert.Equal(t, float64(0),
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))-reclaimedBefore)
}

// TestTrunkENI_DeleteCooledDownENIs_SkipsOwnedENI verifies the pre-delete
// ownership recheck: an ENI that is (or became) pod-owned is dropped from the
// delete queue without an EC2 delete call.
func TestTrunkENI_DeleteCooledDownENIs_SkipsOwnedENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)).WithName("cooldown"))
	trunkENI.trunkENIId = trunkId
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{{ID: Branch1Id, VlanID: VlanId1}}
	trunkENI.deleteQueue = append(trunkENI.deleteQueue, &ENIDetails{
		ID: Branch1Id, VlanID: VlanId1, deletionTimeStamp: time.Now().Add(-time.Hour)})

	// No DeleteNetworkInterface expectation: any delete call fails the mock.
	trunkENI.DeleteCooledDownENIs()

	assert.Empty(t, trunkENI.deleteQueue)
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 1)
}

// TestTrunkENI_DeleteCooledDownENIs_SerializesOrphanReclaim verifies reactive
// reclaim cannot requeue an ENI after the delete worker pops it but before the
// EC2 delete completes.
func TestTrunkENI_DeleteCooledDownENIs_SerializesOrphanReclaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.deleteQueue = []*ENIDetails{{ID: Branch1Id, VlanID: VlanId1}}

	deleteStarted := make(chan struct{})
	allowDelete := make(chan struct{})
	deleteDone := make(chan struct{})
	reclaimDone := make(chan struct{})
	mockHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).DoAndReturn(func(*string) error {
		close(deleteStarted)
		<-allowDelete
		return nil
	})
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		Return([]*awsEc2Types.NetworkInterface{}, nil)

	go func() {
		trunkENI.DeleteCooledDownENIs()
		close(deleteDone)
	}()

	<-deleteStarted
	go func() {
		trunkENI.reclaimOrphans()
		close(reclaimDone)
	}()

	select {
	case <-reclaimDone:
		t.Fatal("orphan reclaim must wait for the active delete")
	default:
	}

	close(allowDelete)
	<-deleteDone
	<-reclaimDone

	assert.Empty(t, trunkENI.deleteQueue)
	assert.False(t, trunkENI.usedVlanIds[VlanId1])
}

func TestTrunkENI_reclaimOrphansOnAssociateFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		Return([]*awsEc2Types.NetworkInterface{branchENIWithVlanTag(Branch1Id, VlanId1)}, nil)

	triggeredBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))
	reclaimedBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))

	trunkENI.reclaimOrphansOnAssociateFailure()

	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI))
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.isOrphanCheckCompleted())
	assertAllQueuedENIsStamped(t, trunkENI)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))-triggeredBefore)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))-reclaimedBefore)

	// A successful check is not repeated until this process restores the trunk
	// again. No second Describe expectation is registered.
	trunkENI.reclaimOrphansOnAssociateFailure()
}

func TestTrunkENI_reclaimOrphans_DescribeErrorCanRetry(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	gomock.InOrder(
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, MockError),
		mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil),
	)

	triggeredBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))
	errorsBefore := testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("reclaim_orphans_describe"))

	trunkENI.reclaimOrphansOnAssociateFailure()

	assert.Empty(t, trunkENI.deleteQueue)
	assert.False(t, trunkENI.isOrphanCheckCompleted())
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))-triggeredBefore)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("reclaim_orphans_describe"))-errorsBefore)

	trunkENI.reclaimOrphansOnAssociateFailure()
	assert.True(t, trunkENI.isOrphanCheckCompleted())
}

func TestTrunkENI_reclaimOrphans_ConcurrentCallsShareDescribe(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().SubnetID().Return(SubnetId)

	describeStarted := make(chan struct{})
	releaseDescribe := make(chan struct{})
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		DoAndReturn(func(*string, *string) ([]*awsEc2Types.NetworkInterface, error) {
			close(describeStarted)
			<-releaseDescribe
			return nil, nil
		})

	const callers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			trunkENI.reclaimOrphansOnAssociateFailure()
		}()
	}

	close(start)
	<-describeStarted
	time.Sleep(50 * time.Millisecond)
	close(releaseDescribe)
	wg.Wait()
}

// TestTrunkENI_InitFromNodeNetworkState verifies that the ledger is rebuilt from
// the observed trunk ID and pod annotations without calling EC2.
func TestTrunkENI_InitFromNodeNetworkState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod1})
	assert.NoError(t, err)

	assert.Equal(t, trunkId, trunkENI.trunkENIId)
	// The pod annotation carries two branch ENIs on vlan 1 and 2.
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
	assert.True(t, trunkENI.usedVlanIds[1])
	assert.True(t, trunkENI.usedVlanIds[2])
	assert.False(t, trunkENI.isOrphanCheckCompleted())
}

// podWithBranches builds a pod whose pod-eni annotation carries the given branch
// ENIs, for exercising the InitFromNodeNetworkState ledger validation.
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

// TestTrunkENI_InitFromNodeNetworkState_DuplicateBranchENI tests that a branch ENI id
// claimed by two pods is rejected without committing any state, so the caller
// falls back to the authoritative EC2 path.
func TestTrunkENI_InitFromNodeNetworkState_DuplicateBranchENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	podA := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-dup", VlanID: 1}})
	podB := podWithBranches("uid-b", []*ENIDetails{{ID: "eni-dup", VlanID: 3}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{podA, podB})
	assert.ErrorIs(t, err, ErrInvalidRestoredLedger)
	assert.Empty(t, trunkENI.uidToBranchENIMap)
	assert.Empty(t, trunkENI.trunkENIId)
}

// TestTrunkENI_InitFromNodeNetworkState_ConflictingVlan tests that two pods claiming the
// same VLAN id is rejected as a structurally invalid ledger.
func TestTrunkENI_InitFromNodeNetworkState_ConflictingVlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	podA := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-1", VlanID: 7}})
	podB := podWithBranches("uid-b", []*ENIDetails{{ID: "eni-2", VlanID: 7}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{podA, podB})
	assert.ErrorIs(t, err, ErrInvalidRestoredLedger)
	assert.Empty(t, trunkENI.uidToBranchENIMap)
}

// TestTrunkENI_InitFromNodeNetworkState_OutOfRangeVlan tests that an out-of-range VLAN in
// a pod annotation (corrupted/tampered) is rejected as a structurally invalid
// ledger rather than reaching the fixed-size VLAN ledger.
func TestTrunkENI_InitFromNodeNetworkState_OutOfRangeVlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-1", VlanID: MaxAllocatableVlanIds}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{pod})
	assert.ErrorIs(t, err, ErrInvalidRestoredLedger)
}

// TestTrunkENI_InitFromNodeNetworkState_VlanZeroIsInvalid tests that VLAN 0 in a pod
// annotation is rejected, matching the allocation path: NewTrunkENI pre-marks
// index 0 used and the delete path treats 0 as "no vlan", so 0 is never an
// assigned id and its presence means the annotation is corrupt.
func TestTrunkENI_InitFromNodeNetworkState_VlanZeroIsInvalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	pod := podWithBranches("uid-a", []*ENIDetails{{ID: "eni-vlan0", VlanID: 0}})

	err := trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{pod})
	assert.ErrorIs(t, err, ErrInvalidRestoredLedger)
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

	// Both the reserved and the marked id are skipped.
	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 2, id)
}

// TestTrunkENI_vlanId_OutOfRangeGuard tests out-of-range vlan ids (which can only
// come from corrupted external data, e.g. a tampered pod annotation) are refused
// with an error count instead of panicking the controller.
func TestTrunkENI_vlanId_OutOfRangeGuard(t *testing.T) {
	trunkENI := getMockTrunk()

	before := testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("vlan_id_out_of_range"))
	assert.NotPanics(t, func() {
		trunkENI.markVlanAssigned(-1)
		trunkENI.markVlanAssigned(MaxAllocatableVlanIds)
		trunkENI.freeVlanId(-1)
		trunkENI.freeVlanId(MaxAllocatableVlanIds)
		// 0 is reserved, so it is outside the assignable range in both directions.
		trunkENI.markVlanAssigned(0)
		trunkENI.freeVlanId(0)
	})
	after := testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("vlan_id_out_of_range"))
	assert.Equal(t, before+6, after)
}

// TestTrunkENI_freeVlanId_CannotReleaseReservedZero tests that the reserved slot 0
// stays reserved: NewTrunkENI marks it used, and a stray free(0) must not put it
// back in circulation for assignVlanId to hand out.
func TestTrunkENI_freeVlanId_CannotReleaseReservedZero(t *testing.T) {
	trunkENI := NewTrunkENI(zap.New(), FakeInstance, nil).(*trunkENI)
	assert.True(t, trunkENI.usedVlanIds[0], "0 is reserved at construction")

	trunkENI.freeVlanId(0)

	assert.True(t, trunkENI.usedVlanIds[0], "free(0) must not release the reserved slot")
	// The first id handed out is still 1, never the reserved 0.
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

// TestTrunkENI_decodeBranchInterfacesUsedByPod tests that branch interface are returned if present in pod annotation
func TestTrunkENI_decodeBranchInterfacesUsedByPod(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs, usable := trunkENI.decodeBranchInterfacesUsedByPod(MockPod1)

	assert.True(t, usable)
	assert.Equal(t, 2, len(branchENIs))
	assert.Equal(t, EniDetails1, branchENIs[0])
	assert.Equal(t, EniDetails2, branchENIs[1])
}

// TestTrunkENI_decodeBranchInterfacesUsedByPod_MissingAnnotation tests that an
// absent annotation is usable with no entries: that pod simply owns nothing.
func TestTrunkENI_decodeBranchInterfacesUsedByPod_MissingAnnotation(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs, usable := trunkENI.decodeBranchInterfacesUsedByPod(MockPod2)

	assert.True(t, usable)
	assert.Equal(t, 0, len(branchENIs))
}

// TestTrunkENI_decodeBranchInterfacesUsedByPod_Unusable tests that an annotation
// which exists but cannot be trusted is reported as unusable, so callers do not
// mistake it for "this pod owns nothing".
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
			if !tt.wantErr {
				assert.True(t, f.trunkENI.isOrphanCheckCompleted(),
					"EC2 initialization already verified the trunk")
				// No Describe expectation is registered: EC2-initialized trunks
				// must ignore later duplicate-triggered check requests.
				f.trunkENI.reclaimOrphansOnAssociateFailure()
			}
			if tt.asserts != nil {
				tt.asserts(&f)
			}
		})
	}
}

// TestTrunkENI_CreateAndAssociateBranchENIs verifies successful creation,
// association, and ownership tracking.
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

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2, nil)
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

	assert.NoError(t, err)
	// VLan ID are marked as used
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	// The returned content is as expected
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

// TestTrunkENI_CreateAndAssociateBranchENIs_LedgerBeforeAnnotation verifies the
// local ledger owns the ENI before the pod annotation callback runs.
func TestTrunkENI_CreateAndAssociateBranchENIs_LedgerBeforeAnnotation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(1)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(1)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(1)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).
		Return(mockAssociationOutput1, nil)

	commitCalled := false
	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1,
		func(enis []*ENIDetails) error {
			commitCalled = true
			trunkENI.lock.RLock()
			defer trunkENI.lock.RUnlock()
			ledgerENIs, inLedger := trunkENI.uidToBranchENIMap[PodUID2]
			assert.True(t, inLedger, "ledger ownership must precede pod annotation")
			assert.Equal(t, enis, ledgerENIs)
			return nil
		})

	assert.NoError(t, err)
	assert.True(t, commitCalled)
	assert.Equal(t, []*ENIDetails{EniDetails1}, eniDetails)
	assert.Equal(t, []*ENIDetails{EniDetails1}, trunkENI.uidToBranchENIMap[PodUID2])
}

func TestTrunkENI_ReactiveOrphanCheckWaitsForAllocationCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)
	mockHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).
		Return(mockAssociationOutput1, nil)

	commitStarted := make(chan struct{})
	allowCommit := make(chan struct{})
	allocationDone := make(chan error)
	go func() {
		_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1,
			func([]*ENIDetails) error {
				close(commitStarted)
				<-allowCommit
				return nil
			})
		allocationDone <- err
	}()
	<-commitStarted

	describeStarted := make(chan struct{})
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		DoAndReturn(func(*string, *string) ([]*awsEc2Types.NetworkInterface, error) {
			close(describeStarted)
			return []*awsEc2Types.NetworkInterface{
				branchENIWithVlanTag(Branch1Id, VlanId1),
			}, nil
		})
	reclaimDone := make(chan struct{})
	go func() {
		trunkENI.reclaimOrphansOnAssociateFailure()
		close(reclaimDone)
	}()

	select {
	case <-describeStarted:
		t.Fatal("orphan check must wait until annotation commit releases the allocation reader")
	default:
	}

	close(allowCommit)
	assert.NoError(t, <-allocationDone)
	<-describeStarted
	<-reclaimDone

	assert.Equal(t, []*ENIDetails{EniDetails1}, trunkENI.uidToBranchENIMap[PodUID2])
	assert.Empty(t, trunkENI.deleteQueue)
}

// TestTrunkENI_CreateAndAssociateBranchENIs_AnnotationFailureHandoff verifies an
// annotation failure observes ledger ownership, then atomically moves the ENI
// from the ledger to deleteQueue.
func TestTrunkENI_CreateAndAssociateBranchENIs_AnnotationFailureHandoff(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(1)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(1)
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).Times(1)
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		append(vlan1Tag, trunkENI.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).
		Return(mockAssociationOutput1, nil)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1,
		func(enis []*ENIDetails) error {
			trunkENI.lock.RLock()
			defer trunkENI.lock.RUnlock()
			ledgerENIs, inLedger := trunkENI.uidToBranchENIMap[PodUID2]
			assert.True(t, inLedger)
			assert.Equal(t, enis, ledgerENIs)
			return MockError
		})

	assert.ErrorIs(t, err, MockError)
	assert.NotContains(t, trunkENI.uidToBranchENIMap, PodUID2)
	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI))
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

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, []string{}, 2, nil)
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

	assert.NoError(t, err)
	// VLan ID are marked as used
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	// The returned content is as expected
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorAssociate verifies that an
// association failure queues every ENI created by the request.
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
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2, nil)
	assert.Error(t, err)
	// Reactive reclaim does not turn the association error into a capacity error.
	assert.NotErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
	assert.Equal(t, []string{EniDetails1.ID, ENIDetailsMissingAssociationID.ID}, queuedENIIDs(trunkENI))
	assert.True(t, trunkENI.deleteQueue[0].deletionTimeStamp.IsZero())
	assert.True(t, trunkENI.deleteQueue[1].deletionTimeStamp.IsZero())
}

func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorAssociate_NoSelfDoubleEnqueue(t *testing.T) {
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
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		DoAndReturn(func(*string, *string) ([]*awsEc2Types.NetworkInterface, error) {
			assert.Equal(t, []string{EniDetails1.ID, ENIDetailsMissingAssociationID.ID},
				queuedENIIDs(trunkENI), "failed allocation must enter deleteQueue before Describe")
			return []*awsEc2Types.NetworkInterface{
				branchENIWithVlanTag(Branch1Id, VlanId1),
				branchENIWithVlanTag(Branch2Id, VlanId2),
			}, nil
		})

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2, nil)

	assert.Error(t, err)
	assert.Equal(t, []string{EniDetails1.ID, ENIDetailsMissingAssociationID.ID}, queuedENIIDs(trunkENI))
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate verifies that a create
// failure queues ENIs completed earlier in the request.
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

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2, nil)
	assert.Error(t, MockError, err)
	assert.Equal(t, []string{EniDetails1.ID}, queuedENIIDs(trunkENI))
	assert.True(t, trunkENI.deleteQueue[0].deletionTimeStamp.IsZero())
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

// branchENIWithVlanTag builds an EC2 branch ENI carrying the given VLAN tag.
func branchENIWithVlanTag(id string, vlanID int) *awsEc2Types.NetworkInterface {
	return &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: aws.String(id),
		// Associated branch ENIs report in-use; reclaim only considers these.
		Status: awsEc2Types.NetworkInterfaceStatusInUse,
		TagSet: []awsEc2Types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String(strconv.Itoa(vlanID))}},
	}
}

// expectInitTrunkExistingTrunk sets the mocks for an InitTrunk against an existing
// trunk that EC2 reports the given branch ENIs for.
func expectInitTrunkExistingTrunk(mockHelper *mock_api.MockEC2APIHelper, mockInstance *mock_ec2.MockEC2Instance,
	branches []*awsEc2Types.NetworkInterface,
) {
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{}).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	mockHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branches, nil)
}

// TestTrunkENI_InitTrunk_RejectsDuplicateVlanFromAnnotation is the authoritative-
// fallback guarantee: this path runs after restored state was rejected, so it
// must not rebuild a ledger with the shape that caused the rejection. Two ENIs whose
// EC2 VLAN tags collide cannot both own that VLAN, so the second is left
// unattributed and reclaimed instead of being recorded on top of the first.
func TestTrunkENI_InitTrunk_RejectsDuplicateVlanFromAnnotation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	// The pod claims two ENIs; EC2 reports both associated on the same VLAN.
	pod := podWithBranches(PodUID, []*ENIDetails{{ID: Branch1Id, VlanID: 1}, {ID: Branch2Id, VlanID: 1}})
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, 1),
		branchENIWithVlanTag(Branch2Id, 1),
	})

	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	// Exactly one owner for VLAN 1, and the loser is queued for reclaim rather than
	// silently sharing the slot.
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 1)
	assert.Equal(t, Branch1Id, trunkENI.uidToBranchENIMap[PodUID][0].ID)
	assert.True(t, trunkENI.usedVlanIds[1])
	assert.Equal(t, []string{Branch2Id}, queuedENIIDs(trunkENI))
}

// TestTrunkENI_InitTrunk_RejectsOutOfRangeVlan tests the other rejected shape: an
// out-of-range VLAN must not be recorded, since markVlanAssigned refuses it and the
// ledger would then hold an ENI on a slot it never reserved.
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

// TestTrunkENI_InitTrunk_AnnotationWinsOverVlanTagDrift tests which copy of the
// VLAN the ledger tracks when they disagree. Both the annotation and the ENI's
// VLAN tag are written by this controller, but the annotation is written after the
// association succeeds and is what the node CNI programs, so the ledger follows it
// and the disagreement is recorded as drift. Tracking the tag instead would make a
// later freeVlanId release a slot the CNI never used.
func TestTrunkENI_InitTrunk_AnnotationWinsOverVlanTagDrift(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	// Annotation says VLAN 1, the ENI tag says VLAN 7.
	pod := podWithBranches(PodUID, []*ENIDetails{{ID: Branch1Id, VlanID: 1}})
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, 7),
	})
	before := testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("branch_eni_vlan_tag_drift"))

	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 1)
	assert.Equal(t, 1, trunkENI.uidToBranchENIMap[PodUID][0].VlanID)
	assert.True(t, trunkENI.usedVlanIds[1], "the vlan the CNI programmed is the one reserved")
	assert.False(t, trunkENI.usedVlanIds[7])
	// The disagreement is observable rather than silently resolved.
	assert.Equal(t, 1.0, testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("branch_eni_vlan_tag_drift"))-before)
}

// TestTrunkENI_InitFromNodeNetworkState_RejectsUnusableAnnotation tests that an annotation
// which cannot be decoded, or which decodes to an entry with no ENI id, is not
// silently read as "this pod owns nothing". The pod may own an ENI we cannot see,
// which restoration would record as unowned, so the node takes the EC2 fallback.
func TestTrunkENI_InitFromNodeNetworkState_RejectsUnusableAnnotation(t *testing.T) {
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
			assert.ErrorIs(t, err, ErrInvalidRestoredLedger)
			assert.Empty(t, trunkENI.uidToBranchENIMap)
		})
	}
}

// TestTrunkENI_DeleteCooledDownENIs_FailedAllocationDeletesImmediately verifies
// a failed allocation is cleanup work, not a warm-pool or recovery trigger.
func TestTrunkENI_DeleteCooledDownENIs_FailedAllocationDeletesImmediately(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).
		Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, trunkENI.log)

	// An ENI that has already cooled down and is due for deletion.
	cooled := &ENIDetails{ID: "eni-already-cooled", VlanID: 5, deletionTimeStamp: time.Now().Add(-time.Hour)}
	trunkENI.usedVlanIds[5] = true
	trunkENI.deleteQueue = []*ENIDetails{cooled}

	// A pod allocation fails, producing a freshly stamped ENI.
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()
	mockHelper.EXPECT().CreateNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(BranchInterface1, nil)
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, gomock.Any()).Return(nil, MockDuplicateVlanError)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.Error(t, err)

	// The failed allocation is placed at the front with no cooldown, then the
	// already-cooled item is deleted in the same pass.
	mockHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(nil)
	mockHelper.EXPECT().DeleteNetworkInterface(&cooled.ID).Return(nil)
	trunkENI.DeleteCooledDownENIs()

	assert.Empty(t, queuedENIIDs(trunkENI))
}

// TestTrunkENI_InitTrunk_UnusableAnnotationSkipsReclaim tests the destructive case
// on the EC2 path. If a pod's annotation cannot be read, its ENIs cannot be
// attributed, and an unattributed ENI otherwise looks like it belongs to no pod -
// so the reclaim would delete a running pod's interface. Reclaim is skipped for
// that pass instead: leaking until the next init is recoverable, deleting a live
// pod's ENI is not.
func TestTrunkENI_InitTrunk_UnusableAnnotationSkipsReclaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	// The pod owns Branch1 in EC2, but its annotation is corrupt so we cannot tell.
	pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{
		UID: MockPodUID1, Name: MockPodName1, Namespace: MockPodNamespace1,
		Annotations: map[string]string{config.ResourceNamePodENI: "{not-json"},
	}}
	expectInitTrunkExistingTrunk(mockHelper, mockInstance, []*awsEc2Types.NetworkInterface{
		branchENIWithVlanTag(Branch1Id, 1),
	})

	assert.NoError(t, trunkENI.InitTrunk(mockInstance, []v1.Pod{pod}))

	// The ENI is neither attributed nor queued for deletion.
	assert.Empty(t, trunkENI.uidToBranchENIMap[PodUID])
	assert.Empty(t, queuedENIIDs(trunkENI), "an ENI we merely failed to attribute must not be reaped")
	// Its VLAN is still occupied in EC2, so the ledger must not hand it out again.
	assert.True(t, trunkENI.usedVlanIds[1], "an unreclaimed ENI's vlan must stay reserved")
	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.NotEqual(t, 1, id)
}
