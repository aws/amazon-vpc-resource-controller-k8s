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
	"testing"
	"time"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func initCoolDownForTest(ctrl *gomock.Controller) {
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(
		&v1.ConfigMap{Data: map[string]string{config.BranchENICooldownPeriodKey: "30"}}, nil)
	cooldown.InitCoolDownPeriod(mockK8sAPI, zap.New(zap.UseDevMode(true)))
}

func TestCanonicalSGKey(t *testing.T) {
	assert.Equal(t, "sg-1,sg-2,sg-3", CanonicalSGKey([]string{"sg-3", "sg-1", "sg-2"}))
	assert.Equal(t, "sg-1,sg-2,sg-3", CanonicalSGKey([]string{"sg-1", "sg-2", "sg-3"}))
	assert.Equal(t, "sg-1", CanonicalSGKey([]string{"sg-1"}))
}

func TestBranchENIWithPrefix_AllocateIP(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		ENIDetail:  &ENIDetails{ID: "eni-123"},
		FreeIPs:    []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
		UsedIPs:    make(map[string]string),
		CoolingIPs: nil,
	}

	ip := sharedENI.AllocateIP("pod-uid-1")
	assert.Equal(t, "10.0.0.1", ip)
	assert.Equal(t, 2, len(sharedENI.FreeIPs))
	assert.Equal(t, "pod-uid-1", sharedENI.UsedIPs["10.0.0.1"])

	ip = sharedENI.AllocateIP("pod-uid-2")
	assert.Equal(t, "10.0.0.2", ip)
	assert.Equal(t, 1, len(sharedENI.FreeIPs))
}

func TestBranchENIWithPrefix_AllocateIP_Empty(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		FreeIPs: []string{},
		UsedIPs: make(map[string]string),
	}

	ip := sharedENI.AllocateIP("pod-uid-1")
	assert.Equal(t, "", ip)
}

func TestBranchENIWithPrefix_ReleaseIP(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		ENIDetail:  &ENIDetails{ID: "eni-123"},
		FreeIPs:    []string{},
		UsedIPs:    map[string]string{"10.0.0.1": "pod-uid-1", "10.0.0.2": "pod-uid-2"},
		CoolingIPs: nil,
	}

	ip := sharedENI.ReleaseIP("pod-uid-1")
	assert.Equal(t, "10.0.0.1", ip)
	assert.Equal(t, 1, len(sharedENI.UsedIPs))
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))
	assert.Equal(t, "10.0.0.1", sharedENI.CoolingIPs[0].IP)
	assert.Equal(t, "pod-uid-1", sharedENI.CoolingIPs[0].PodUID)
}

func TestBranchENIWithPrefix_ReleaseIP_NotFound(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		UsedIPs:    map[string]string{"10.0.0.1": "pod-uid-1"},
		CoolingIPs: nil,
	}

	ip := sharedENI.ReleaseIP("pod-uid-unknown")
	assert.Equal(t, "", ip)
	assert.Equal(t, 1, len(sharedENI.UsedIPs))
}

func TestBranchENIWithPrefix_HasFreeIPs(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{FreeIPs: []string{"10.0.0.1"}}
	assert.True(t, sharedENI.HasFreeIPs())

	sharedENI.FreeIPs = []string{}
	assert.False(t, sharedENI.HasFreeIPs())
}

func TestBranchENIWithPrefix_ProcessCoolDown(t *testing.T) {
	cooldownPeriod := 30 * time.Second

	sharedENI := &BranchENIWithPrefix{
		FreeIPs: []string{},
		UsedIPs: make(map[string]string),
		CoolingIPs: []CoolingIP{
			{IP: "10.0.0.1", PodUID: "pod-1", DeletionTimestamp: time.Now().Add(-60 * time.Second)},
			{IP: "10.0.0.2", PodUID: "pod-2", DeletionTimestamp: time.Now().Add(-10 * time.Second)},
		},
	}

	fullyDrained := sharedENI.ProcessCoolDown(cooldownPeriod)
	assert.False(t, fullyDrained)
	assert.Equal(t, 1, len(sharedENI.FreeIPs))
	assert.Equal(t, "10.0.0.1", sharedENI.FreeIPs[0])
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))
	assert.Equal(t, "10.0.0.2", sharedENI.CoolingIPs[0].IP)
}

