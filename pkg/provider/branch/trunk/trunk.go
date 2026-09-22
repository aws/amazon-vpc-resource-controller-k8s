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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	ec2Errors "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/errors"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/samber/lo"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsEc2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// MaxAllocatableVlanIds is the maximum number of Vlan Ids that can be allocated per trunk.
	MaxAllocatableVlanIds = 121
	// MaxDeleteRetries is the maximum number of times the ENI will be retried before being removed from the delete queue
	MaxDeleteRetries    = 3
	SubnetLabel         = "subnet"
	SecurityGroupsLabel = "security_groups"
)

var (
	InterfaceTypeTrunk   = "trunk"
	TrunkEniDescription  = "trunk-eni"
	BranchEniDescription = "branch-eni"
)

var ErrCurrentlyAtMaxCapacity = fmt.Errorf("cannot create more branches at this point as used branches plus the " +
	"delete queue is at max capacity")

var (
	trunkENIOperationsErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trunk_eni_operations_err_count",
			Help: "The number of errors encountered for operations on Trunk ENI",
		},
		[]string{"operation"},
	)
	unreconciledTrunkENICount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unreconciled_trunk_network_interfaces",
			Help: "The number of unreconciled trunk network interfaces",
		},
		[]string{"attribute"},
	)
	branchENIOperationsSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_eni_opeartions_success_count",
			Help: "The number of branch ENI succeeded operations",
		},
		[]string{"operation"},
	)
	branchENIOperationsFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_eni_opeartions_failure_count",
			Help: "The number of branch ENI failed operations",
		},
		[]string{"operation"},
	)
	trunkReinitCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trunk_reinit_total",
			Help: "The number of trunk re-initializations by path (restored|ec2|recovered) and result",
		},
		[]string{"path", "result"},
	)
	prometheusRegisterOnce sync.Once
)

type TrunkENI interface {
	// InitTrunk initializes trunk interface
	InitTrunk(instance ec2.EC2Instance, pods []v1.Pod) error
	// CreateAndAssociateBranchENIs allocates ENIs and commits Pod ownership.
	CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error)
	// PushBranchENIsToCoolDownQueue pushes the branch interface belonging to the pod to the cool down queue
	PushBranchENIsToCoolDownQueue(UID string)
	// DeleteCooledDownENIs deletes the interfaces that have been sitting in the queue for cool down period
	DeleteCooledDownENIs()
	// Reconcile compares the cache state with the list of pods to identify events that were missed and clean up the dangling interfaces
	Reconcile(pods []v1.Pod) bool
	// RecoverBranchState verifies a restored trunk before allocation.
	RecoverBranchState(listPods func() ([]v1.Pod, error)) error
	// PushENIsToFrontOfDeleteQueue pushes the eni network interfaces to the front of the delete queue
	PushENIsToFrontOfDeleteQueue(*v1.Pod, []*ENIDetails)
	// InitFromNodeNetworkState restores trunk state without EC2.
	InitFromNodeNetworkState(trunkENIID string, pods []v1.Pod) error
	// TrunkENIID returns the trunk ENI ID.
	TrunkENIID() string
	// TrunkSubnetID returns the subnet observed on the trunk ENI.
	TrunkSubnetID() string
	// Introspect returns the state of the Trunk ENI
	Introspect() IntrospectResponse
}

// trunkENI is the first trunk network interface of an instance
type trunkENI struct {
	// Log is the logger with the instance details
	log logr.Logger
	// lock is used to perform concurrent operation on the shared variables like the list of used vlan ids
	lock sync.RWMutex
	// ec2ApiHelper is the wrapper interface that provides EC2 API helper functions
	ec2ApiHelper api.EC2APIHelper
	// trunkENIId is the interface id of the trunk network interface
	trunkENIId string
	// trunkSubnetID is the subnet observed on the trunk network interface.
	trunkSubnetID string
	// instance is the pointer to the instance details
	instance ec2.EC2Instance
	// usedVlanIds is the list of boolean value representing the used vlan ids
	usedVlanIds []bool
	// branchENIs is the list of BranchENIs associated with the trunk
	uidToBranchENIMap map[string][]*ENIDetails
	// deleteQueue is the queue of ENIs that are being cooled down before being deleted
	deleteQueue []*ENIDetails
	// pendingCreate reserves capacity for in-flight allocations.
	pendingCreate int
	// nodeName tag is the tag added to trunk and branch ENIs created on the node
	nodeIDTag []ec2types.Tag
	// branchStateGate serializes verification with branch-state mutations.
	branchStateGate sync.RWMutex
	// branchStateVerified gates allocation on restored trunks.
	branchStateVerified bool
}

