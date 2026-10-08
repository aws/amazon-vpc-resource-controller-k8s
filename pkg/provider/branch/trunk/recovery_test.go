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
	"sync"
	"testing"
	"time"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/golang/mock/gomock"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	recoveryPrimaryENIID = "eni-00000000000000001"
	recoveryTrunkENIID   = "eni-00000000000000002"
	recoveryBranchENIID  = "eni-00000000000000003"
	recoveryRefreshedSG  = "sg-00000000000000005"
	recoveryOldSubnetID  = "subnet-old"
)

func newRestoredTrunk(t *testing.T, ctrl *gomock.Controller, pods []v1.Pod) (*trunkENI, *mock_api.MockEC2APIHelper, ec2.EC2Instance) {
	t.Helper()

	instance := ec2.NewEC2Instance(NodeName, InstanceId, config.OSLinux, zap.New())
	assert.NoError(t, instance.LoadFromNodeNetworkState(validRecoveryNodeNetworkState(), recoveryTrunkENIID))
	assert.NoError(t, instance.UpdateCurrentSubnetAndCidrBlock(nil))

	helper := mock_api.NewMockEC2APIHelper(ctrl)
	trunk := NewTrunkENI(zap.New(), instance, helper).(*trunkENI)
	assert.NoError(t, trunk.InitTrunk(pods))
	return trunk, helper, instance
}

func prepareTrunk(trunk *trunkENI, pods []v1.Pod) error {
	_, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) {
		return pods, nil
	})
	return err
}

func TestCorruptOrphanMetricContract(t *testing.T) {
	counter := trunkENIOperationsErrCount.WithLabelValues("corrupt_orphan_branch_eni")
	assert.Contains(t, counter.Desc().String(), `fqName: "trunk_eni_operations_err_count"`)
	assert.Contains(t, counter.Desc().String(), `variableLabels: {operation}`)
	value := &dto.Metric{}
	assert.NoError(t, counter.Write(value))
	before := value.GetCounter().GetValue()

	trunk := &trunkENI{log: zap.New()}
	state := trunk.buildRecoveredBranchState(nil, []*ec2types.NetworkInterface{{
		NetworkInterfaceId: aws.String(recoveryBranchENIID),
	}})

	assert.Len(t, state.deleteQueue, 1)
	value.Reset()
	assert.NoError(t, counter.Write(value))
	assert.Equal(t, before+1, value.GetCounter().GetValue())
}

func validRecoveryNodeNetworkState() rcv1alpha1.NodeNetworkState {
	return rcv1alpha1.NodeNetworkState{
		InstanceID:                InstanceId,
		InstanceType:              InstanceType,
		SubnetID:                  SubnetId,
		SubnetCIDRBlock:           SubnetCidrBlock,
		PrimaryNetworkInterfaceID: recoveryPrimaryENIID,
	}
}

func validRestoredInterfaces(instanceID string) []ec2types.NetworkInterface {
	refreshedTCP := int32(600)
	return []ec2types.NetworkInterface{
		{
			NetworkInterfaceId: aws.String(recoveryPrimaryENIID),
			Groups: []ec2types.GroupIdentifier{
				{GroupId: aws.String(recoveryRefreshedSG)},
			},
			Attachment: &ec2types.NetworkInterfaceAttachment{
				InstanceId:  aws.String(instanceID),
				Status:      ec2types.AttachmentStatusAttached,
				DeviceIndex: aws.Int32(0),
			},
			ConnectionTrackingConfiguration: &ec2types.ConnectionTrackingConfiguration{
				TcpEstablishedTimeout: &refreshedTCP,
			},
		},
		{
			NetworkInterfaceId: aws.String(recoveryTrunkENIID),
			InterfaceType:      ec2types.NetworkInterfaceTypeTrunk,
			Attachment: &ec2types.NetworkInterfaceAttachment{
				InstanceId: aws.String(instanceID),
				Status:     ec2types.AttachmentStatusAttached,
			},
		},
	}
}

func recoveryPod(uid, eniID string, vlanID int) v1.Pod {
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID(uid),
			Annotations: map[string]string{
				config.ResourceNamePodENI: fmt.Sprintf(`[{"eniId":%q,"vlanId":%d}]`, eniID, vlanID),
			},
		},
	}
}