func TestBranchENIWithPrefix_ProcessCoolDown_FullyDrained(t *testing.T) {
	cooldownPeriod := 30 * time.Second

	sharedENI := &BranchENIWithPrefix{
		FreeIPs: []string{"10.0.0.3"},
		UsedIPs: make(map[string]string),
		CoolingIPs: []CoolingIP{
			{IP: "10.0.0.1", PodUID: "pod-1", DeletionTimestamp: time.Now().Add(-60 * time.Second)},
			{IP: "10.0.0.2", PodUID: "pod-2", DeletionTimestamp: time.Now().Add(-60 * time.Second)},
		},
	}

	fullyDrained := sharedENI.ProcessCoolDown(cooldownPeriod)
	assert.True(t, fullyDrained)
	assert.Equal(t, 3, len(sharedENI.FreeIPs))
	assert.Equal(t, 0, len(sharedENI.CoolingIPs))
}

func TestBranchENIWithPrefix_ProcessCoolDown_NotDrainedWithUsedIPs(t *testing.T) {
	cooldownPeriod := 30 * time.Second

	sharedENI := &BranchENIWithPrefix{
		FreeIPs: []string{},
		UsedIPs: map[string]string{"10.0.0.5": "pod-active"},
		CoolingIPs: []CoolingIP{
			{IP: "10.0.0.1", PodUID: "pod-1", DeletionTimestamp: time.Now().Add(-60 * time.Second)},
		},
	}

	fullyDrained := sharedENI.ProcessCoolDown(cooldownPeriod)
	assert.False(t, fullyDrained)
	assert.Equal(t, 1, len(sharedENI.FreeIPs))
}

func TestBranchENIWithPrefix_IsFullyDrained(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		FreeIPs:    []string{"10.0.0.1"},
		UsedIPs:    make(map[string]string),
		CoolingIPs: nil,
	}
	assert.True(t, sharedENI.IsFullyDrained())

	sharedENI.UsedIPs["10.0.0.2"] = "pod-1"
	assert.False(t, sharedENI.IsFullyDrained())

	sharedENI.UsedIPs = make(map[string]string)
	sharedENI.CoolingIPs = []CoolingIP{{IP: "10.0.0.3"}}
	assert.False(t, sharedENI.IsFullyDrained())
}

func TestAllocateIPFromSharedENI_ExistingENI(t *testing.T) {
	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1,sg-2": {
			{
				ENIDetail: &ENIDetails{
					ID: "eni-shared-1", MACAdd: "AA:BB:CC:DD:EE:FF",
					VlanID: 5, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
					AssociationID: "assoc-1", PrefixCIDR: "10.0.0.0/28",
				},
				SecurityGroups: []string{"sg-1", "sg-2"},
				PrefixCIDRs:    []string{"10.0.0.0/28"},
				AllIPs:         []string{"10.0.0.0", "10.0.0.1", "10.0.0.2"},
				FreeIPs:        []string{"10.0.0.1", "10.0.0.2"},
				UsedIPs:        map[string]string{"10.0.0.0": "existing-pod"},
				CoolingIPs:     nil,
			},
		},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	pod := &MockPod2
	result, err := trunk.AllocateIPFromSharedENI(*pod, []string{"sg-1", "sg-2"})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "10.0.0.1", result.IPV4Addr)
	assert.Equal(t, "eni-shared-1", result.ID)
	assert.Equal(t, "10.0.0.0/28", result.PrefixCIDR)
	assert.Equal(t, 5, result.VlanID)

	alloc, exists := trunk.uidToPrefixAllocation[PodUID2]
	assert.True(t, exists)
	assert.Equal(t, "10.0.0.1", alloc.AssignedIP)
}

func TestAllocateIPFromSharedENI_SGOrderDoesNotMatter(t *testing.T) {
	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-a,sg-b": {
			{
				ENIDetail: &ENIDetails{
					ID: "eni-shared-1", MACAdd: "AA:BB:CC:DD:EE:FF",
					VlanID: 3, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
					AssociationID: "assoc-1",
				},
				SecurityGroups: []string{"sg-a", "sg-b"},
				PrefixCIDRs:    []string{"10.0.0.16/28"},
				AllIPs:         []string{"10.0.0.16", "10.0.0.17"},
				FreeIPs:        []string{"10.0.0.16", "10.0.0.17"},
				UsedIPs:        make(map[string]string),
			},
		},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	pod := &MockPod2
	// Pass SGs in reverse order
	result, err := trunk.AllocateIPFromSharedENI(*pod, []string{"sg-b", "sg-a"})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "10.0.0.16", result.IPV4Addr)
}

