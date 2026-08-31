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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
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
		inFlightENIs:      map[string]struct{}{},
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
	assertAllQueuedENIsStamped(t, trunkENI)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))-triggeredBefore)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))-reclaimedBefore)
}

func TestTrunkENI_reclaimOrphans_SkipsInFlightENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.markENIInFlight(Branch2Id)
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		Return([]*awsEc2Types.NetworkInterface{
			branchENIWithVlanTag(Branch1Id, VlanId1),
			branchENIWithVlanTag(Branch2Id, VlanId2),
		}, nil)

	triggeredBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))
	reclaimedBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))
	skippedBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("skipped_in_flight"))

	trunkENI.reclaimOrphansOnAssociateFailure()

	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI))
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.False(t, trunkENI.usedVlanIds[VlanId2])
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))-triggeredBefore)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("reclaimed"))-reclaimedBefore)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("skipped_in_flight"))-skippedBefore)
}

func TestTrunkENI_reclaimOrphans_DescribeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, MockError)

	triggeredBefore := testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))
	errorsBefore := testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("reclaim_orphans_describe"))

	trunkENI.reclaimOrphansOnAssociateFailure()

	assert.Empty(t, trunkENI.deleteQueue)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(branchENIOrphanReclaimCount.WithLabelValues("triggered"))-triggeredBefore)
	assert.Equal(t, 1.0,
		testutil.ToFloat64(trunkENIOperationsErrCount.WithLabelValues("reclaim_orphans_describe"))-errorsBefore)
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

func TestTrunkENI_reclaimOrphans_ConcurrentInFlight(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	const inFlightID = "eni-in-flight"
	const orphanID = "eni-orphan"
	trunkENI.markENIInFlight(inFlightID)

	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		Return([]*awsEc2Types.NetworkInterface{
			branchENIWithVlanTag(inFlightID, 5),
			branchENIWithVlanTag(orphanID, 6),
		}, nil).AnyTimes()

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(2)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := fmt.Sprintf("eni-%d-%d", worker, i)
				trunkENI.markENIInFlight(id)
				trunkENI.clearENIsInFlight([]string{id})
			}
		}(worker)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				trunkENI.reclaimOrphansOnAssociateFailure()
			}
		}()
	}
	wg.Wait()

	assert.NotContains(t, queuedENIIDs(trunkENI), inFlightID)
	assert.Contains(t, queuedENIIDs(trunkENI), orphanID)
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
}

// TestTrunkENI_InitFromNodeNetworkState_SetsRestored verifies that restoration
// enables runtime resync.
func TestTrunkENI_InitFromNodeNetworkState_SetsRestored(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())

	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod1}))
	assert.True(t, trunkENI.IsRestoredFromNodeNetworkState())
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
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
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
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
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
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
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
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
	assert.Empty(t, trunkENI.uidToBranchENIMap)
}

// expectResyncEC2 sets the mock expectations for one authoritative InitTrunk
// rebuild of an existing trunk with its two branches. The describe calls are
// Times(1) so a resync that runs more than once (a singleflight or termination
// bug) fails the test.
func expectResyncEC2(mockHelper *mock_api.MockEC2APIHelper, mockInstance *mock_ec2.MockEC2Instance) {
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{}).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil).Times(1)
	mockHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil).Times(1)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil).Times(1)
}