// getConnectionTrackingSpec builds a ConnectionTrackingSpecificationRequest from the
// primary ENI's cached settings. Returns nil if no settings are configured.
func (t *trunkENI) getConnectionTrackingSpec() *ec2types.ConnectionTrackingSpecificationRequest {
	instanceId := t.instance.InstanceID()
	tcpEstablishedTimeout, udpStreamTimeout, udpTimeout := t.instance.GetConnectionTrackingSpec()

	if tcpEstablishedTimeout != nil || udpStreamTimeout != nil || udpTimeout != nil {
		t.log.Info("using connection tracking settings from primary ENI",
			"instanceID", instanceId,
			"tcpEstablishedTimeout", tcpEstablishedTimeout,
			"udpStreamTimeout", udpStreamTimeout,
			"udpTimeout", udpTimeout)
		return &ec2types.ConnectionTrackingSpecificationRequest{
			TcpEstablishedTimeout: tcpEstablishedTimeout,
			UdpStreamTimeout:      udpStreamTimeout,
			UdpTimeout:            udpTimeout,
		}
	}
	return nil
}

// PodENI is a json convertible structure that stores the Branch ENI details that can be
// used by the CNI plugin or the component consuming the resource
type ENIDetails struct {
	// BranchENId is the network interface id of the branch interface
	ID string `json:"eniId"`
	// MacAdd is the MAC address of the network interface
	MACAdd string `json:"ifAddress"`
	// IPv4 and/or IPv6 address assigned to the branch Network interface
	IPV4Addr string `json:"privateIp"`
	IPV6Addr string `json:"ipv6Addr"`
	// VlanId is the VlanId of the branch network interface
	VlanID int `json:"vlanId"`
	// SubnetCIDR is the CIDR block of the subnet
	SubnetCIDR   string `json:"subnetCidr"`
	SubnetV6CIDR string `json:"subnetV6Cidr"`
	// deletionTimeStamp is the time when the pod was marked deleted.
	deletionTimeStamp time.Time
	// deleteRetryCount is the
	deleteRetryCount int
	// ID of association between branch and trunk ENI
	AssociationID string `json:"associationID"`
}

type IntrospectResponse struct {
	TrunkENIID     string
	InstanceID     string
	PodToBranchENI map[string][]ENIDetails
	DeleteQueue    []ENIDetails
}

type IntrospectSummaryResponse struct {
	TrunkENIID     string
	InstanceID     string
	BranchENICount int
	DeleteQueueLen int
}

// NewTrunkENI returns a new Trunk ENI interface.
func NewTrunkENI(logger logr.Logger, instance ec2.EC2Instance, helper api.EC2APIHelper) TrunkENI {
	availVlans := make([]bool, MaxAllocatableVlanIds)
	// VlanID 0 cannot be assigned.
	availVlans[0] = true

	return &trunkENI{
		log:               logger,
		usedVlanIds:       availVlans,
		ec2ApiHelper:      helper,
		instance:          instance,
		uidToBranchENIMap: make(map[string][]*ENIDetails),
		nodeIDTag: []ec2types.Tag{
			{
				Key:   aws.String(config.NetworkInterfaceNodeIDKey),
				Value: aws.String(instance.InstanceID()),
			},
		},
	}
}

func PrometheusRegister() {
	prometheusRegisterOnce.Do(func() {
		metrics.Registry.MustRegister(trunkENIOperationsErrCount)
		metrics.Registry.MustRegister(unreconciledTrunkENICount)
		metrics.Registry.MustRegister(branchENIOperationsSuccessCount)
		metrics.Registry.MustRegister(branchENIOperationsFailureCount)
		metrics.Registry.MustRegister(trunkReinitCount)
	})
}

