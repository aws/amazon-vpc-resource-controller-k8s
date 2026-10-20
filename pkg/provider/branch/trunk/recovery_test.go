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
	"bytes"
	"fmt"
	"strings"
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
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	recoveryPrimaryIP    = "192.168.0.10"
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

func writeTrunkMetric(t *testing.T, counter prometheus.Metric) float64 {
	t.Helper()
	value := &dto.Metric{}
	require.NoError(t, counter.Write(value))
	return value.GetCounter().GetValue()
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

// coldInstanceDetails is the DescribeInstances answer a cold initialization reads.
func coldInstanceDetails() *ec2types.Instance {
	primaryIP := aws.String(recoveryPrimaryIP)
	return &ec2types.Instance{
		InstanceId:       aws.String(InstanceId),
		InstanceType:     ec2types.InstanceType(InstanceType),
		SubnetId:         aws.String(SubnetId),
		PrivateIpAddress: primaryIP,
		NetworkInterfaces: []ec2types.InstanceNetworkInterface{{
			NetworkInterfaceId: aws.String(recoveryPrimaryENIID),
			PrivateIpAddress:   primaryIP,
			Groups:             []ec2types.GroupIdentifier{{GroupId: aws.String(recoveryRefreshedSG)}},
			Attachment:         &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: aws.Int32(0)},
		}},
	}
}

func validRestoredInterfaces(instanceID string) []ec2types.NetworkInterface {
	refreshedTCP := int32(600)
	return []ec2types.NetworkInterface{
		{
			NetworkInterfaceId: aws.String(recoveryPrimaryENIID),
			SubnetId:           aws.String(SubnetId),
			PrivateIpAddress:   aws.String(recoveryPrimaryIP),
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

func TestRestoredBranchesReadPrimaryOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
	assert.Empty(t, instance.CurrentInstanceSecurityGroups())
	conntrack := &ec2types.ConnectionTrackingSpecificationRequest{
		TcpEstablishedTimeout: aws.Int32(600),
	}
	describeCalls := 0
	var observedConntrack []*ec2types.ConnectionTrackingSpecificationRequest

	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		DoAndReturn(func([]string) ([]ec2types.NetworkInterface, error) {
			describeCalls++
			return validRestoredInterfaces(InstanceId), nil
		}).AnyTimes()
	helper.EXPECT().GetBranchNetworkInterface(gomock.Eq(aws.String(recoveryTrunkENIID))).
		Return(nil, nil)
	assert.NoError(t, prepareTrunk(trunk, nil))

	gomock.InOrder(
		helper.EXPECT().CreateNetworkInterface(
			&BranchEniDescription,
			&SubnetId,
			[]string{recoveryRefreshedSG},
			append(vlan1Tag, trunk.nodeIDTag...),
			nil,
			nil,
			gomock.Any(),
		).DoAndReturn(func(
			_, _ *string,
			_ []string,
			_ []ec2types.Tag,
			_ *config.IPResourceCount,
			_ *string,
			connectionTracking *ec2types.ConnectionTrackingSpecificationRequest,
		) (*ec2types.NetworkInterface, error) {
			observedConntrack = append(observedConntrack, connectionTracking)
			return BranchInterface1, nil
		}),
		helper.EXPECT().AssociateBranchToTrunk(
			aws.String(recoveryTrunkENIID),
			&Branch1Id,
			VlanId1,
		).Return(mockAssociationOutput1, nil),
		helper.EXPECT().CreateNetworkInterface(
			&BranchEniDescription,
			&SubnetId,
			[]string{recoveryRefreshedSG},
			append(vlan2Tag, trunk.nodeIDTag...),
			nil,
			nil,
			gomock.Any(),
		).DoAndReturn(func(
			_, _ *string,
			_ []string,
			_ []ec2types.Tag,
			_ *config.IPResourceCount,
			_ *string,
			connectionTracking *ec2types.ConnectionTrackingSpecificationRequest,
		) (*ec2types.NetworkInterface, error) {
			observedConntrack = append(observedConntrack, connectionTracking)
			return BranchInterface2, nil
		}),
		helper.EXPECT().AssociateBranchToTrunk(
			aws.String(recoveryTrunkENIID),
			&Branch2Id,
			VlanId2,
		).Return(mockAssociationOutput2, nil),
	)

	first, err := trunk.CreateAndAssociateBranchENIs(MockPod1, nil, 1)
	assert.NoError(t, err)
	second, err := trunk.CreateAndAssociateBranchENIs(MockPod2, nil, 1)
	assert.NoError(t, err)
	assert.Equal(t, Branch1Id, first[0].ID)
	assert.Equal(t, Branch2Id, second[0].ID)
	assert.Equal(t, 1, describeCalls)
	assert.Equal(t, []*ec2types.ConnectionTrackingSpecificationRequest{
		conntrack,
		conntrack,
	}, observedConntrack)
}

func TestColdInitTrunkNeedsNoPreparation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helper := mock_api.NewMockEC2APIHelper(ctrl)
	trunk := NewTrunkENI(zap.New(), ec2.NewEC2Instance(NodeName, InstanceId, config.OSLinux, zap.New()), helper)
	helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)

	require.NoError(t, trunk.InitTrunk(nil))
	require.False(t, trunk.NeedsPreparation())
}