// podLister returns a pod-lister closure for ResyncTrunkLedgerFromEC2.
func podLister(pods ...v1.Pod) func() ([]v1.Pod, error) {
	return func() ([]v1.Pod, error) { return pods, nil }
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2 tests the ledger is rebuilt authoritatively
// from EC2 (reusing InitTrunk), the ledger becomes authoritative, and resync is
// recorded with its trigger.
func TestTrunkENI_ResyncTrunkLedgerFromEC2(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	// Start from a restored ledger that (wrongly) has no branches.
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod2}))
	assert.True(t, trunkENI.IsRestoredFromNodeNetworkState())
	assert.Empty(t, trunkENI.uidToBranchENIMap)

	expectResyncEC2(mockHelper, mockInstance)
	before := testutil.ToFloat64(trunkResyncCount.WithLabelValues("capacity"))

	assert.NoError(t, trunkENI.ResyncTrunkLedgerFromEC2(podLister(*MockPod1, *MockPod2), "capacity"))

	// State is authoritative: the two branches from EC2 are now tracked and the
	// trunk is no longer restored (so it cannot resync again).
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
	assert.Equal(t, trunkId, trunkENI.trunkENIId)
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	assert.Equal(t, 1.0, testutil.ToFloat64(trunkResyncCount.WithLabelValues("capacity"))-before)
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_PodsListedAfterQuiesce proves the pod
// pod list is read inside the resync after the recovery gate has quiesced
// mutations.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_PodsListedAfterQuiesce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod2}))
	expectResyncEC2(mockHelper, mockInstance)

	listed := false
	listPods := func() ([]v1.Pod, error) {
		// By the time the ledger is read, the gate is already held exclusively.
		assert.True(t, trunkENI.IsRestoredFromNodeNetworkState())
		listed = true
		return []v1.Pod{*MockPod1, *MockPod2}, nil
	}

	assert.NoError(t, trunkENI.ResyncTrunkLedgerFromEC2(listPods, "capacity"))
	assert.True(t, listed, "resync must read the pod list itself")
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_ListPodsError tests a pod-list failure
// aborts the resync without touching the live ledger, leaving it restored so a
// later contradiction can retry.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_ListPodsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod1}))

	err := trunkENI.ResyncTrunkLedgerFromEC2(func() ([]v1.Pod, error) { return nil, MockError }, "capacity")
	assert.Error(t, err)
	assert.True(t, trunkENI.IsRestoredFromNodeNetworkState())
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_TrunkMissing tests the documented recovery
// boundary: if the rebuild does not land on the same trunk (e.g. the persisted
// trunk is gone and InitTrunk would create a new one), the resync fails safely
// without replacing the live ledger instead of pretending to recover.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_TrunkMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod1}))

	// EC2 reports no trunk on the instance. Restoration does not load the device
	// indexes needed to create one.
	newTrunkID := "eni-brand-new-trunk"
	freeIndex := int32(2)
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(SecurityGroups).AnyTimes()
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(freeIndex, nil).AnyTimes()
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]awsEc2Types.InstanceNetworkInterface{}, nil)
	mockHelper.EXPECT().CreateAndAttachNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&awsEc2Types.NetworkInterface{NetworkInterfaceId: &newTrunkID}, nil)

	err := trunkENI.ResyncTrunkLedgerFromEC2(podLister(*MockPod1), "capacity")
	assert.ErrorIs(t, err, ErrTrunkChangedDuringResync)
	// Live state remains untouched and eligible for another resync.
	assert.True(t, trunkENI.IsRestoredFromNodeNetworkState())
	assert.Equal(t, trunkId, trunkENI.trunkENIId)
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_TerminatesAfterSuccess tests a second
// resync attempt after a successful one is a no-op (no EC2 call), so a repeated
// contradiction cannot loop.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_TerminatesAfterSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod2}))

	expectResyncEC2(mockHelper, mockInstance) // Times(1): the second resync must not call EC2.
	assert.NoError(t, trunkENI.ResyncTrunkLedgerFromEC2(podLister(*MockPod1), "capacity"))
	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())

	// Second attempt: trunk is authoritative now, so this is a no-op.
	assert.NoError(t, trunkENI.ResyncTrunkLedgerFromEC2(podLister(*MockPod1), "capacity"))
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_Concurrent stress-tests the singleflight
// guard (run with -race): many goroutines detect the same contradiction at once,
// but exactly one authoritative EC2 rebuild happens (the describe mocks are
// Times(1)) and exactly one resync is recorded.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_Concurrent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod2}))

	expectResyncEC2(mockHelper, mockInstance)
	before := testutil.ToFloat64(trunkResyncCount.WithLabelValues("capacity"))

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = trunkENI.ResyncTrunkLedgerFromEC2(podLister(*MockPod1, *MockPod2), "capacity")
		}()
	}
	wg.Wait()

	assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
	// Exactly one resync ran despite 16 concurrent detections.
	assert.Equal(t, 1.0, testutil.ToFloat64(trunkResyncCount.WithLabelValues("capacity"))-before)
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_ConcurrentWriterNotLost is the recovery
// gate's real test (run with -race): a concurrent allocation runs while a resync
// rebuilds and swaps the ledger. The gate must order them, so the new pod's ENI
// either lands before pods are read for recovery (and is therefore rebuilt
// from EC2) or is applied after the swap - it must never be silently dropped by
// the swap.
//
// The rebuild is driven off a channel so the resync is provably in flight while
// the writer runs.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_ConcurrentWriterNotLost(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod2}))

	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{}).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()
	mockHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil).AnyTimes()
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(branchInterfaces, nil).AnyTimes()

	// Block the EC2 rebuild until the test says so, so the writer provably races it.
	rebuildStarted := make(chan struct{})
	releaseRebuild := make(chan struct{})
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).
		DoAndReturn(func(_ *string) ([]awsEc2Types.InstanceNetworkInterface, error) {
			close(rebuildStarted)
			<-releaseRebuild
			return instanceNwInterfaces, nil
		})

	// The new pod's allocation: created in EC2 and associated successfully.
	newENIID := "eni-concurrent-writer"
	newENI := &awsEc2Types.NetworkInterface{
		MacAddress: aws.String("FF:FF:FF:FF:FF:AA"), NetworkInterfaceId: &newENIID,
		PrivateIpAddress: aws.String("192.168.0.99"), Ipv6Address: aws.String("2600::99"),
	}
	mockHelper.EXPECT().CreateNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(newENI, nil).AnyTimes()
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newENIID, gomock.Any()).
		Return(mockAssociationOutput1, nil).AnyTimes()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = trunkENI.ResyncTrunkLedgerFromEC2(podLister(*MockPod1, *MockPod2), "capacity")
	}()

	<-rebuildStarted // resync now holds the exclusive gate and is mid-rebuild

	writerDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
		writerDone <- err
	}()

	// The gate must hold the writer off while the rebuild is in flight.
	select {
	case <-writerDone:
		t.Fatal("allocation ran during the ledger rebuild; the recovery gate did not quiesce mutations")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseRebuild)
	assert.NoError(t, <-writerDone)
	wg.Wait()

	// The writer ran after the swap, so its ENI is in the authoritative ledger.
	trunkENI.lock.RLock()
	defer trunkENI.lock.RUnlock()
	assert.False(t, trunkENI.restoredFromNodeNetworkState)
	writerENIs, ok := trunkENI.uidToBranchENIMap[PodUID2]
	assert.True(t, ok, "the concurrent writer's pod must still own its ENI after the swap")
	assert.Len(t, writerENIs, 1)
	assert.Equal(t, newENIID, writerENIs[0].ID)
	// And the rebuilt state is present too, so nothing was lost either way.
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID], 2)
}