func TestAllocateIPFromSharedENI_DuplicatePodUID(t *testing.T) {
	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = make(map[string][]*BranchENIWithPrefix)
	trunk.uidToPrefixAllocation = map[string]*PrefixAllocation{
		PodUID2: {AssignedIP: "10.0.0.1"},
	}

	pod := &MockPod2
	_, err := trunk.AllocateIPFromSharedENI(*pod, []string{"sg-1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestFreePrefixIP(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		ENIDetail: &ENIDetails{ID: "eni-shared-1"},
		FreeIPs:   []string{},
		UsedIPs:   map[string]string{"10.0.0.1": "pod-uid-1", "10.0.0.2": "pod-uid-2"},
	}

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = make(map[string][]*BranchENIWithPrefix)
	trunk.uidToPrefixAllocation = map[string]*PrefixAllocation{
		"pod-uid-1": {BranchENI: sharedENI, AssignedIP: "10.0.0.1"},
		"pod-uid-2": {BranchENI: sharedENI, AssignedIP: "10.0.0.2"},
	}

	trunk.FreePrefixIP("pod-uid-1")

	_, exists := trunk.uidToPrefixAllocation["pod-uid-1"]
	assert.False(t, exists)
	assert.Equal(t, 1, len(sharedENI.UsedIPs))
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))
	assert.Equal(t, "10.0.0.1", sharedENI.CoolingIPs[0].IP)
}

func TestFreePrefixIP_NotFound(t *testing.T) {
	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	// Should not panic
	trunk.FreePrefixIP("non-existent-uid")
}

func TestHasPrefixAllocation(t *testing.T) {
	trunk := getMockTrunk()
	trunk.uidToPrefixAllocation = map[string]*PrefixAllocation{
		"pod-uid-1": {AssignedIP: "10.0.0.1"},
	}

	assert.True(t, trunk.HasPrefixAllocation("pod-uid-1"))
	assert.False(t, trunk.HasPrefixAllocation("pod-uid-2"))
}

func TestReconcile_PrefixAllocations(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		ENIDetail: &ENIDetails{ID: "eni-shared-1"},
		FreeIPs:   []string{},
		UsedIPs:   map[string]string{"10.0.0.1": "uid-active", "10.0.0.2": "uid-leaked"},
	}

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = make(map[string][]*BranchENIWithPrefix)
	trunk.uidToPrefixAllocation = map[string]*PrefixAllocation{
		"uid-active": {BranchENI: sharedENI, AssignedIP: "10.0.0.1"},
		"uid-leaked": {BranchENI: sharedENI, AssignedIP: "10.0.0.2"},
	}

	// Only uid-active is in the current pod set
	pods := []v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{UID: "uid-active"}},
	}

	hasLeaks := trunk.Reconcile(pods)
	assert.True(t, hasLeaks)

	// uid-leaked should be removed
	_, exists := trunk.uidToPrefixAllocation["uid-leaked"]
	assert.False(t, exists)

	// uid-active should remain
	_, exists = trunk.uidToPrefixAllocation["uid-active"]
	assert.True(t, exists)

	// Leaked IP should be in cooling
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))
	assert.Equal(t, "10.0.0.2", sharedENI.CoolingIPs[0].IP)
}

func TestBranchENIWithPrefix_AddPrefix(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		ENIDetail:   &ENIDetails{ID: "eni-123"},
		PrefixCIDRs: []string{"10.0.0.0/28"},
		AllIPs:      []string{"10.0.0.0", "10.0.0.1"},
		FreeIPs:     []string{},
		UsedIPs:     map[string]string{"10.0.0.0": "pod-1", "10.0.0.1": "pod-2"},
	}

	assert.Equal(t, 1, sharedENI.PrefixCount())
	assert.False(t, sharedENI.HasFreeIPs())

	newIPs := []string{"10.0.0.16", "10.0.0.17", "10.0.0.18"}
	sharedENI.AddPrefix("10.0.0.16/28", newIPs)

	assert.Equal(t, 2, sharedENI.PrefixCount())
	assert.Equal(t, []string{"10.0.0.0/28", "10.0.0.16/28"}, sharedENI.PrefixCIDRs)
	assert.True(t, sharedENI.HasFreeIPs())
	assert.Equal(t, 3, len(sharedENI.FreeIPs))
	assert.Equal(t, 5, len(sharedENI.AllIPs))

	// Can allocate from the new prefix
	ip := sharedENI.AllocateIP("pod-3")
	assert.Equal(t, "10.0.0.16", ip)
}