func TestValidateRestoredInterfacesRequiresColdInitForInvalidCheckpoint(t *testing.T) {
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

			require.ErrorIs(t, trunk.validateRestoredNetworkInterfaces(recoveryTrunkENIID), errNeedsColdInit)
			assert.True(t, trunk.NeedsPreparation())
		})
	}
}

func TestRestoredCheckpointSubnetMustMatchThePrimaryENI(t *testing.T) {
	tests := map[string]func([]ec2types.NetworkInterface){
		"primary in another subnet": func(interfaces []ec2types.NetworkInterface) {
			interfaces[0].SubnetId = aws.String(recoveryOldSubnetID)
		},
		"primary without a subnet": func(interfaces []ec2types.NetworkInterface) {
			interfaces[0].SubnetId = nil
		},
		"primary address outside the checkpoint CIDR": func(interfaces []ec2types.NetworkInterface) {
			interfaces[0].PrivateIpAddress = aws.String("10.9.0.5")
		},
		"primary without an address": func(interfaces []ec2types.NetworkInterface) {
			interfaces[0].PrivateIpAddress = nil
		},
	}

	for name, breakPrimary := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
			interfaces := validRestoredInterfaces(InstanceId)
			breakPrimary(interfaces)
			helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
				Return(interfaces, nil)

			err := trunk.validateRestoredNetworkInterfaces(recoveryTrunkENIID)

			require.ErrorIs(t, err, errNeedsColdInit)
			assert.True(t, trunk.NeedsPreparation())
			// A rejected checkpoint must not update the instance.
			assert.Empty(t, instance.CurrentInstanceSecurityGroups())
		})
	}
}

func TestCheckpointSubnetMismatchFallsBackToColdInit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
	interfaces := validRestoredInterfaces(InstanceId)
	interfaces[0].SubnetId = aws.String(recoveryOldSubnetID)
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(interfaces, nil)
	helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(coldInstanceDetails(), nil)
	helper.EXPECT().GetSubnet(aws.String(SubnetId)).
		Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil)
	helper.EXPECT().GetInstanceNetworkInterface(aws.String(InstanceId)).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).
		Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)

	checkpointInstance, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) { return nil, nil })

	require.NoError(t, err)
	require.NotNil(t, checkpointInstance, "the caller must re-persist the checkpoint after cold init")
	require.Same(t, instance, checkpointInstance)
	assert.False(t, trunk.NeedsPreparation())
	assert.Equal(t, SubnetId, instance.SubnetID())
}