// InitTrunk initializes the trunk and its associated branch network interfaces
// from EC2.
func (t *trunkENI) InitTrunk(instance ec2.EC2Instance, podList []v1.Pod) error {
	instanceID := t.instance.InstanceID()
	log := t.log.WithValues("request", "initialize", "instance ID", instanceID)

	nwInterfaces, err := t.ec2ApiHelper.GetInstanceNetworkInterface(&instanceID)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("describe_instance_nw_interface").Inc()
		return err
	}

	var trunk ec2types.InstanceNetworkInterface
	// Get trunk network interface
	for _, nwInterface := range nwInterfaces {
		// It's possible to get an empty network interface response if the instance is being deleted.
		if nwInterface.InterfaceType == nil {
			return fmt.Errorf("received an empty network interface response "+
				"from EC2 %+v", nwInterface)
		}
		if *nwInterface.InterfaceType == "trunk" {
			// Check that the trunkENI is in attached state before adding to cache
			if err = t.ec2ApiHelper.WaitForNetworkInterfaceStatusChange(nwInterface.NetworkInterfaceId, string(ec2types.AttachmentStatusAttached)); err == nil {
				t.trunkENIId = *nwInterface.NetworkInterfaceId
				t.trunkSubnetID = lo.FromPtr(nwInterface.SubnetId)
			} else {
				return fmt.Errorf("failed to verify network interface status attached for %v", *nwInterface.NetworkInterfaceId)
			}
			trunk = nwInterface
		}
	}

	// Trunk interface doesn't exists, try to create a new trunk interface
	if t.trunkENIId == "" {
		freeIndex, err := instance.GetHighestUnusedDeviceIndex()
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("find_free_index").Inc()
			log.Error(err, "failed to find free device index")
			return err
		}
		// Trunk ENI doesn't need to have security group timeout as applied on primary ENI or branch ENIs as it is not a endpoint used in connection
		trunk, err := t.ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceID, aws.String(t.instance.SubnetID()),
			t.instance.CurrentInstanceSecurityGroups(), t.nodeIDTag, &freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, nil, nil)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("create_trunk_eni").Inc()
			return err
		}

		t.trunkENIId = *trunk.NetworkInterfaceId
		t.trunkSubnetID = lo.FromPtr(trunk.SubnetId)
		t.branchStateVerified = true
		log.Info("created a new trunk interface", "trunk id", t.trunkENIId)

		trunkReinitCount.WithLabelValues("ec2", "success").Inc()
		return nil
	}

	// the node already have trunk, let's check if its SGs and Subnets match with expected
	expectedSubnetID, expectedSecurityGroups := t.instance.GetCustomNetworkingSpec()
	if len(expectedSecurityGroups) > 0 || expectedSubnetID != "" {
		slices.Sort(expectedSecurityGroups)
		trunkSGs := lo.Map(trunk.Groups, func(g ec2types.GroupIdentifier, _ int) string {
			return lo.FromPtr(g.GroupId)
		})
		slices.Sort(trunkSGs)

		mismatchedSubnets := expectedSubnetID != lo.FromPtr(trunk.SubnetId)
		mismatchedSGs := !slices.Equal(expectedSecurityGroups, trunkSGs)

		extraSGsInTrunk, missingSGsInTrunk := lo.Difference(trunkSGs, expectedSecurityGroups)
		t.log.Info("Observed trunk ENI config",
			"instanceID", t.instance.InstanceID(),
			"trunkENIID", lo.FromPtr(trunk.NetworkInterfaceId),
			"configuredTrunkSGs", trunkSGs,
			"configuredTrunkSubnet", lo.FromPtr(trunk.SubnetId),
			"desiredTrunkSGs", expectedSecurityGroups,
			"desiredTrunkSubnet", expectedSubnetID,
			"mismatchedSGs", mismatchedSGs,
			"mismatchedSubnets", mismatchedSubnets,
			"missingSGs", missingSGsInTrunk,
			"extraSGs", extraSGsInTrunk,
		)

		if mismatchedSGs {
			unreconciledTrunkENICount.WithLabelValues(SecurityGroupsLabel).Inc()
		}

		if mismatchedSubnets {
			unreconciledTrunkENICount.WithLabelValues(SubnetLabel).Inc()
		}
	}

	// Get the list of branch ENIs
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&t.trunkENIId, nil)
	if err != nil {
		return err
	}

	// Convert the list of interfaces to a set
	associatedBranchInterfaces := make(map[string]*ec2types.NetworkInterface)
	for _, branchInterface := range branchInterfaces {
		associatedBranchInterfaces[*branchInterface.NetworkInterfaceId] = branchInterface
	}

	// From the list of pods on the given node, and the branch ENIs from EC2 API call rebuild the internal cache
	for _, pod := range podList {
		pod := pod // Fix gosec G601, so we can use &node
		eniListFromPod, usable := t.decodeBranchInterfacesUsedByPod(&pod)
		if !usable {
			trunkENIOperationsErrCount.WithLabelValues("unusable_pod_eni_annotation").Inc()
			continue
		}
		if len(eniListFromPod) == 0 {
			continue
		}
		uid := string(pod.UID)
		var branchENIs []*ENIDetails
		for _, eni := range eniListFromPod {
			branchInterface, isPresent := associatedBranchInterfaces[eni.ID]
			if !isPresent {
				t.log.Error(fmt.Errorf("eni allocated to pod not found in ec2"), "eni not found", "eni", eni)
				trunkENIOperationsErrCount.WithLabelValues("get_branch_eni_from_ec2").Inc()
				continue
			}

			vlanID, err := t.getVlanIdFromTag(branchInterface.TagSet)
			if err != nil {
				trunkENIOperationsErrCount.WithLabelValues("get_vlan_from_tag").Inc()
				log.Error(err, "failed to find vlan id", "interface", *branchInterface.NetworkInterfaceId)
				continue
			}
			t.markVlanAssigned(vlanID)

			recovered := *eni
			recovered.VlanID = vlanID
			branchENIs = append(branchENIs, &recovered)
			delete(associatedBranchInterfaces, eni.ID)
		}
		t.uidToBranchENIMap[uid] = branchENIs
	}

	for _, branchInterface := range associatedBranchInterfaces {
		t.log.Info("pushing eni to delete queue as no pod owns it", "eni",
			*branchInterface.NetworkInterfaceId)

		vlanId, err := t.getVlanIdFromTag(branchInterface.TagSet)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("get_vlan_from_tag").Inc()
			log.Error(err, "failed to find vlan id", "interface", *branchInterface.NetworkInterfaceId)
			continue
		}

		// Reserve the VLAN until the queued ENI is deleted.
		t.markVlanAssigned(vlanId)
		t.pushENIToDeleteQueue(&ENIDetails{
			ID:                *branchInterface.NetworkInterfaceId,
			VlanID:            vlanId,
			deletionTimeStamp: time.Now(),
		})
	}

	log.V(1).Info("successfully initialized trunk with all associated branch interfaces",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)

	t.branchStateVerified = true
	trunkReinitCount.WithLabelValues("ec2", "success").Inc()
	return nil
}