func TestBranchENIWithPrefix_PrefixCount(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		PrefixCIDRs: []string{"10.0.0.0/28", "10.0.0.16/28", "10.0.0.32/28"},
	}
	assert.Equal(t, 3, sharedENI.PrefixCount())
}

// --- canCreateMoreLocked tests ---

func TestCanCreateMoreLocked_WithSharedENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()

	trunk := getMockTrunk()
	trunk.instance = mockInstance
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1": {{ENIDetail: &ENIDetails{ID: "eni-1"}}},
		"sg-2": {{ENIDetail: &ENIDetails{ID: "eni-2"}}, {ENIDetail: &ENIDetails{ID: "eni-3"}}},
	}
	trunk.uidToBranchENIMap = map[string][]*ENIDetails{
		"uid-1": {{ID: "eni-4"}},
	}

	// Total: 3 shared + 1 legacy = 4, which is well below c5.xlarge limit
	assert.True(t, trunk.canCreateMoreLocked())
}

func TestCanCreateMoreLocked_AtCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()

	trunk := getMockTrunk()
	trunk.instance = mockInstance

	// Fill up to instance limit with shared ENIs
	limit := vpc.Limits[InstanceType].BranchInterface
	pool := make([]*BranchENIWithPrefix, limit)
	for i := 0; i < limit; i++ {
		pool[i] = &BranchENIWithPrefix{ENIDetail: &ENIDetails{ID: fmt.Sprintf("eni-%d", i)}}
	}
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{"sg-1": pool}

	assert.False(t, trunk.canCreateMoreLocked())
}

// --- Multiple security group tests ---

func TestAllocateIPFromSharedENI_DifferentSGsGetDifferentENIs(t *testing.T) {
	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-a": {
			{
				ENIDetail: &ENIDetails{
					ID: "eni-for-sg-a", MACAdd: "AA:AA:AA:AA:AA:AA",
					VlanID: 1, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
					AssociationID: "assoc-a", PrefixCIDR: "10.0.0.0/28",
				},
				SecurityGroups: []string{"sg-a"},
				PrefixCIDRs:    []string{"10.0.0.0/28"},
				AllIPs:         []string{"10.0.0.1"},
				FreeIPs:        []string{"10.0.0.1"},
				UsedIPs:        make(map[string]string),
			},
		},
		"sg-b": {
			{
				ENIDetail: &ENIDetails{
					ID: "eni-for-sg-b", MACAdd: "BB:BB:BB:BB:BB:BB",
					VlanID: 2, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
					AssociationID: "assoc-b", PrefixCIDR: "10.0.1.0/28",
				},
				SecurityGroups: []string{"sg-b"},
				PrefixCIDRs:    []string{"10.0.1.0/28"},
				AllIPs:         []string{"10.0.1.1"},
				FreeIPs:        []string{"10.0.1.1"},
				UsedIPs:        make(map[string]string),
			},
		},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	pod := &MockPod2
	result, err := trunk.AllocateIPFromSharedENI(*pod, []string{"sg-b"})

	assert.NoError(t, err)
	assert.Equal(t, "eni-for-sg-b", result.ID)
	assert.Equal(t, "10.0.1.1", result.IPV4Addr)
}

func TestAllocateIPFromSharedENI_EmptySecurityGroupsUsesInstanceSGs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return([]string{"sg-default"})

	trunk := getMockTrunk()
	trunk.instance = mockInstance
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-default": {
			{
				ENIDetail: &ENIDetails{
					ID: "eni-default", MACAdd: "CC:CC:CC:CC:CC:CC",
					VlanID: 1, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
					AssociationID: "assoc-d", PrefixCIDR: "10.0.2.0/28",
				},
				SecurityGroups: []string{"sg-default"},
				PrefixCIDRs:    []string{"10.0.2.0/28"},
				AllIPs:         []string{"10.0.2.1"},
				FreeIPs:        []string{"10.0.2.1"},
				UsedIPs:        make(map[string]string),
			},
		},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	pod := &MockPod2
	result, err := trunk.AllocateIPFromSharedENI(*pod, nil)

	assert.NoError(t, err)
	assert.Equal(t, "eni-default", result.ID)
	assert.Equal(t, "10.0.2.1", result.IPV4Addr)
}