func TestPrepareForAllocationValidatesOnceRefreshesPrimaryAndFindsOldSubnetBranches(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
	branchVLAN := 17
	branch := &ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String(recoveryBranchENIID),
		SubnetId:           aws.String(recoveryOldSubnetID),
		TagSet: []ec2types.Tag{
			{Key: aws.String(config.VLandIDTag), Value: aws.String(fmt.Sprint(branchVLAN))},
		},
	}
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Eq(aws.String(recoveryTrunkENIID))).
		Return([]*ec2types.NetworkInterface{branch}, nil)

	assert.NoError(t, prepareTrunk(trunk, nil))
	assert.NoError(t, prepareTrunk(trunk, nil))
	assert.False(t, trunk.NeedsPreparation())
	assert.True(t, trunk.usedVlanIds[branchVLAN])
	assert.Len(t, trunk.deleteQueue, 1)
	assert.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
	assert.Equal(t, []string{recoveryRefreshedSG}, instance.CurrentInstanceSecurityGroups())
	tcpTimeout, _, _ := instance.GetConnectionTrackingSpec()
	assert.Equal(t, int32(600), *tcpTimeout)
}

func TestRestoredBranchCreationUsesRefreshedPrimaryENISettings(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
	assert.Empty(t, instance.CurrentInstanceSecurityGroups())

	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Eq(aws.String(recoveryTrunkENIID))).
		Return(nil, nil)
	assert.NoError(t, prepareTrunk(trunk, nil))

	helper.EXPECT().CreateNetworkInterface(
		&BranchEniDescription,
		&SubnetId,
		[]string{recoveryRefreshedSG},
		append(vlan1Tag, trunk.nodeIDTag...),
		nil,
		nil,
		gomock.Eq(&ec2types.ConnectionTrackingSpecificationRequest{
			TcpEstablishedTimeout: aws.Int32(600),
		}),
	).Return(BranchInterface1, nil)
	helper.EXPECT().AssociateBranchToTrunk(aws.String(recoveryTrunkENIID), &Branch1Id, VlanId1).
		Return(mockAssociationOutput1, nil)

	_, err := trunk.CreateAndAssociateBranchENIs(MockPod2, nil, 1)
	assert.NoError(t, err)
}

func TestPrepareForAllocationColdInitializedTrunkSkipsValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, instance := getMockHelperInstanceAndTrunkObject(ctrl)
	instance.EXPECT().InstanceID().Return(InstanceId)
	instance.EXPECT().RestoredTrunkENIID().Return("")
	instance.EXPECT().GetCustomNetworkingSpec().Return("", nil)
	helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(
		&trunkId,
		string(ec2types.AttachmentStatusAttached),
	).Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)

	assert.NoError(t, trunk.InitTrunk(nil))
	assert.NoError(t, prepareTrunk(trunk, nil))
	assert.False(t, trunk.NeedsPreparation())
}

func TestPrepareForAllocationReturnsColdInitSentinelForInvalidRestoredTrunk(t *testing.T) {
	tests := map[string]struct {
		interfaces []ec2types.NetworkInterface
		err        error
	}{
		"missing trunk": {
			interfaces: validRestoredInterfaces(InstanceId)[:1],
		},
		"missing primary": {
			interfaces: validRestoredInterfaces(InstanceId)[1:],
		},
		"wrong trunk type": {
			interfaces: func() []ec2types.NetworkInterface {
				interfaces := validRestoredInterfaces(InstanceId)
				interfaces[1].InterfaceType = ec2types.NetworkInterfaceTypeBranch
				return interfaces
			}(),
		},
		"attached elsewhere": {
			interfaces: validRestoredInterfaces("i-other"),
		},
		"primary without attachment": {
			interfaces: func() []ec2types.NetworkInterface {
				interfaces := validRestoredInterfaces(InstanceId)
				interfaces[0].Attachment = nil
				return interfaces
			}(),
		},
		"primary is not device zero": {
			interfaces: func() []ec2types.NetworkInterface {
				interfaces := validRestoredInterfaces(InstanceId)
				interfaces[0].Attachment.DeviceIndex = aws.Int32(1)
				return interfaces
			}(),
		},
		"primary refresh fails": {
			interfaces: func() []ec2types.NetworkInterface {
				interfaces := validRestoredInterfaces(InstanceId)
				interfaces[0].Groups = nil
				return interfaces
			}(),
		},
		"not found API error": {
			err: &smithy.GenericAPIError{
				Code:    "InvalidNetworkInterfaceID.NotFound",
				Message: "missing",
			},
		},
		"malformed API error": {
			err: &smithy.GenericAPIError{
				Code:    "InvalidNetworkInterfaceID.Malformed",
				Message: "malformed",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunk, helper, _ := newRestoredTrunk(t, ctrl, nil)
			helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
				Return(test.interfaces, test.err)

			assert.ErrorIs(t, prepareTrunk(trunk, nil), ErrNeedsColdInit)
			assert.True(t, trunk.NeedsPreparation())
			trunk.CompletePreparation(false)
		})
	}
}

func TestPrepareForAllocationRetriesTransientFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, _ := newRestoredTrunk(t, ctrl, nil)
	gomock.InOrder(
		helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
			Return(nil, MockError),
		helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
			Return(validRestoredInterfaces(InstanceId), nil),
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(nil, nil),
	)

	assert.ErrorIs(t, prepareTrunk(trunk, nil), MockError)
	assert.True(t, trunk.NeedsPreparation())
	assert.NoError(t, prepareTrunk(trunk, nil))
	assert.False(t, trunk.NeedsPreparation())
}

func TestPrepareForAllocationSharesConcurrentValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, _ := newRestoredTrunk(t, ctrl, nil)
	describeStarted := make(chan struct{})
	releaseDescribe := make(chan struct{})
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		DoAndReturn(func([]string) ([]ec2types.NetworkInterface, error) {
			close(describeStarted)
			<-releaseDescribe
			return validRestoredInterfaces(InstanceId), nil
		})
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(nil, nil)

	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer wg.Done()
			errs <- prepareTrunk(trunk, nil)
		}()
	}

	<-describeStarted
	select {
	case err := <-errs:
		t.Fatalf("concurrent allocation returned before validation completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseDescribe)
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
}

func TestPrepareForAllocationSnapshotsAfterProvisionalStateChanges(t *testing.T) {
	operations := map[string]func(*trunkENI){
		"reconcile": func(trunk *trunkENI) {
			trunk.Reconcile(nil)
		},
		"pod deletion": func(trunk *trunkENI) {
			trunk.PushBranchENIsToCoolDownQueue("pod-uid")
		},
	}

	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			pod := recoveryPod("pod-uid", recoveryBranchENIID, 17)
			trunk, helper, _ := newRestoredTrunk(t, ctrl, []v1.Pod{pod})
			branch := &ec2types.NetworkInterface{
				NetworkInterfaceId: aws.String(recoveryBranchENIID),
				TagSet: []ec2types.Tag{
					{Key: aws.String(config.VLandIDTag), Value: aws.String("17")},
				},
			}
			helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
				Return(validRestoredInterfaces(InstanceId), nil)
			helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).
				Return([]*ec2types.NetworkInterface{branch}, nil)

			trunk.lock.Lock()
			operationDone := make(chan struct{})
			go func() {
				operation(trunk)
				close(operationDone)
			}()
			assert.Eventually(t, func() bool {
				if trunk.prepareMu.TryLock() {
					trunk.prepareMu.Unlock()
					return false
				}
				return true
			}, time.Second, time.Millisecond)

			prepareDone := make(chan error, 1)
			snapshotTaken := make(chan struct{})
			go func() {
				_, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) {
					close(snapshotTaken)
					return nil, nil
				})
				prepareDone <- err
			}()
			select {
			case <-snapshotTaken:
				t.Fatal("pod snapshot was taken before cleanup finished")
			case <-time.After(25 * time.Millisecond):
			}

			trunk.lock.Unlock()
			<-operationDone
			assert.NoError(t, <-prepareDone)
			assert.Len(t, trunk.deleteQueue, 1)
			assert.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
		})
	}
}

func TestPeriodicStateOperationsDoNotPrepareRestoredTrunk(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	pod := recoveryPod("pod-uid", recoveryBranchENIID, 17)
	trunk, _, _ := newRestoredTrunk(t, ctrl, []v1.Pod{pod})

	assert.False(t, trunk.Reconcile([]v1.Pod{pod}))
	trunk.PushBranchENIsToCoolDownQueue("missing")
	trunk.DeleteCooledDownENIs()
	assert.True(t, trunk.NeedsPreparation())
}

func TestDeleteCooledDownENIsWaitsForPreparationAndDropsFabricatedOwnership(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	fabricatedENIID := "eni-00000000000000004"
	pods := []v1.Pod{
		recoveryPod("real-pod", recoveryBranchENIID, 17),
		recoveryPod("fabricated-pod", fabricatedENIID, 18),
	}
	trunk, helper, _ := newRestoredTrunk(t, ctrl, pods)
	trunk.PushBranchENIsToCoolDownQueue("real-pod")
	trunk.PushBranchENIsToCoolDownQueue("fabricated-pod")
	for _, branchENI := range trunk.deleteQueue {
		branchENI.AssociationID = "association-" + branchENI.ID
		branchENI.deletionTimeStamp = time.Time{}
	}

	trunk.DeleteCooledDownENIs()

	if assert.Len(t, trunk.deleteQueue, 2) {
		assert.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
		assert.Equal(t, fabricatedENIID, trunk.deleteQueue[1].ID)
	}

	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return([]*ec2types.NetworkInterface{{
		NetworkInterfaceId: aws.String(recoveryBranchENIID),
		TagSet: []ec2types.Tag{
			{Key: aws.String(config.VLandIDTag), Value: aws.String("17")},
		},
	}}, nil)
	assert.NoError(t, prepareTrunk(trunk, nil))

	if assert.Len(t, trunk.deleteQueue, 1) {
		assert.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
		trunk.deleteQueue[0].deletionTimeStamp = time.Time{}
	}
	helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).Return(nil)

	trunk.DeleteCooledDownENIs()

	assert.Empty(t, trunk.deleteQueue)
}