// InitFromNodeNetworkState restores and validates annotation state without EC2.
func (t *trunkENI) InitFromNodeNetworkState(trunkENIID string, podList []v1.Pod) error {
	t.trunkENIId = trunkENIID
	t.branchStateVerified = false

	candidateMap := make(map[string][]*ENIDetails)

	for _, pod := range podList {
		pod := pod
		uid := string(pod.UID)
		eniList, usable := t.decodeBranchInterfacesUsedByPod(&pod)
		if !usable {
			trunkENIOperationsErrCount.WithLabelValues("unusable_pod_eni_annotation").Inc()
			continue
		}
		if len(eniList) == 0 {
			continue
		}
		var validENIs []*ENIDetails
		for _, eni := range eniList {
			if err := validateVlanID(eni.VlanID); err != nil {
				trunkENIOperationsErrCount.WithLabelValues("branch_eni_unusable_vlan").Inc()
				t.log.Error(err,
					"ignoring branch eni from pod annotation",
					"eni", eni.ID, "vlanID", eni.VlanID, "pod", uid)
				continue
			}
			validENIs = append(validENIs, eni)
		}
		if len(validENIs) != 0 {
			candidateMap[uid] = validENIs
		}
	}

	t.lock.Lock()
	t.uidToBranchENIMap = candidateMap
	for _, enis := range candidateMap {
		for _, eni := range enis {
			t.usedVlanIds[eni.VlanID] = true
		}
	}
	t.lock.Unlock()

	trunkReinitCount.WithLabelValues("restored", "success").Inc()
	t.log.Info("restored trunk ledger from NodeNetworkState",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)
	return nil
}

// RecoverBranchState verifies a restored trunk once before allocation.
func (t *trunkENI) RecoverBranchState(listPods func() ([]v1.Pod, error)) error {
	t.branchStateGate.RLock()
	verified := t.branchStateVerified
	t.branchStateGate.RUnlock()
	if verified {
		return nil
	}

	t.branchStateGate.Lock()
	defer t.branchStateGate.Unlock()

	if t.branchStateVerified {
		return nil
	}

	pods, err := listPods()
	if err != nil {
		return fmt.Errorf("listing pods before branch state recovery: %w", err)
	}
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&t.trunkENIId, nil)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("recover_branch_state").Inc()
		return fmt.Errorf("describing branch ENIs before allocation: %w", err)
	}

	state, err := t.buildRecoveredBranchState(pods, branchInterfaces)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("recover_branch_state").Inc()
		return err
	}

	t.lock.Lock()
	t.uidToBranchENIMap = state.uidToBranchENIMap
	t.deleteQueue = state.deleteQueue
	t.usedVlanIds = state.usedVlanIDs
	t.pendingCreate = 0
	t.lock.Unlock()
	t.branchStateVerified = true

	trunkReinitCount.WithLabelValues("recovered", "success").Inc()
	t.log.Info("recovered branch state from EC2 before allocation",
		"trunk", t.trunkENIId,
		"ownedPods", len(state.uidToBranchENIMap),
		"unownedENIs", len(state.deleteQueue))
	return nil
}

type recoveredBranchState struct {
	uidToBranchENIMap map[string][]*ENIDetails
	deleteQueue       []*ENIDetails
	usedVlanIDs       []bool
}

func (t *trunkENI) buildRecoveredBranchState(pods []v1.Pod,
	branchInterfaces []*ec2types.NetworkInterface,
) (*recoveredBranchState, error) {
	state := newRecoveredBranchState()
	branchByID, vlanByENIID, err := t.reserveRecoveredBranchInterfaces(state, branchInterfaces)
	if err != nil {
		return nil, err
	}

	t.recoverPodOwnership(state, pods, branchByID, vlanByENIID)

	for id := range branchByID {
		state.deleteQueue = append(state.deleteQueue, &ENIDetails{
			ID:                id,
			VlanID:            vlanByENIID[id],
			deletionTimeStamp: time.Now(),
		})
	}
	return state, nil
}

func newRecoveredBranchState() *recoveredBranchState {
	state := &recoveredBranchState{
		uidToBranchENIMap: make(map[string][]*ENIDetails),
		usedVlanIDs:       make([]bool, MaxAllocatableVlanIds),
	}
	state.usedVlanIDs[0] = true
	return state
}

func (t *trunkENI) reserveRecoveredBranchInterfaces(state *recoveredBranchState,
	branchInterfaces []*ec2types.NetworkInterface,
) (map[string]*ec2types.NetworkInterface, map[string]int, error) {
	branchByID := make(map[string]*ec2types.NetworkInterface, len(branchInterfaces))
	vlanByENIID := make(map[string]int, len(branchInterfaces))
	for _, branchInterface := range branchInterfaces {
		id := *branchInterface.NetworkInterfaceId
		vlanID, err := t.getVlanIdFromTag(branchInterface.TagSet)
		if err != nil {
			return nil, nil, fmt.Errorf("branch ENI %s has an unusable VLAN tag: %w", id, err)
		}
		branchByID[id] = branchInterface
		vlanByENIID[id] = vlanID
		state.usedVlanIDs[vlanID] = true
	}
	return branchByID, vlanByENIID, nil
}