// TestTrunkENI_ResyncTrunkLedgerFromEC2_WaitsForOwnershipCommit is the ownership
// half of the gate contract (run with -race). Between association and the pod
// annotation the ENI exists in EC2 but no pod claims it, and InitTrunk classifies
// exactly that as unowned and queues it for deletion. This test blocks the
// ownership commit (the annotation) after association and starts a resync
// concurrently: the resync must not read pods until the annotation
// transaction has finished, so the new ENI can never be reaped.
func TestTrunkENI_ResyncTrunkLedgerFromEC2_WaitsForOwnershipCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	assert.NoError(t, trunkENI.InitFromNodeNetworkState(trunkId, []v1.Pod{*MockPod2}))

	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().GetCustomNetworkingSpec().Return("", []string{}).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()
	mockHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil).AnyTimes()
	mockHelper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(awsEc2Types.AttachmentStatusAttached)).Return(nil).AnyTimes()

	newENIID := "eni-awaiting-ownership"
	newENI := &awsEc2Types.NetworkInterface{
		MacAddress: aws.String("FF:FF:FF:FF:FF:BB"), NetworkInterfaceId: &newENIID,
		PrivateIpAddress: aws.String("192.168.0.98"), Ipv6Address: aws.String("2600::98"),
	}
	mockHelper.EXPECT().CreateNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(newENI, nil).AnyTimes()
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &newENIID, gomock.Any()).
		Return(mockAssociationOutput1, nil).AnyTimes()

	// EC2 already reports the new attachment, exactly as it would in the window
	// before the pod annotation lands.
	ec2View := append([]*awsEc2Types.NetworkInterface{}, branchInterfaces...)
	ec2View = append(ec2View, &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: &newENIID,
		TagSet:             []awsEc2Types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String("3")}},
	})
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(ec2View, nil).AnyTimes()

	associated := make(chan struct{})
	releaseCommit := make(chan struct{})
	commitDone := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1,
			func(enis []*ENIDetails) error {
				// Association has happened; ownership is not committed yet.
				close(associated)
				<-releaseCommit
				close(commitDone)
				return nil
			})
		assert.NoError(t, err)
	}()

	<-associated

	// The resync must block here: the annotation transaction is still open.
	resyncStarted := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(resyncStarted)
		_ = trunkENI.ResyncTrunkLedgerFromEC2(func() ([]v1.Pod, error) {
			// The gate ensures ownership is committed before pods are read.
			select {
			case <-commitDone:
			default:
				t.Error("resync read pods before the ownership commit finished")
			}
			// The annotation is now durable, so the rebuild sees the pod owning it.
			owner := MockPod2.DeepCopy()
			raw, _ := json.Marshal([]*ENIDetails{{ID: newENIID, VlanID: 3}})
			owner.Annotations = map[string]string{config.ResourceNamePodENI: string(raw)}
			return []v1.Pod{*MockPod1, *owner}, nil
		}, "capacity")
	}()

	<-resyncStarted
	time.Sleep(50 * time.Millisecond) // give the resync a chance to (wrongly) proceed
	close(releaseCommit)
	wg.Wait()

	// The new ENI kept its owner and was never queued for deletion.
	trunkENI.lock.RLock()
	defer trunkENI.lock.RUnlock()
	assert.Len(t, trunkENI.uidToBranchENIMap[PodUID2], 1)
	assert.Equal(t, newENIID, trunkENI.uidToBranchENIMap[PodUID2][0].ID)
	for _, queued := range trunkENI.deleteQueue {
		assert.NotEqual(t, newENIID, queued.ID, "an ENI whose owner was committed must not be reaped")
	}
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
	assert.Empty(t, trunkENI.inFlightENIs)
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
	assert.Empty(t, trunkENI.inFlightENIs)
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
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, MockError),
	)
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2, nil)
	assert.Error(t, err)
	// Reactive reclaim does not turn the association error into a capacity error.
	assert.NotErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
	assert.Equal(t, []string{EniDetails1.ID, ENIDetailsMissingAssociationID.ID}, queuedENIIDs(trunkENI))
	// Stamped so they cool down and keep their capacity slots accounted for.
	assertAllQueuedENIsStamped(t, trunkENI)
	assert.Empty(t, trunkENI.inFlightENIs)
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
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, MockError),
	)
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).
		Return([]*awsEc2Types.NetworkInterface{
			branchENIWithVlanTag(Branch1Id, VlanId1),
			branchENIWithVlanTag(Branch2Id, VlanId2),
		}, nil)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2, nil)

	assert.Error(t, err)
	assert.Equal(t, []string{EniDetails1.ID, ENIDetailsMissingAssociationID.ID}, queuedENIIDs(trunkENI))
	assert.Empty(t, trunkENI.inFlightENIs)
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
	assertAllQueuedENIsStamped(t, trunkENI)
	assert.Empty(t, trunkENI.inFlightENIs)
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