func TestPreparedDeleteDoesNotHoldPrepareMutexAcrossEC2(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, _ := newRestoredTrunk(t, ctrl, nil)
	trunk.prepared.Store(true)
	trunk.deleteQueue = []*ENIDetails{{ID: recoveryBranchENIID}}

	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).
		DoAndReturn(func(*string) error {
			close(deleteStarted)
			<-releaseDelete
			return nil
		})

	deleteDone := make(chan struct{})
	go func() {
		trunk.DeleteCooledDownENIs()
		close(deleteDone)
	}()
	<-deleteStarted
	if !trunk.prepareMu.TryLock() {
		t.Fatal("prepared cleanup held prepareMu across EC2 deletion")
	}
	trunk.prepareMu.Unlock()
	close(releaseDelete)
	<-deleteDone
}

func TestPrepareForAllocationSkipsMalformedEC2BranchRecord(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, _ := newRestoredTrunk(t, ctrl, nil)
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return([]*ec2types.NetworkInterface{
		nil,
		{
			NetworkInterfaceId: aws.String(recoveryBranchENIID),
			TagSet: []ec2types.Tag{
				{Key: aws.String(config.VLandIDTag), Value: aws.String("17")},
			},
		},
	}, nil)

	assert.NoError(t, prepareTrunk(trunk, nil))
	assert.Len(t, trunk.deleteQueue, 1)
	assert.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
}

func TestPrepareForAllocationHandlesUnusableAnnotationsAndCorruptOrphans(t *testing.T) {
	tests := []struct {
		name             string
		annotation       string
		branchID         string
		branchTags       []ec2types.Tag
		wantOwned        bool
		wantOwnedVLAN    int
		wantDeleteQueue  bool
		wantReservedVLAN int
		wantFreeVLAN     int
	}{
		{
			name:          "valid ENI with invalid VLAN is preserved as owned",
			annotation:    `[{"eniId":"eni-00000000000000003","vlanId":-1}]`,
			wantOwned:     true,
			wantOwnedVLAN: 0,
		},
		{
			name:             "pod annotation VLAN remains authoritative",
			annotation:       `[{"eniId":"eni-00000000000000003","vlanId":17}]`,
			branchTags:       []ec2types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String("42")}},
			wantOwned:        true,
			wantOwnedVLAN:    17,
			wantReservedVLAN: 17,
			wantFreeVLAN:     42,
		},
		{
			name:             "legacy short ENI ID is accepted",
			annotation:       `[{"eniId":"eni-1234abcd","vlanId":17}]`,
			branchID:         "eni-1234abcd",
			wantOwned:        true,
			wantOwnedVLAN:    17,
			wantReservedVLAN: 17,
		},
		{
			name:            "malformed ENI ID is not ownership proof",
			annotation:      `[{"eniId":"eni-not-hex","vlanId":17}]`,
			wantDeleteQueue: true,
		},
		{
			name:             "malformed JSON is not ownership proof",
			annotation:       `[`,
			branchTags:       []ec2types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String("17")}},
			wantDeleteQueue:  true,
			wantReservedVLAN: 17,
		},
		{
			name:            "VLAN-less EC2 orphan is queued by ENI ID",
			wantDeleteQueue: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunk, helper, _ := newRestoredTrunk(t, ctrl, nil)
			pods := []v1.Pod{{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("pod-uid"),
					Annotations: map[string]string{
						config.ResourceNamePodENI: test.annotation,
					},
				},
			}}
			if test.annotation == "" {
				pods = nil
			}
			branchID := test.branchID
			if branchID == "" {
				branchID = recoveryBranchENIID
			}
			helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
				Return(validRestoredInterfaces(InstanceId), nil)
			helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return([]*ec2types.NetworkInterface{{
				NetworkInterfaceId: aws.String(branchID),
				TagSet:             test.branchTags,
			}}, nil)

			assert.NoError(t, prepareTrunk(trunk, pods))

			owned := trunk.uidToBranchENIMap["pod-uid"]
			assert.Equal(t, test.wantOwned, len(owned) == 1)
			if test.wantOwned {
				assert.Equal(t, test.wantOwnedVLAN, owned[0].VlanID)
			}
			assert.Equal(t, test.wantDeleteQueue, len(trunk.deleteQueue) == 1)
			if test.wantDeleteQueue {
				assert.Equal(t, branchID, trunk.deleteQueue[0].ID)
			}
			if test.wantReservedVLAN != 0 {
				assert.True(t, trunk.usedVlanIds[test.wantReservedVLAN])
			}
			if test.wantFreeVLAN != 0 {
				assert.False(t, trunk.usedVlanIds[test.wantFreeVLAN])
			}
		})
	}
}