func (t *trunkENI) recoverPodOwnership(state *recoveredBranchState, pods []v1.Pod,
	branchByID map[string]*ec2types.NetworkInterface, vlanByENIID map[string]int,
) {
	for i := range pods {
		eniList, usable := t.decodeBranchInterfacesUsedByPod(&pods[i])
		if !usable {
			trunkENIOperationsErrCount.WithLabelValues("unusable_pod_eni_annotation").Inc()
			continue
		}
		uid := string(pods[i].UID)
		for _, eni := range eniList {
			if _, found := branchByID[eni.ID]; !found {
				continue
			}

			recovered := *eni
			recovered.VlanID = vlanByENIID[eni.ID]
			state.uidToBranchENIMap[uid] = append(state.uidToBranchENIMap[uid], &recovered)
			delete(branchByID, eni.ID)
		}
	}
}

// TrunkENIID returns the trunk ENI ID.
func (t *trunkENI) TrunkENIID() string {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.trunkENIId
}

// TrunkSubnetID returns the subnet observed on the trunk ENI.
func (t *trunkENI) TrunkSubnetID() string {
	t.lock.RLock()
	trunkSubnetID := t.trunkSubnetID
	t.lock.RUnlock()
	if trunkSubnetID != "" {
		return trunkSubnetID
	}
	// Restored trunks use the checkpointed instance subnet.
	return t.instance.SubnetID()
}

// Reconcile reconciles the state from the API Server to the internal cache of EC2 Branch Interfaces, if the controller
// missed some delete events the reconcile method will perform cleanup for the dangling interfaces
func (t *trunkENI) Reconcile(pods []v1.Pod) bool {
	t.branchStateGate.RLock()
	defer t.branchStateGate.RUnlock()

	// Perform under lock to block new pods being added/removed concurrently
	t.lock.Lock()
	defer t.lock.Unlock()

	currentPodSet := make(map[string]struct{})
	var isPresent struct{}
	for _, pod := range pods {
		currentPodSet[string(pod.UID)] = isPresent
	}

	leakedENIs := 0
	for uid, branchENIs := range t.uidToBranchENIMap {
		_, exists := currentPodSet[uid]
		if !exists {
			leakedENIs += 1
			branchENIOperationsSuccessCount.WithLabelValues("leaked_branch_enis").Inc()
			for _, eni := range branchENIs {
				// Pod could have been deleted recently, set the timestamp to current time as controller is not aware of the actual time.
				eni.deletionTimeStamp = time.Now()
				t.deleteQueue = append(t.deleteQueue, eni)
			}
			delete(t.uidToBranchENIMap, uid)
			t.log.Info("leaked eni pushed to delete queue, deleted non-existing pod", "pod uid", uid, "eni", branchENIs)
		}
	}

	return leakedENIs > 0
}

// CreateAndAssociateBranchENIs allocates ENIs and commits Pod ownership.
func (t *trunkENI) CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error) {
	t.branchStateGate.RLock()
	defer t.branchStateGate.RUnlock()

	newENIs, err := t.createAndAssociateBranchENIs(pod, securityGroups, eniCount)
	if err != nil {
		return nil, err
	}

	t.commitReservedBranchENIsToLedger(string(pod.UID), newENIs, eniCount)
	return newENIs, nil
}