// fillLedgerToLimitMinusOne seeds the restored ledger with limit-1 owned branch
// ENIs, matching restored state when EC2 actually holds one
// more attachment that no pod annotation mentions (a hidden orphan).
func fillLedgerToLimitMinusOne(trunkENI *trunkENI) int {
	limit := vpc.Limits[InstanceType].BranchInterface
	for i := 0; i < limit-1; i++ {
		uid := fmt.Sprintf("uid-filler-%d", i)
		trunkENI.uidToBranchENIMap[uid] = []*ENIDetails{{ID: fmt.Sprintf("eni-filler-%d", i), VlanID: i + 1}}
		trunkENI.usedVlanIds[i+1] = true
	}
	return limit
}

// TestTrunkENI_HiddenOrphan_ReachesCapacityError proves the capacity-only recovery
// trigger covers the failure mode state restoration cannot see: EC2 holds an
// attachment that no pod annotation claims, so the reconstructed ledger undercounts.
//
//	local restored ledger = limit - 1
//	EC2 actual            = limit          <- hidden orphan
//
// The first allocation believes capacity exists, association fails, and the failed
// ENI lands in the delete queue. The retry then sees used+deleteQueue == limit and
// returns ErrCurrentlyAtMaxCapacity, which is what arms the resync.
func TestTrunkENI_HiddenOrphan_ReachesCapacityError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.restoredFromNodeNetworkState = true
	limit := fillLedgerToLimitMinusOne(trunkENI)

	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()
	mockHelper.EXPECT().CreateNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(BranchInterface1, nil)
	// EC2 is actually full because of the orphan, so the association is rejected.
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, gomock.Any()).Return(nil, MockError)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil)

	// First attempt: the local ledger says there is room.
	assert.True(t, trunkENI.canCreateMore())
	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
	assert.Len(t, trunkENI.deleteQueue, 1)

	// Retry: the failed ENI still occupies a slot, so the ledger now reports full.
	assert.Equal(t, limit, len(trunkENI.uidToBranchENIMap)+len(trunkENI.deleteQueue))
	assert.False(t, trunkENI.canCreateMore())
	_, err = trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.ErrorIs(t, err, ErrCurrentlyAtMaxCapacity)
	assert.True(t, trunkENI.IsRestoredFromNodeNetworkState(), "restored state allows the capacity error to trigger resync")
}