// A checkpoint that EC2 contradicts is cold-initialized, counted once and logged once as an error.
func TestRestoredCheckpointMismatchColdInitsAndReportsOnce(t *testing.T) {
	mismatches := map[string]func([]ec2types.NetworkInterface) []ec2types.NetworkInterface{
		"trunk typed interface": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[1].InterfaceType = ec2types.NetworkInterfaceTypeInterface
			return interfaces
		},
		"primary absent": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			return interfaces[1:]
		},
		"primary on another instance": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[0].Attachment.InstanceId = aws.String("i-other")
			return interfaces
		},
		"primary at device index 1": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[0].Attachment.DeviceIndex = aws.Int32(1)
			return interfaces
		},
		"trunk absent": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			return interfaces[:1]
		},
		"trunk not attached": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[1].Attachment.Status = ec2types.AttachmentStatusDetached
			return interfaces
		},
		"subnet mismatch": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[0].SubnetId = aws.String(recoveryOldSubnetID)
			return interfaces
		},
		"primary IP outside the checkpoint CIDR": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[0].PrivateIpAddress = aws.String("10.9.0.5")
			return interfaces
		},
		"primary refresh without security groups": func(interfaces []ec2types.NetworkInterface) []ec2types.NetworkInterface {
			interfaces[0].Groups = nil
			return interfaces
		},
	}

	for name, mismatch := range mismatches {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
			var logs bytes.Buffer
			trunk.log = zap.New(zap.WriteTo(&logs))
			helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
				Return(mismatch(validRestoredInterfaces(InstanceId)), nil)
			helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(coldInstanceDetails(), nil)
			helper.EXPECT().GetSubnet(aws.String(SubnetId)).
				Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil)
			helper.EXPECT().GetInstanceNetworkInterface(aws.String(InstanceId)).Return(instanceNwInterfaces, nil)
			helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).
				Return(nil)
			helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)
			mismatchCount := trunkENIOperationsErrCount.WithLabelValues("restored_checkpoint_mismatch")
			before := writeTrunkMetric(t, mismatchCount)

			checkpointInstance, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) { return nil, nil })

			require.NoError(t, err)
			require.Same(t, instance, checkpointInstance)
			require.Equal(t, before+1, writeTrunkMetric(t, mismatchCount))
			errorLines := errorLogLines(&logs)
			require.Len(t, errorLines, 1)
			require.Contains(t, errorLines[0], recoveryTrunkENIID)
			require.Contains(t, errorLines[0], errNeedsColdInit.Error())
		})
	}
}

// errorLogLines returns the Error lines of a captured controller log.
func errorLogLines(logs *bytes.Buffer) []string {
	var lines []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, `"level":"error"`) {
			lines = append(lines, line)
		}
	}
	return lines
}

// A rejected checkpoint is counted and logged once, even when the instance reload fails and the attempt retries.
func TestRestoredCheckpointMismatchIsReportedOncePerRejection(t *testing.T) {
	ctrl := gomock.NewController(t)
	trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
	var logs bytes.Buffer
	trunk.log = zap.New(zap.WriteTo(&logs))
	interfaces := validRestoredInterfaces(InstanceId)
	interfaces[0].SubnetId = aws.String(recoveryOldSubnetID)
	// The checkpoint is validated once; after the failed reload the retry goes straight to cold init.
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(interfaces, nil)
	gomock.InOrder(
		helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(nil, MockError),
		helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(coldInstanceDetails(), nil),
	)
	helper.EXPECT().GetSubnet(aws.String(SubnetId)).
		Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil)
	helper.EXPECT().GetInstanceNetworkInterface(aws.String(InstanceId)).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).
		Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)
	mismatchCount := trunkENIOperationsErrCount.WithLabelValues("restored_checkpoint_mismatch")
	before := writeTrunkMetric(t, mismatchCount)

	require.ErrorIs(t, prepareTrunk(trunk, nil), MockError)
	trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
	checkpointInstance, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) { return nil, nil })

	require.NoError(t, err)
	require.Same(t, instance, checkpointInstance)
	require.Equal(t, before+1, writeTrunkMetric(t, mismatchCount))
	require.Len(t, errorLogLines(&logs), 1)
}