// createAndAssociateBranchENIs queues partially created ENIs on failure.
func (t *trunkENI) createAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error) {
	log := t.log.WithValues("request", "create", "pod namespace", pod.Namespace, "pod name", pod.Name)

	branchENI, isPresent := t.getBranchFromCache(string(pod.UID))
	if isPresent {
		// Possible when older pod with same namespace and name is still being deleted
		return nil, fmt.Errorf("cannot create new eni entry already exist, older entry : %v", branchENI)
	}

	if !t.canCreateMore(eniCount) {
		return nil, ErrCurrentlyAtMaxCapacity
	}

	// If the security group is empty use the instance security group
	if securityGroups == nil || len(securityGroups) == 0 {
		securityGroups = t.instance.CurrentInstanceSecurityGroups()
	}

	connectionTrackingSpec := t.getConnectionTrackingSpec()

	var newENIs []*ENIDetails
	var err error
	var nwInterface *ec2types.NetworkInterface
	var vlanID int

	for i := 0; i < eniCount; i++ {
		// Assign VLAN
		vlanID, err = t.assignVlanId()
		if err != nil {
			err = fmt.Errorf("assigning vlad id, %w", err)
			trunkENIOperationsErrCount.WithLabelValues("assign_vlan_id").Inc()
			break
		}

		// Vlan ID tag workaround, as describe trunk association is not supported with assumed role
		tags := []ec2types.Tag{
			{
				Key:   aws.String(config.VLandIDTag),
				Value: aws.String(strconv.Itoa(vlanID)),
			},
			{
				Key:   aws.String(config.TrunkENIIDTag),
				Value: &t.trunkENIId,
			},
		}
		// append the nodeName tag to add to branch ENIs
		tags = append(tags, t.nodeIDTag...)
		// Create Branch ENI
		nwInterface, err = t.ec2ApiHelper.CreateNetworkInterface(&BranchEniDescription,
			aws.String(t.instance.SubnetID()), securityGroups, tags, nil, nil, connectionTrackingSpec)
		if err != nil {
			err = fmt.Errorf("creating network interface, %w", err)
			t.freeVlanId(vlanID)
			branchENIOperationsFailureCount.WithLabelValues("creating_branch_eni_failed").Inc()
			break
		}
		branchENIOperationsSuccessCount.WithLabelValues("created_branch_eni_succeeded").Inc()

		newENI := t.newBranchENIDetails(nwInterface, vlanID)
		newENIs = append(newENIs, newENI)

		// Associate Branch to trunk
		var associationOutput *awsEc2.AssociateTrunkInterfaceOutput
		associationOutput, err = t.ec2ApiHelper.AssociateBranchToTrunk(&t.trunkENIId, nwInterface.NetworkInterfaceId, vlanID)
		if err != nil {
			err = fmt.Errorf("associating branch to trunk, %w", err)
			trunkENIOperationsErrCount.WithLabelValues("associate_branch").Inc()
			break
		}
		newENI.AssociationID = *associationOutput.InterfaceAssociation.AssociationId
	}

	if err != nil {
		log.Error(err, "failed to create ENI, moving created ENIs to the delete queue")
		t.moveReservedBranchENIsToDeleteQueue(eniCount, newENIs)
		return nil, err
	}

	log.Info("successfully created branch interfaces", "interfaces", newENIs,
		"security group used", securityGroups)

	return newENIs, nil
}

func (t *trunkENI) newBranchENIDetails(nwInterface *ec2types.NetworkInterface, vlanID int) *ENIDetails {
	// Branch ENI can have an IPv4 address, IPv6 address, or both.
	var v4Addr, v6Addr string
	if nwInterface.PrivateIpAddress != nil {
		v4Addr = *nwInterface.PrivateIpAddress
	}
	if nwInterface.Ipv6Address != nil {
		v6Addr = *nwInterface.Ipv6Address
	}
	return &ENIDetails{
		ID: *nwInterface.NetworkInterfaceId, MACAdd: *nwInterface.MacAddress,
		IPV4Addr: v4Addr, IPV6Addr: v6Addr, SubnetCIDR: t.instance.SubnetCidrBlock(),
		SubnetV6CIDR: t.instance.SubnetV6CidrBlock(), VlanID: vlanID,
	}
}

// DeleteBranchNetworkInterface deletes the branch network interface and returns an error in case of failure to delete
func (t *trunkENI) PushBranchENIsToCoolDownQueue(UID string) {
	t.branchStateGate.RLock()
	defer t.branchStateGate.RUnlock()

	// Lock is required as Reconciler is also performing operation concurrently
	t.lock.Lock()
	defer t.lock.Unlock()

	branchENIs, isPresent := t.uidToBranchENIMap[UID]
	if !isPresent {
		t.log.Info("couldn't find Branch ENI in cache, it could have been released if pod"+
			"succeeded/failed before being deleted", "UID", UID)
		trunkENIOperationsErrCount.WithLabelValues("get_branch_from_cache").Inc()
		return
	}

	for _, eni := range branchENIs {
		eni.deletionTimeStamp = time.Now()
		t.deleteQueue = append(t.deleteQueue, eni)
	}

	delete(t.uidToBranchENIMap, UID)

	t.log.Info("moved branch network interfaces to delete queue", "Interfaces",
		branchENIs, "UID", UID)
}

func (t *trunkENI) DeleteCooledDownENIs() {
	t.branchStateGate.RLock()
	defer t.branchStateGate.RUnlock()

	for eni, hasENI := t.peekENIFromDeleteQueue(); hasENI; eni, hasENI = t.peekENIFromDeleteQueue() {
		if eni.deletionTimeStamp.IsZero() ||
			time.Now().After(eni.deletionTimeStamp.Add(cooldown.GetCoolDown().GetCoolDownPeriod())) {
			err := t.deleteENI(eni)
			if err != nil {
				retryCount, present := t.incrementDeleteRetry(eni)
				if !present {
					continue
				}
				if retryCount >= MaxDeleteRetries {
					t.log.Error(err, "forgetting eni as max retries exceeded", "eni", eni)
					// TODO: free vlan id?
					t.removeENIFromDeleteQueue(eni, false)
					continue
				}
				t.log.Error(err, "failed to delete eni, will retry", "eni", eni)
				t.moveENIToFrontOfDeleteQueue(eni)
				continue
			}
			t.removeENIFromDeleteQueue(eni, true)
			t.log.V(1).Info("deleted eni successfully", "eni", eni, "deletion time", time.Now(),
				"pushed to queue time", eni.deletionTimeStamp)
		} else {
			// Since the current item is not cooled down so the items added after it would not be cooled down either
			return
		}
	}
}