// --- Cooldown processing tests ---

func TestProcessPrefixCoolDowns_MovesIPsBackToFree(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	initCoolDownForTest(ctrl)

	sharedENI := &BranchENIWithPrefix{
		ENIDetail:   &ENIDetails{ID: "eni-1", VlanID: 3},
		PrefixCIDRs: []string{"10.0.0.0/28"},
		FreeIPs:     []string{},
		UsedIPs:     map[string]string{"10.0.0.5": "active-pod"},
		CoolingIPs: []CoolingIP{
			{IP: "10.0.0.1", PodUID: "old-pod-1", DeletionTimestamp: time.Now().Add(-120 * time.Second)},
			{IP: "10.0.0.2", PodUID: "old-pod-2", DeletionTimestamp: time.Now().Add(-120 * time.Second)},
		},
	}

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1": {sharedENI},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	trunk.processPrefixCoolDowns()

	assert.Equal(t, 2, len(sharedENI.FreeIPs))
	assert.Equal(t, 0, len(sharedENI.CoolingIPs))
	// ENI not drained because active-pod still holds an IP
	assert.Equal(t, 1, len(trunk.sgToBranchENIPool["sg-1"]))
}

func TestProcessPrefixCoolDowns_DrainedENIPushedToDeleteQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	initCoolDownForTest(ctrl)

	sharedENI := &BranchENIWithPrefix{
		ENIDetail:   &ENIDetails{ID: "eni-drain", VlanID: 5},
		PrefixCIDRs: []string{"10.0.0.0/28"},
		FreeIPs:     []string{"10.0.0.3", "10.0.0.4"},
		UsedIPs:     make(map[string]string),
		CoolingIPs: []CoolingIP{
			{IP: "10.0.0.1", PodUID: "done-pod", DeletionTimestamp: time.Now().Add(-120 * time.Second)},
		},
	}

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1": {sharedENI},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	trunk.processPrefixCoolDowns()

	// ENI should be removed from pool and pushed to delete queue
	_, exists := trunk.sgToBranchENIPool["sg-1"]
	assert.False(t, exists)
	assert.Equal(t, 1, len(trunk.deleteQueue))
	assert.Equal(t, "eni-drain", trunk.deleteQueue[0].ID)
}

func TestProcessPrefixCoolDowns_NotYetCooledDown(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	initCoolDownForTest(ctrl)

	sharedENI := &BranchENIWithPrefix{
		ENIDetail:   &ENIDetails{ID: "eni-waiting"},
		PrefixCIDRs: []string{"10.0.0.0/28"},
		FreeIPs:     []string{},
		UsedIPs:     make(map[string]string),
		CoolingIPs: []CoolingIP{
			{IP: "10.0.0.1", PodUID: "recent-pod", DeletionTimestamp: time.Now().Add(-5 * time.Second)},
		},
	}

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1": {sharedENI},
	}

	trunk.processPrefixCoolDowns()

	// IP should still be cooling
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))
	assert.Equal(t, 0, len(sharedENI.FreeIPs))
	// ENI should remain in pool
	assert.Equal(t, 1, len(trunk.sgToBranchENIPool["sg-1"]))
}

// --- DeleteAllBranchENIs with shared ENIs ---

func TestDeleteAllBranchENIs_CleansUpSharedENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	// Expect deletion calls for shared ENI
	mockHelper.EXPECT().DisassociateTrunkInterface(gomock.Any()).Return(nil).AnyTimes()
	mockHelper.EXPECT().DeleteNetworkInterface(gomock.Any()).Return(nil).AnyTimes()

	trunk := getMockTrunk()
	trunk.ec2ApiHelper = mockHelper
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1": {{ENIDetail: &ENIDetails{ID: "eni-shared", AssociationID: "assoc-1", VlanID: 3}, PrefixCIDRs: []string{"10.0.0.0/28"}}},
	}
	trunk.uidToPrefixAllocation = map[string]*PrefixAllocation{
		"pod-1": {AssignedIP: "10.0.0.1"},
	}

	trunk.DeleteAllBranchENIs()

	assert.Equal(t, 0, len(trunk.sgToBranchENIPool))
	assert.Equal(t, 0, len(trunk.uidToPrefixAllocation))
}