// TestTrunkENI_HiddenOrphan_DeleteWorkerWinsRace covers the interleaving where the
// delete worker drains the failed ENI before the pod retries. canCreateMore counts
// usedBranches+len(deleteQueue), so once the queue is drained the ledger reports
// room again and the retry re-associates instead of reporting capacity.
//
// This documents the boundary of the capacity-only trigger: whether recovery is
// reached depends on which worker wins, so the failed ENI must keep its slot
// accounted for while it cools down.
func TestTrunkENI_HiddenOrphan_DeleteWorkerWinsRace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.restoredFromNodeNetworkState = true
	fillLedgerToLimitMinusOne(trunkENI)

	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()
	mockHelper.EXPECT().CreateNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(BranchInterface1, nil).AnyTimes()
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, gomock.Any()).Return(nil, MockError).AnyTimes()
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil)
	mockHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(nil).AnyTimes()

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.Error(t, err)
	assert.Len(t, trunkENI.deleteQueue, 1)

	// The delete worker runs before the pod retry. The failed ENI carries a
	// deletion timestamp, so it is held for the cool-down period and keeps its slot
	// accounted for; the retry therefore still reports capacity rather than
	// silently re-associating against a full trunk.
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).
		Return(createCoolDownMockCM("30"), nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, trunkENI.log)
	trunkENI.DeleteCooledDownENIs()
	assert.Len(t, trunkENI.deleteQueue, 1, "a freshly failed ENI must cool down, not vanish from the ledger")

	_, err = trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.ErrorIs(t, err, ErrCurrentlyAtMaxCapacity,
		"capacity must still be reported after the delete worker runs, otherwise recovery is never armed")
}

// branchENIWithVlanTag builds an EC2 branch ENI carrying the given VLAN tag.
func branchENIWithVlanTag(id string, vlanID int) *awsEc2Types.NetworkInterface {
	return &awsEc2Types.NetworkInterface{
		InterfaceType:      awsEc2Types.NetworkInterfaceTypeBranch,
		NetworkInterfaceId: aws.String(id),
		TagSet:             []awsEc2Types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String(strconv.Itoa(vlanID))}},
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
			assert.False(t, trunkENI.IsRestoredFromNodeNetworkState())
			assert.Empty(t, trunkENI.uidToBranchENIMap)
		})
	}
}

// TestTrunkENI_DeleteCooledDownENIs_FailedAllocationDoesNotBlockQueue tests the
// delete queue's ordering assumption. DeleteCooledDownENIs stops at the first
// item that has not cooled down, so a freshly stamped failed allocation must be
// appended, not placed at the front where it would hold already-cooled ENIs back
// for another full period.
func TestTrunkENI_DeleteCooledDownENIs_FailedAllocationDoesNotBlockQueue(t *testing.T) {
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
	mockHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, gomock.Any()).Return(nil, MockError)
	mockHelper.EXPECT().GetBranchNetworkInterface(&trunkId, &SubnetId).Return(nil, nil)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 1, nil)
	assert.Error(t, err)

	// The cooled ENI must still be deleted on this pass.
	mockHelper.EXPECT().DeleteNetworkInterface(&cooled.ID).Return(nil)
	trunkENI.DeleteCooledDownENIs()

	assert.Equal(t, []string{Branch1Id}, queuedENIIDs(trunkENI),
		"only the freshly failed ENI should remain; the cooled one must not be blocked behind it")
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