// deleteENI deletes the ENI; the caller updates queue and VLAN state.
func (t *trunkENI) deleteENI(eniDetail *ENIDetails) (err error) {
	// Disassociate branch ENI from trunk if association ID exists and delete branch network interface
	if eniDetail.AssociationID != "" {
		err = t.ec2ApiHelper.DisassociateTrunkInterface(&eniDetail.AssociationID)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("disassociate_trunk_error").Inc()
			if !strings.Contains(err.Error(), ec2Errors.NotFoundAssociationID) {
				t.log.Error(err, "failed to disassociate branch ENI from trunk, will try to delete the branch ENI")
				// Not returning error here, fallback to force branch ENI deletion
			} else {
				t.log.Info("AssociationID not found when disassociating branch from trunk ENI, it is already disassociated so delete the branch ENI")
			}
		}
	}
	err = t.ec2ApiHelper.DeleteNetworkInterface(&eniDetail.ID)
	if err != nil {
		branchENIOperationsFailureCount.WithLabelValues("delete_branch_error").Inc()

		if !strings.Contains(err.Error(), ec2Errors.NotFoundInterfaceID) {
			t.log.Error(err, "calling EC2 delete API to delete the branch ENI failed", "BranchENI", eniDetail)
			return err
		} else {
			t.log.Info("The branch ENI was not found by EC2. Will not call EC2 for deletion again", "BranchENI", eniDetail, "Error", err.Error())
		}
	}

	branchENIOperationsSuccessCount.WithLabelValues("deleted_branch_succesfully").Inc()

	t.log.Info("deleted eni", "eni details", eniDetail)

	return nil
}

func (t *trunkENI) getBranchInterfaceMap(eniList []*ENIDetails) map[string]*ENIDetails {
	eniMap := make(map[string]*ENIDetails)
	for _, eni := range eniList {
		eniMap[eni.ID] = eni
	}
	return eniMap
}

// decodeBranchInterfacesUsedByPod returns false for an untrusted present annotation.
func (t *trunkENI) decodeBranchInterfacesUsedByPod(pod *v1.Pod) (eniDetails []*ENIDetails, usable bool) {
	branchAnnotation, isPresent := pod.Annotations[config.ResourceNamePodENI]
	if !isPresent {
		return nil, true
	}

	if err := json.Unmarshal([]byte(branchAnnotation), &eniDetails); err != nil {
		t.log.Error(err, "failed to unmarshal resource annotation", "annotation", branchAnnotation)
		return nil, false
	}
	for _, eni := range eniDetails {
		if eni == nil || eni.ID == "" {
			t.log.Error(fmt.Errorf("branch eni annotation entry has no eni id"),
				"unusable resource annotation", "annotation", branchAnnotation)
			return nil, false
		}
	}
	return eniDetails, true
}

// pushENIToDeleteQueue pushes an ENI to a delete queue
func (t *trunkENI) pushENIToDeleteQueue(eni *ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.deleteQueue = append(t.deleteQueue, eni)
}

// PushENIsToFrontOfDeleteQueue pushes the ENI list to the front of the delete queue
func (t *trunkENI) PushENIsToFrontOfDeleteQueue(pod *v1.Pod, eniList []*ENIDetails) {
	t.branchStateGate.RLock()
	defer t.branchStateGate.RUnlock()

	t.pushENIsToFrontOfDeleteQueue(pod, eniList)
}

func (t *trunkENI) pushENIsToFrontOfDeleteQueue(pod *v1.Pod, eniList []*ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if pod != nil {
		t.log.Info("pushing ENIs to delete queue and removing pod from cache",
			"uid", pod.UID, "ENIs", eniList)
		delete(t.uidToBranchENIMap, string(pod.UID))
	} else {
		t.log.Info("pushing ENIs to delete queue", "ENIs", eniList)
	}

	t.deleteQueue = append(eniList, t.deleteQueue...)
}

func (t *trunkENI) peekENIFromDeleteQueue() (eni *ENIDetails, hasENI bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	if len(t.deleteQueue) == 0 {
		return nil, false
	}
	return t.deleteQueue[0], true
}

func (t *trunkENI) findDeleteQueueEntryLocked(target *ENIDetails) int {
	for i, eni := range t.deleteQueue {
		if eni == target {
			return i
		}
	}
	return -1
}

func (t *trunkENI) removeENIFromDeleteQueue(target *ENIDetails, releaseVLAN bool) bool {
	t.lock.Lock()
	defer t.lock.Unlock()

	index := t.findDeleteQueueEntryLocked(target)
	if index < 0 {
		return false
	}
	t.deleteQueue = append(t.deleteQueue[:index], t.deleteQueue[index+1:]...)
	if releaseVLAN && target.VlanID != 0 {
		t.freeVlanIfUnreferencedLocked(target.VlanID)
	}
	return true
}

func (t *trunkENI) incrementDeleteRetry(target *ENIDetails) (int, bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	index := t.findDeleteQueueEntryLocked(target)
	if index < 0 {
		return 0, false
	}
	t.deleteQueue[index].deleteRetryCount++
	return t.deleteQueue[index].deleteRetryCount, true
}