// --- Full allocation lifecycle test ---

func TestPrefixAllocation_FullLifecycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	initCoolDownForTest(ctrl)

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.sgToBranchENIPool = make(map[string][]*BranchENIWithPrefix)
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	// Set up a shared ENI with 3 IPs
	sharedENI := &BranchENIWithPrefix{
		ENIDetail: &ENIDetails{
			ID: "eni-lifecycle", MACAdd: "11:22:33:44:55:66",
			VlanID: 7, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
			AssociationID: "assoc-lc", PrefixCIDR: "10.0.5.0/28",
		},
		SecurityGroups: []string{"sg-x", "sg-y"},
		PrefixCIDRs:    []string{"10.0.5.0/28"},
		AllIPs:         []string{"10.0.5.1", "10.0.5.2", "10.0.5.3"},
		FreeIPs:        []string{"10.0.5.1", "10.0.5.2", "10.0.5.3"},
		UsedIPs:        make(map[string]string),
	}
	trunk.sgToBranchENIPool["sg-x,sg-y"] = []*BranchENIWithPrefix{sharedENI}

	// Allocate 3 pods
	pods := []v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{UID: "pod-a", Name: "a", Namespace: "ns"}},
		{ObjectMeta: metav1.ObjectMeta{UID: "pod-b", Name: "b", Namespace: "ns"}},
		{ObjectMeta: metav1.ObjectMeta{UID: "pod-c", Name: "c", Namespace: "ns"}},
	}
	for _, pod := range pods {
		result, err := trunk.AllocateIPFromSharedENI(&pod, []string{"sg-y", "sg-x"})
		assert.NoError(t, err)
		assert.NotEmpty(t, result.IPV4Addr)
	}
	assert.Equal(t, 0, len(sharedENI.FreeIPs))
	assert.Equal(t, 3, len(sharedENI.UsedIPs))
	assert.Equal(t, 3, len(trunk.uidToPrefixAllocation))

	// Free one pod
	trunk.FreePrefixIP("pod-b")
	assert.Equal(t, 2, len(sharedENI.UsedIPs))
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))
	assert.Equal(t, 2, len(trunk.uidToPrefixAllocation))
	assert.False(t, trunk.HasPrefixAllocation("pod-b"))

	// Process cooldown (simulate time passed)
	sharedENI.CoolingIPs[0].DeletionTimestamp = time.Now().Add(-120 * time.Second)
	trunk.processPrefixCoolDowns()

	// IP should be back in free pool
	assert.Equal(t, 1, len(sharedENI.FreeIPs))
	assert.Equal(t, 0, len(sharedENI.CoolingIPs))

	// Allocate again from recycled IP
	newPod := v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod-d", Name: "d", Namespace: "ns"}}
	result, err := trunk.AllocateIPFromSharedENI(&newPod, []string{"sg-x", "sg-y"})
	assert.NoError(t, err)
	assert.NotEmpty(t, result.IPV4Addr)
	assert.Equal(t, 3, len(sharedENI.UsedIPs))
}

// --- CanonicalSGKey edge cases ---

func TestCanonicalSGKey_SingleSG(t *testing.T) {
	assert.Equal(t, "sg-only", CanonicalSGKey([]string{"sg-only"}))
}

func TestCanonicalSGKey_DuplicateSGs(t *testing.T) {
	assert.Equal(t, "sg-a,sg-a,sg-b", CanonicalSGKey([]string{"sg-b", "sg-a", "sg-a"}))
}

// --- Multiple allocations exhaust all IPs ---