func TestRestoredTrunkDetachedNeedsColdInit(t *testing.T) {
	tests := map[string]func([]ec2types.NetworkInterface){
		"primary detached": func(interfaces []ec2types.NetworkInterface) {
			interfaces[0].Attachment.Status = ec2types.AttachmentStatusDetached
		},
		"trunk detached": func(interfaces []ec2types.NetworkInterface) {
			interfaces[1].Attachment.Status = ec2types.AttachmentStatusDetached
		},
		"trunk attachment missing": func(interfaces []ec2types.NetworkInterface) {
			interfaces[1].Attachment = nil
		},
	}

	for name, detach := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunk, helper, instance := getMockHelperInstanceAndTrunkObject(ctrl)
			instance.EXPECT().RestoredTrunkENIID().Return(recoveryTrunkENIID).Times(1)
			assert.NoError(t, trunk.InitTrunk(nil))
			trunk.prepared.Store(false)

			interfaces := validRestoredInterfaces(InstanceId)
			detach(interfaces)
			instance.EXPECT().PrimaryNetworkInterfaceID().Return(recoveryPrimaryENIID)
			instance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
			instance.EXPECT().RefreshPrimaryNetworkInterface(gomock.Any()).
				Return(nil).AnyTimes()
			helper.EXPECT().DescribeNetworkInterfaces(
				[]string{recoveryPrimaryENIID, recoveryTrunkENIID},
			).Return(interfaces, nil)
			helper.EXPECT().GetBranchNetworkInterface(
				gomock.Eq(aws.String(recoveryTrunkENIID)),
			).Return(nil, nil).AnyTimes()

			require.ErrorIs(t, trunk.validateRestoredNetworkInterfaces(recoveryTrunkENIID), errNeedsColdInit)
			assert.Empty(t, trunk.uidToBranchENIMap)
			assert.Empty(t, trunk.deleteQueue)
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
	trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
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

	leaderDone := make(chan error, 1)
	go func() { leaderDone <- prepareTrunk(trunk, nil) }()
	<-describeStarted

	const callers = 8
	errs := make(chan error, callers)
	var workers sync.WaitGroup
	for index := 0; index < callers; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errs <- prepareTrunk(trunk, nil)
		}()
	}
	for index := 0; index < callers; index++ {
		select {
		case err := <-errs:
			var deferred *PreparationDeferred
			require.ErrorAs(t, err, &deferred)
			require.Nil(t, deferred.Err)
		case <-time.After(time.Second):
			close(releaseDescribe)
			t.Fatal("follower blocked behind EC2 validation")
		}
	}
	workers.Wait()
	close(releaseDescribe)
	require.NoError(t, <-leaderDone)
	require.NoError(t, prepareTrunk(trunk, nil))
}

func TestPrepareForAllocationSnapshotsAfterProvisionalStateChanges(t *testing.T) {
	operations := map[string]func(*testing.T, *trunkENI){
		"reconcile": func(_ *testing.T, trunk *trunkENI) {
			trunk.Reconcile(nil)
		},
		"pod deletion": func(t *testing.T, trunk *trunkENI) {
			// The push took prepareMu before waiting on the inner lock, so it runs instead of deferring.
			assert.False(t, trunk.PushBranchENIsToCoolDownQueue("pod-uid"))
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
				operation(t, trunk)
				close(operationDone)
			}()
			assert.Eventually(t, func() bool {
				if trunk.prepareMu.TryLock() {
					trunk.prepareMu.Unlock()
					return false
				}
				return true
			}, time.Second, time.Millisecond)

			_, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) {
				t.Error("pod snapshot was taken before cleanup finished")
				return nil, nil
			})
			var deferred *PreparationDeferred
			require.ErrorAs(t, err, &deferred)
			trunk.lock.Unlock()
			<-operationDone
			require.NoError(t, prepareTrunk(trunk, nil))
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
	}
	helper.EXPECT().DisassociateTrunkInterface(aws.String("association-" + recoveryBranchENIID)).Return(nil)
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