func (t *trunkENI) moveENIToFrontOfDeleteQueue(target *ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	index := t.findDeleteQueueEntryLocked(target)
	if index <= 0 {
		return
	}
	t.deleteQueue = append(t.deleteQueue[:index], t.deleteQueue[index+1:]...)
	t.deleteQueue = append([]*ENIDetails{target}, t.deleteQueue...)
}

func (t *trunkENI) addBranchENIsToLedger(UID string, branchENIs []*ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.addBranchENIsToLedgerLocked(UID, branchENIs)
}

func (t *trunkENI) addBranchENIsToLedgerLocked(UID string, branchENIs []*ENIDetails) {
	if _, ok := t.uidToBranchENIMap[UID]; ok {
		t.log.Info("branch eni already exist not adding again", "request", branchENIs)
		return
	}

	t.uidToBranchENIMap[UID] = branchENIs
}

// commitReservedBranchENIsToLedger converts reserved capacity to Pod ownership.
func (t *trunkENI) commitReservedBranchENIsToLedger(UID string, branchENIs []*ENIDetails, reserved int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.pendingCreate -= reserved
	t.addBranchENIsToLedgerLocked(UID, branchENIs)
}

// moveReservedBranchENIsToDeleteQueue transfers failed allocations to cleanup.
func (t *trunkENI) moveReservedBranchENIsToDeleteQueue(reserved int, eniList []*ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.pendingCreate -= reserved
	t.log.Info("pushing ENIs to delete queue", "ENIs", eniList)
	t.deleteQueue = append(eniList, t.deleteQueue...)
}

// getBranchFromCache returns the branch from the cache
func (t *trunkENI) getBranchFromCache(UID string) (branchENIs []*ENIDetails, isPresent bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	branchENIs, isPresent = t.uidToBranchENIMap[UID]
	return
}

// assignVlanId assigns a free vlan id from the list of available vlan ids. In the future this can be changed to LL
func (t *trunkENI) assignVlanId() (int, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	for index, used := range t.usedVlanIds {
		if !used {
			t.usedVlanIds[index] = true
			return index, nil
		}
	}
	return 0, fmt.Errorf("failed to find free vlan id in the available %d ids", len(t.usedVlanIds))
}

// markVlanAssigned marks a vlan Id as assigned if not used
func (t *trunkENI) markVlanAssigned(vlanId int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.usedVlanIds[vlanId] = true
}

// freeVlanId frees a vlan ID currently used by a network interface
func (t *trunkENI) freeVlanId(vlanId int) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.freeVlanIfUnreferencedLocked(vlanId)
}

func (t *trunkENI) freeVlanIfUnreferencedLocked(vlanId int) {
	for _, enis := range t.uidToBranchENIMap {
		for _, eni := range enis {
			if eni.VlanID == vlanId {
				return
			}
		}
	}
	for _, eni := range t.deleteQueue {
		if eni.VlanID == vlanId {
			return
		}
	}
	isUsed := t.usedVlanIds[vlanId]
	if !isUsed {
		trunkENIOperationsErrCount.WithLabelValues("free_unused_vlan_id").Inc()
		t.log.Error(fmt.Errorf("failed to free a unused vlan id"), "", "vlan id", vlanId)
		return
	}
	t.usedVlanIds[vlanId] = false
}

func (t *trunkENI) getVlanIdFromTag(tags []ec2types.Tag) (int, error) {
	for _, tag := range tags {
		if *tag.Key == config.VLandIDTag {
			vlanID, err := strconv.Atoi(*tag.Value)
			if err != nil {
				return 0, err
			}
			if err := validateVlanID(vlanID); err != nil {
				return 0, err
			}
			return vlanID, nil
		}
	}

	return 0, fmt.Errorf("failed to find vlan tag from the list of tags")
}

func validateVlanID(vlanID int) error {
	if vlanID <= 0 || vlanID >= MaxAllocatableVlanIds {
		return fmt.Errorf("vlan id %d is outside the assignable range", vlanID)
	}
	return nil
}

// canCreateMore reserves eniCount slots when capacity is available.
func (t *trunkENI) canCreateMore(eniCount int) bool {
	t.lock.Lock()
	defer t.lock.Unlock()

	var usedBranches int
	for _, branches := range t.uidToBranchENIMap {
		usedBranches += len(branches)
	}

	if usedBranches+len(t.deleteQueue)+t.pendingCreate+eniCount >
		vpc.Limits[t.instance.Type()].BranchInterface {
		return false
	}
	t.pendingCreate += eniCount
	return true
}

func (t *trunkENI) Introspect() IntrospectResponse {
	t.lock.RLock()
	defer t.lock.RUnlock()

	response := IntrospectResponse{
		TrunkENIID:     t.trunkENIId,
		InstanceID:     t.instance.InstanceID(),
		PodToBranchENI: make(map[string][]ENIDetails),
	}
	for uid, allENI := range t.uidToBranchENIMap {
		var eniDetails []ENIDetails
		for _, eni := range allENI {
			eniDetails = append(eniDetails, *eni)
		}
		response.PodToBranchENI[uid] = eniDetails
	}
	for _, eni := range t.deleteQueue {
		response.DeleteQueue = append(response.DeleteQueue, *eni)
	}
	return response
}