func TestAllocateIPFromSharedENI_ExhaustsAllIPs_AtMaxPrefixes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockInstance.EXPECT().Type().Return(InstanceType).AnyTimes()
	mockInstance.EXPECT().SubnetID().Return(SubnetId).AnyTimes()
	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return([]string{"sg-1"}).AnyTimes()
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).AnyTimes()
	mockInstance.EXPECT().SubnetV6CidrBlock().Return(SubnetV6CidrBlock).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(InstanceId).AnyTimes()
	mockInstance.EXPECT().GetConnectionTrackingSpec().Return(nil, nil, nil).AnyTimes()

	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	// CreateNetworkInterface will be called when trying to create a new ENI
	mockHelper.EXPECT().CreateNetworkInterface(gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("simulated failure")).AnyTimes()

	// c5.xlarge has IPv4PerInterface=15, so maxPrefixesPerENI = 15
	// Fill PrefixCIDRs to already be at max (15 prefixes)
	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.instance = mockInstance
	trunk.ec2ApiHelper = mockHelper

	maxPrefixes := make([]string, 15)
	for i := range maxPrefixes {
		maxPrefixes[i] = fmt.Sprintf("10.0.%d.0/28", i)
	}

	trunk.sgToBranchENIPool = map[string][]*BranchENIWithPrefix{
		"sg-1": {
			{
				ENIDetail: &ENIDetails{
					ID: "eni-full", MACAdd: "AA:BB:CC:DD:EE:FF",
					VlanID: 1, SubnetCIDR: SubnetCidrBlock, SubnetV6CIDR: SubnetV6CidrBlock,
					AssociationID: "assoc-f", PrefixCIDR: "10.0.0.0/28",
				},
				SecurityGroups: []string{"sg-1"},
				PrefixCIDRs:    maxPrefixes, // already at max (15 prefixes for c5.xlarge)
				AllIPs:         []string{"10.0.0.1", "10.0.0.2"},
				FreeIPs:        []string{"10.0.0.1", "10.0.0.2"},
				UsedIPs:        make(map[string]string),
			},
		},
	}
	trunk.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	// Allocate both IPs
	pod1 := v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "p1", Name: "p1", Namespace: "ns"}}
	pod2 := v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "p2", Name: "p2", Namespace: "ns"}}

	r1, err := trunk.AllocateIPFromSharedENI(&pod1, []string{"sg-1"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1", r1.IPV4Addr)

	r2, err := trunk.AllocateIPFromSharedENI(&pod2, []string{"sg-1"})
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.2", r2.IPV4Addr)

	// Next allocation: ENI is at max prefixes, so it tries to create a new ENI
	// which will fail since no ec2ApiHelper is configured
	pod3 := v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "p3", Name: "p3", Namespace: "ns"}}
	_, err = trunk.AllocateIPFromSharedENI(&pod3, []string{"sg-1"})
	assert.Error(t, err)
}

// --- Reconcile with mixed legacy and prefix pods ---

func TestReconcile_MixedLegacyAndPrefixPods(t *testing.T) {
	sharedENI := &BranchENIWithPrefix{
		ENIDetail: &ENIDetails{ID: "eni-shared"},
		FreeIPs:   []string{},
		UsedIPs:   map[string]string{"10.0.0.1": "prefix-active", "10.0.0.2": "prefix-leaked"},
	}

	trunk := getMockTrunk()
	trunk.prefixDelegationEnabled = true
	trunk.uidToBranchENIMap = map[string][]*ENIDetails{
		"legacy-active": {{ID: "eni-legacy-1"}},
		"legacy-leaked": {{ID: "eni-legacy-2", VlanID: 3}},
	}
	trunk.sgToBranchENIPool = make(map[string][]*BranchENIWithPrefix)
	trunk.uidToPrefixAllocation = map[string]*PrefixAllocation{
		"prefix-active": {BranchENI: sharedENI, AssignedIP: "10.0.0.1"},
		"prefix-leaked": {BranchENI: sharedENI, AssignedIP: "10.0.0.2"},
	}

	// Only active pods are present
	pods := []v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{UID: "legacy-active"}},
		{ObjectMeta: metav1.ObjectMeta{UID: "prefix-active"}},
	}

	hasLeaks := trunk.Reconcile(pods)
	assert.True(t, hasLeaks)

	// Legacy leaked should be in delete queue
	assert.Equal(t, 1, len(trunk.deleteQueue))
	assert.Equal(t, "eni-legacy-2", trunk.deleteQueue[0].ID)
	_, exists := trunk.uidToBranchENIMap["legacy-leaked"]
	assert.False(t, exists)

	// Prefix leaked should be in cooling
	_, exists = trunk.uidToPrefixAllocation["prefix-leaked"]
	assert.False(t, exists)
	assert.Equal(t, 1, len(sharedENI.CoolingIPs))

	// Active ones should remain
	_, exists = trunk.uidToBranchENIMap["legacy-active"]
	assert.True(t, exists)
	_, exists = trunk.uidToPrefixAllocation["prefix-active"]
	assert.True(t, exists)
}