const (
	recoveryOrphanENIID  = "eni-00000000000000007"
	recoveryCustomSubnet = "subnet-custom"
	recoveryCustomSG     = "sg-00000000000000006"
)

// trunkDriftCounts returns the SG and subnet drift counters.
func trunkDriftCounts(t *testing.T) (securityGroups float64, subnet float64) {
	t.Helper()
	return writeTrunkMetric(t, unreconciledTrunkENICount.WithLabelValues(SecurityGroupsLabel)),
		writeTrunkMetric(t, unreconciledTrunkENICount.WithLabelValues(SubnetLabel))
}

func TestRestoredTrunkReportsConfigDrift(t *testing.T) {
	tests := map[string]struct {
		customSubnet    string
		customSGs       []string
		trunkSubnet     string
		trunkSGs        []string
		wantSGDrift     float64
		wantSubnetDrift float64
	}{
		"security group and subnet drift": {
			customSubnet:    recoveryCustomSubnet,
			customSGs:       []string{recoveryCustomSG},
			trunkSubnet:     recoveryOldSubnetID,
			trunkSGs:        []string{recoveryRefreshedSG},
			wantSGDrift:     1,
			wantSubnetDrift: 1,
		},
		"trunk matches the ENIConfig": {
			customSubnet: recoveryCustomSubnet,
			customSGs:    []string{recoveryCustomSG},
			trunkSubnet:  recoveryCustomSubnet,
			trunkSGs:     []string{recoveryCustomSG},
		},
		"no custom networking spec": {
			trunkSubnet: recoveryOldSubnetID,
			trunkSGs:    []string{recoveryRefreshedSG},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
			instance.SetNewCustomNetworkingSpec(test.customSubnet, test.customSGs)
			interfaces := validRestoredInterfaces(InstanceId)
			interfaces[1].SubnetId = aws.String(test.trunkSubnet)
			for _, group := range test.trunkSGs {
				interfaces[1].Groups = append(interfaces[1].Groups,
					ec2types.GroupIdentifier{GroupId: aws.String(group)})
			}
			helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
				Return(interfaces, nil)
			helper.EXPECT().GetBranchNetworkInterface(aws.String(recoveryTrunkENIID)).Return(nil, nil)
			sgBefore, subnetBefore := trunkDriftCounts(t)

			require.NoError(t, prepareTrunk(trunk, nil))

			sgAfter, subnetAfter := trunkDriftCounts(t)
			assert.Equal(t, test.wantSGDrift, sgAfter-sgBefore)
			assert.Equal(t, test.wantSubnetDrift, subnetAfter-subnetBefore)
		})
	}
}

func TestColdFallbackReportsConfigDriftOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// The primary ENI has no security groups, so the refresh fails and preparation falls back to cold init.
	trunk, helper, instance := newRestoredTrunk(t, ctrl, nil)
	instance.SetNewCustomNetworkingSpec(recoveryCustomSubnet, []string{recoveryCustomSG})
	interfaces := validRestoredInterfaces(InstanceId)
	interfaces[0].Groups = nil
	interfaces[1].SubnetId = aws.String(recoveryOldSubnetID)
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(interfaces, nil)
	helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(coldInstanceDetails(), nil)
	helper.EXPECT().GetSubnet(aws.String(SubnetId)).
		Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil)
	helper.EXPECT().GetSubnet(aws.String(recoveryCustomSubnet)).
		Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil)
	helper.EXPECT().GetInstanceNetworkInterface(aws.String(InstanceId)).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).
		Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)
	sgBefore, subnetBefore := trunkDriftCounts(t)

	_, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) { return nil, nil })
	require.NoError(t, err)

	sgAfter, subnetAfter := trunkDriftCounts(t)
	assert.Equal(t, float64(1), sgAfter-sgBefore)
	assert.Equal(t, float64(1), subnetAfter-subnetBefore)
}

func TestColdInitTrunkReportsConfigDrift(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helper := mock_api.NewMockEC2APIHelper(ctrl)
	instance := ec2.NewEC2Instance(NodeName, InstanceId, config.OSLinux, zap.New())
	instance.SetNewCustomNetworkingSpec(recoveryCustomSubnet, []string{recoveryCustomSG})
	trunk := NewTrunkENI(zap.New(), instance, helper)
	helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).
		Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)
	sgBefore, subnetBefore := trunkDriftCounts(t)

	require.NoError(t, trunk.InitTrunk(nil))

	sgAfter, subnetAfter := trunkDriftCounts(t)
	assert.Equal(t, float64(1), sgAfter-sgBefore)
	assert.Equal(t, float64(1), subnetAfter-subnetBefore)
}

func TestOrphanDoesNotTakeAVlanThatAnOwnerHolds(t *testing.T) {
	trunk := &trunkENI{log: zap.New()}
	owner := recoveryPod("owner-pod", recoveryBranchENIID, VlanId1)

	state := trunk.buildRecoveredBranchState([]v1.Pod{owner}, []*ec2types.NetworkInterface{
		taggedBranch(recoveryBranchENIID, VlanId1),
		taggedBranch(recoveryOrphanENIID, VlanId1),
	})

	owned := state.uidToBranchENIMap["owner-pod"]
	require.Len(t, owned, 1)
	assert.Equal(t, recoveryBranchENIID, owned[0].ID)
	assert.Equal(t, VlanId1, owned[0].VlanID)
	if assert.Len(t, state.deleteQueue, 1) {
		assert.Equal(t, recoveryOrphanENIID, state.deleteQueue[0].ID)
		assert.Zero(t, state.deleteQueue[0].VlanID)
	}
	assert.True(t, state.usedVlanIDs[VlanId1])
}

func TestDeletingAnOrphanKeepsTheOwnersVlanReserved(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	owner := recoveryPod("owner-pod", recoveryBranchENIID, VlanId1)
	trunk, helper, _ := newRestoredTrunk(t, ctrl, []v1.Pod{owner})
	helper.EXPECT().DescribeNetworkInterfaces([]string{recoveryPrimaryENIID, recoveryTrunkENIID}).
		Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return([]*ec2types.NetworkInterface{
		taggedBranch(recoveryBranchENIID, VlanId1),
		taggedBranch(recoveryOrphanENIID, VlanId1),
	}, nil)
	require.NoError(t, prepareTrunk(trunk, []v1.Pod{owner}))

	require.Len(t, trunk.deleteQueue, 1)
	trunk.deleteQueue[0].deletionTimeStamp = time.Time{}
	helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryOrphanENIID)).Return(nil)
	trunk.DeleteCooledDownENIs()

	require.Empty(t, trunk.deleteQueue)
	require.True(t, trunk.usedVlanIds[VlanId1], "deleting the orphan freed a VLAN its owner still uses")

	helper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, []string{recoveryRefreshedSG},
		append(vlan2Tag, trunk.nodeIDTag...), nil, nil, gomock.Any()).Return(BranchInterface2, nil)
	helper.EXPECT().AssociateBranchToTrunk(aws.String(recoveryTrunkENIID), &Branch2Id, VlanId2).
		Return(mockAssociationOutput2, nil)
	allocated, err := trunk.CreateAndAssociateBranchENIs(MockPod2, nil, 1)
	require.NoError(t, err)
	require.Equal(t, VlanId2, allocated[0].VlanID)

	freedUnused := trunkENIOperationsErrCount.WithLabelValues("free_unused_vlan_id")
	freedUnusedBefore := writeTrunkMetric(t, freedUnused)
	trunk.PushBranchENIsToCoolDownQueue("owner-pod")
	require.Len(t, trunk.deleteQueue, 1)
	trunk.deleteQueue[0].deletionTimeStamp = time.Time{}
	helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).Return(nil)
	trunk.DeleteCooledDownENIs()

	require.False(t, trunk.usedVlanIds[VlanId1])
	require.Equal(t, freedUnusedBefore, writeTrunkMetric(t, freedUnused))
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
