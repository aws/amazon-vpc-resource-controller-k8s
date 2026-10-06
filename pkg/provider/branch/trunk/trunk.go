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
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/aws/smithy-go"
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

var ErrNeedsColdInit = errors.New("restored trunk needs cold initialization")

var (
	networkInterfaceIDPattern = regexp.MustCompile(`^eni-([0-9a-f]{8}|[0-9a-f]{17})$`)

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

	prometheusRegistered = false
)

type TrunkENI interface {
	// InitTrunk initializes trunk interface
	InitTrunk(pods []v1.Pod) error
	// CreateAndAssociateBranchENIs creates and associate branch interface/s to trunk interface
	CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error)
	// PrepareForAllocation keeps prepareMu locked when it returns ErrNeedsColdInit.
	PrepareForAllocation(listRunningPods func() ([]v1.Pod, error)) ([]v1.Pod, error)
	// ColdInit rebuilds the trunk after PrepareForAllocation returns ErrNeedsColdInit.
	ColdInit(pods []v1.Pod) (ec2.EC2Instance, error)
	// CompletePreparation must be called exactly once after ErrNeedsColdInit.
	CompletePreparation(succeeded bool)
	// NeedsPreparation reports whether deferred restored-trunk inventory remains.
	NeedsPreparation() bool
	// PushBranchENIsToCoolDownQueue pushes the branch interface belonging to the pod to the cool down queue
	PushBranchENIsToCoolDownQueue(UID string)
	// DeleteCooledDownENIs deletes the interfaces that have been sitting in the queue for cool down period
	DeleteCooledDownENIs()
	// Reconcile compares the cache state with the list of pods to identify events that were missed and clean up the dangling interfaces
	Reconcile(pods []v1.Pod) bool
	// PushENIsToFrontOfDeleteQueue pushes the eni network interfaces to the front of the delete queue
	PushENIsToFrontOfDeleteQueue(*v1.Pod, []*ENIDetails)
	// TrunkENIID returns the trunk network interface ID.
	TrunkENIID() string
	// InstanceID returns the EC2 instance that owns the trunk.
	InstanceID() string
	// Introspect returns the state of the Trunk ENI
	Introspect() IntrospectResponse
}

// trunkENI is the first trunk network interface of an instance
type trunkENI struct {
	// Log is the logger with the instance details
	log logr.Logger
	// lock is used to perform concurrent operation on the shared variables like the list of used vlan ids
	lock sync.RWMutex
	// Lock order is prepareMu then lock. Once prepared is true, no whole-state commit remains.
	prepareMu sync.Mutex
	prepared  atomic.Bool
	// ec2ApiHelper is the wrapper interface that provides EC2 API helper functions
	ec2ApiHelper api.EC2APIHelper
	// trunkENIId is the interface id of the trunk network interface
	trunkENIId string
	// instance is the pointer to the instance details
	instance ec2.EC2Instance
	// usedVlanIds is the list of boolean value representing the used vlan ids
	usedVlanIds []bool
	// branchENIs is the list of BranchENIs associated with the trunk
	uidToBranchENIMap map[string][]*ENIDetails
	// deleteQueue is the queue of ENIs that are being cooled down before being deleted
	deleteQueue []*ENIDetails
	// nodeName tag is the tag added to trunk and branch ENIs created on the node
	nodeIDTag []ec2types.Tag
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
	if !prometheusRegistered {
		metrics.Registry.MustRegister(trunkENIOperationsErrCount)
		metrics.Registry.MustRegister(unreconciledTrunkENICount)
		metrics.Registry.MustRegister(branchENIOperationsSuccessCount)
		metrics.Registry.MustRegister(branchENIOperationsFailureCount)

		prometheusRegistered = true
	}
}

func (t *trunkENI) InitTrunk(podList []v1.Pod) error {
	restoredTrunkENIID := t.instance.RestoredTrunkENIID()
	if restoredTrunkENIID != "" {
		state := t.buildAnnotationBranchState(podList)
		t.commitBranchState(restoredTrunkENIID, state)
		t.log.V(1).Info("restored trunk identity from CNINode checkpoint", "trunk", restoredTrunkENIID)
		return nil
	}

	if err := t.coldInit(podList); err != nil {
		return err
	}
	t.prepared.Store(true)
	return nil
}

// ColdInit is called while PrepareForAllocation retains prepareMu for fallback.
func (t *trunkENI) ColdInit(podList []v1.Pod) (ec2.EC2Instance, error) {
	if err := t.instance.LoadDetails(t.ec2ApiHelper); err != nil {
		return nil, err
	}
	if err := t.coldInit(podList); err != nil {
		return nil, err
	}
	return t.instance, nil
}

func (t *trunkENI) coldInit(podList []v1.Pod) error {
	instanceID := t.instance.InstanceID()
	log := t.log.WithValues("request", "initialize", "instance ID", instanceID)
	var trunk ec2types.InstanceNetworkInterface

	nwInterfaces, err := t.ec2ApiHelper.GetInstanceNetworkInterface(&instanceID)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("describe_instance_nw_interface").Inc()
		return err
	}

	trunkENIID := ""
	for _, nwInterface := range nwInterfaces {
		// It's possible to get an empty network interface response if the instance is being deleted.
		if nwInterface.InterfaceType == nil {
			return fmt.Errorf("received an empty network interface response from EC2 %+v", nwInterface)
		}
		if *nwInterface.InterfaceType != string(ec2types.NetworkInterfaceTypeTrunk) {
			continue
		}
		if nwInterface.NetworkInterfaceId == nil {
			return fmt.Errorf("received trunk network interface without an ID")
		}
		if err = t.ec2ApiHelper.WaitForNetworkInterfaceStatusChange(
			nwInterface.NetworkInterfaceId,
			string(ec2types.AttachmentStatusAttached),
		); err != nil {
			return fmt.Errorf("failed to verify network interface status attached for %v", *nwInterface.NetworkInterfaceId)
		}
		trunkENIID = *nwInterface.NetworkInterfaceId
		trunk = nwInterface
	}

	// Trunk interface doesn't exist, try to create a new trunk interface.
	if trunkENIID == "" {
		freeIndex, err := t.instance.GetHighestUnusedDeviceIndex()
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

		if trunk.NetworkInterfaceId == nil {
			return fmt.Errorf("created trunk network interface has no ID")
		}
		trunkENIID = *trunk.NetworkInterfaceId
		t.commitBranchState(trunkENIID, newRecoveredBranchState())
		log.Info("created a new trunk interface", "trunk id", trunkENIID)

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
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&trunkENIID)
	if err != nil {
		return err
	}

	state := t.buildRecoveredBranchState(podList, branchInterfaces)
	t.commitBranchState(trunkENIID, state)

	log.V(1).Info("successfully initialized trunk with all associated branch interfaces",
		"trunk", trunkENIID, "branch interfaces", state.uidToBranchENIMap)

	return nil
}

type recoveredBranchState struct {
	uidToBranchENIMap map[string][]*ENIDetails
	deleteQueue       []*ENIDetails
	usedVlanIDs       []bool
}

func newRecoveredBranchState() *recoveredBranchState {
	state := &recoveredBranchState{
		uidToBranchENIMap: make(map[string][]*ENIDetails),
		usedVlanIDs:       make([]bool, MaxAllocatableVlanIds),
	}
	state.usedVlanIDs[0] = true
	return state
}

func (t *trunkENI) buildAnnotationBranchState(pods []v1.Pod) *recoveredBranchState {
	state := newRecoveredBranchState()
	for index := range pods {
		branchENIs := t.decodeBranchInterfacesUsedByPod(&pods[index])
		if len(branchENIs) == 0 {
			continue
		}
		state.uidToBranchENIMap[string(pods[index].UID)] = branchENIs
		for _, branchENI := range branchENIs {
			if branchENI.VlanID != 0 {
				state.usedVlanIDs[branchENI.VlanID] = true
			}
		}
	}
	return state
}

func (t *trunkENI) buildRecoveredBranchState(
	pods []v1.Pod,
	branchInterfaces []*ec2types.NetworkInterface,
) *recoveredBranchState {
	state := newRecoveredBranchState()
	branchByID := make(map[string]*ec2types.NetworkInterface, len(branchInterfaces))
	for _, branchInterface := range branchInterfaces {
		if branchInterface == nil || branchInterface.NetworkInterfaceId == nil ||
			*branchInterface.NetworkInterfaceId == "" {
			trunkENIOperationsErrCount.WithLabelValues("branch_eni_missing_id").Inc()
			t.log.Error(fmt.Errorf("branch ENI response is missing an ID"),
				"ignoring unusable branch ENI response")
			continue
		}
		branchByID[*branchInterface.NetworkInterfaceId] = branchInterface
	}

	for index := range pods {
		uid := string(pods[index].UID)
		for _, branchENI := range t.decodeBranchInterfacesUsedByPod(&pods[index]) {
			branchInterface, found := branchByID[branchENI.ID]
			if !found {
				trunkENIOperationsErrCount.WithLabelValues("get_branch_eni_from_ec2").Inc()
				t.log.Error(fmt.Errorf("eni allocated to pod not found in ec2"),
					"eni not found", "eni", branchENI, "podUID", uid)
				continue
			}
			if branchENI.VlanID == 0 {
				if vlanID, err := t.getVlanIdFromTag(branchInterface.TagSet); err == nil {
					branchENI.VlanID = vlanID
				}
			}
			state.uidToBranchENIMap[uid] = append(state.uidToBranchENIMap[uid], branchENI)
			if branchENI.VlanID != 0 {
				state.usedVlanIDs[branchENI.VlanID] = true
			}
			delete(branchByID, branchENI.ID)
		}
	}

	orphanIDs := make([]string, 0, len(branchByID))
	for id := range branchByID {
		orphanIDs = append(orphanIDs, id)
	}
	slices.Sort(orphanIDs)
	for _, id := range orphanIDs {
		details := &ENIDetails{
			ID:                id,
			deletionTimeStamp: time.Now(),
		}
		vlanID, err := t.getVlanIdFromTag(branchByID[id].TagSet)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("corrupt_orphan_branch_eni").Inc()
			t.log.Error(err, "queuing corrupt orphan branch ENI by ID", "interface", id)
		} else {
			details.VlanID = vlanID
			state.usedVlanIDs[vlanID] = true
		}
		state.deleteQueue = append(state.deleteQueue, details)
	}
	return state
}

func (t *trunkENI) decodeBranchInterfacesUsedByPod(pod *v1.Pod) []*ENIDetails {
	branchAnnotation, isPresent := pod.Annotations[config.ResourceNamePodENI]
	if !isPresent {
		return nil
	}

	var eniDetails []*ENIDetails
	if err := json.Unmarshal([]byte(branchAnnotation), &eniDetails); err != nil {
		trunkENIOperationsErrCount.WithLabelValues("unusable_pod_eni_annotation").Inc()
		t.log.Error(err, "failed to unmarshal resource annotation",
			"annotationBytes", len(branchAnnotation),
			"podUID", pod.UID)
		return nil
	}

	usable := make([]*ENIDetails, 0, len(eniDetails))
	invalidENIIDs := 0
	for _, eni := range eniDetails {
		if eni == nil || !networkInterfaceIDPattern.MatchString(eni.ID) {
			trunkENIOperationsErrCount.WithLabelValues("unusable_pod_eni_annotation").Inc()
			invalidENIIDs++
			continue
		}
		recovered := *eni
		if err := validateVlanID(recovered.VlanID); err != nil {
			trunkENIOperationsErrCount.WithLabelValues("branch_eni_unusable_vlan").Inc()
			t.log.Error(err,
				"preserving branch ENI ownership while deferring VLAN recovery to EC2 inventory",
				"eni", recovered.ID,
				"vlanID", recovered.VlanID,
				"podUID", pod.UID)
			recovered.VlanID = 0
		}
		usable = append(usable, &recovered)
	}
	if invalidENIIDs != 0 {
		t.log.Error(fmt.Errorf("%d branch ENI annotation entries have invalid ENI IDs", invalidENIIDs),
			"ignored unusable resource annotation entries",
			"podUID", pod.UID)
	}
	return usable
}

func validateVlanID(vlanID int) error {
	if vlanID <= 0 || vlanID >= MaxAllocatableVlanIds {
		return fmt.Errorf("VLAN ID %d is outside the allocatable range", vlanID)
	}
	return nil
}

func (t *trunkENI) commitBranchState(trunkENIID string, state *recoveredBranchState) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.trunkENIId = trunkENIID
	t.uidToBranchENIMap = state.uidToBranchENIMap
	t.deleteQueue = state.deleteQueue
	t.usedVlanIds = state.usedVlanIDs
}

func (t *trunkENI) PrepareForAllocation(
	listRunningPods func() ([]v1.Pod, error),
) ([]v1.Pod, error) {
	if t.prepared.Load() {
		return nil, nil
	}

	t.prepareMu.Lock()
	if t.prepared.Load() {
		t.prepareMu.Unlock()
		return nil, nil
	}

	pods, err := listRunningPods()
	if err != nil {
		t.prepareMu.Unlock()
		return nil, fmt.Errorf("listing running pods before branch inventory: %w", err)
	}

	restoredTrunkENIID := t.instance.RestoredTrunkENIID()
	if restoredTrunkENIID == "" {
		return pods, ErrNeedsColdInit
	}

	err = t.validateRestoredNetworkInterfaces(restoredTrunkENIID)
	if err != nil {
		if errors.Is(err, ErrNeedsColdInit) {
			t.log.Info("restored trunk is invalid, falling back to cold initialization",
				"trunk", restoredTrunkENIID,
				"error", err)
			return pods, err
		}
		t.prepareMu.Unlock()
		trunkENIOperationsErrCount.WithLabelValues("recover_branch_state").Inc()
		return nil, fmt.Errorf("validating restored trunk before allocation: %w", err)
	}

	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&restoredTrunkENIID)
	if err != nil {
		t.prepareMu.Unlock()
		trunkENIOperationsErrCount.WithLabelValues("recover_branch_state").Inc()
		return nil, fmt.Errorf("describing branch ENIs before allocation: %w", err)
	}
	state := t.buildRecoveredBranchState(pods, branchInterfaces)
	t.commitBranchState(restoredTrunkENIID, state)
	t.prepared.Store(true)
	t.prepareMu.Unlock()
	branchENIOperationsSuccessCount.WithLabelValues("recover_branch_state").Inc()

	t.log.Info("validated restored trunk and recovered branch state before allocation",
		"trunk", restoredTrunkENIID,
		"ownedPods", len(state.uidToBranchENIMap),
		"orphanENIs", len(state.deleteQueue))
	return pods, nil
}

func (t *trunkENI) CompletePreparation(succeeded bool) {
	if succeeded {
		t.prepared.Store(true)
		branchENIOperationsSuccessCount.WithLabelValues("recover_branch_state").Inc()
	} else {
		trunkENIOperationsErrCount.WithLabelValues("recover_branch_state").Inc()
	}
	t.prepareMu.Unlock()
}

func (t *trunkENI) NeedsPreparation() bool {
	return !t.prepared.Load()
}

func (t *trunkENI) InstanceID() string {
	return t.instance.InstanceID()
}

func (t *trunkENI) validateRestoredNetworkInterfaces(trunkENIID string) error {
	primaryENIID := t.instance.PrimaryNetworkInterfaceID()
	if primaryENIID == "" || trunkENIID == "" || primaryENIID == trunkENIID {
		return fmt.Errorf("%w: checkpoint has invalid primary or trunk ENI IDs", ErrNeedsColdInit)
	}

	interfaces, err := t.ec2ApiHelper.DescribeNetworkInterfaces([]string{primaryENIID, trunkENIID})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) &&
			(apiErr.ErrorCode() == "InvalidNetworkInterfaceID.NotFound" ||
				apiErr.ErrorCode() == "InvalidNetworkInterfaceID.Malformed") {
			return fmt.Errorf("%w: %v", ErrNeedsColdInit, err)
		}
		return err
	}

	var primaryInterface, trunkInterface *ec2types.NetworkInterface
	for index := range interfaces {
		if interfaces[index].NetworkInterfaceId == nil {
			continue
		}
		switch *interfaces[index].NetworkInterfaceId {
		case primaryENIID:
			primaryInterface = &interfaces[index]
		case trunkENIID:
			trunkInterface = &interfaces[index]
		}
	}
	if primaryInterface == nil {
		return fmt.Errorf("%w: primary ENI %s was not returned", ErrNeedsColdInit, primaryENIID)
	}
	if primaryInterface.Attachment == nil ||
		primaryInterface.Attachment.InstanceId == nil ||
		*primaryInterface.Attachment.InstanceId != t.instance.InstanceID() ||
		primaryInterface.Attachment.Status != ec2types.AttachmentStatusAttached ||
		primaryInterface.Attachment.DeviceIndex == nil ||
		*primaryInterface.Attachment.DeviceIndex != 0 {
		return fmt.Errorf("%w: primary ENI %s is not attached to instance %s at device index 0",
			ErrNeedsColdInit, primaryENIID, t.instance.InstanceID())
	}
	if trunkInterface == nil {
		return fmt.Errorf("%w: trunk ENI %s was not returned", ErrNeedsColdInit, trunkENIID)
	}
	if trunkInterface.InterfaceType != ec2types.NetworkInterfaceTypeTrunk {
		return fmt.Errorf("%w: ENI %s has interface type %q",
			ErrNeedsColdInit, trunkENIID, trunkInterface.InterfaceType)
	}
	if trunkInterface.Attachment == nil ||
		trunkInterface.Attachment.InstanceId == nil ||
		*trunkInterface.Attachment.InstanceId != t.instance.InstanceID() ||
		trunkInterface.Attachment.Status != ec2types.AttachmentStatusAttached {
		return fmt.Errorf("%w: trunk ENI %s is not attached to instance %s",
			ErrNeedsColdInit, trunkENIID, t.instance.InstanceID())
	}
	if err := t.instance.RefreshPrimaryNetworkInterface(*primaryInterface); err != nil {
		return fmt.Errorf("%w: refreshing primary ENI %s: %v",
			ErrNeedsColdInit, primaryENIID, err)
	}
	return nil
}

// Reconcile reconciles the state from the API Server to the internal cache of EC2 Branch Interfaces, if the controller
// missed some delete events the reconcile method will perform cleanup for the dangling interfaces
func (t *trunkENI) Reconcile(pods []v1.Pod) bool {
	if !t.prepared.Load() {
		t.prepareMu.Lock()
		defer t.prepareMu.Unlock()
	}

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

// CreateAndAssociateBranchToTrunk creates a new branch network interface and associates the branch to the trunk
// network interface. It returns a Json convertible structure which has all the required details of the branch ENI
func (t *trunkENI) CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error) {
	log := t.log.WithValues("request", "create", "pod namespace", pod.Namespace, "pod name", pod.Name)

	branchENI, isPresent := t.getBranchFromCache(string(pod.UID))
	if isPresent {
		// Possible when older pod with same namespace and name is still being deleted
		return nil, fmt.Errorf("cannot create new eni entry already exist, older entry : %v", branchENI)
	}

	if !t.canCreateMore() {
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
		} else {
			branchENIOperationsSuccessCount.WithLabelValues("created_branch_eni_succeeded").Inc()
		}

		// Branch ENI can have an IPv4 address, IPv6 address, or both
		var v4Addr, v6Addr string
		if nwInterface.PrivateIpAddress != nil {
			v4Addr = *nwInterface.PrivateIpAddress
		}
		if nwInterface.Ipv6Address != nil {
			v6Addr = *nwInterface.Ipv6Address
		}
		newENI := &ENIDetails{
			ID: *nwInterface.NetworkInterfaceId, MACAdd: *nwInterface.MacAddress,
			IPV4Addr: v4Addr, IPV6Addr: v6Addr, SubnetCIDR: t.instance.SubnetCidrBlock(),
			SubnetV6CIDR: t.instance.SubnetV6CidrBlock(), VlanID: vlanID,
		}
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
		log.Error(err, "failed to create ENI, moving the ENI to delete list")
		// Moving to delete list, because it has all the retrying logic in case of failure
		t.PushENIsToFrontOfDeleteQueue(nil, newENIs)
		return nil, err
	}

	t.addBranchToCache(string(pod.UID), newENIs)

	log.Info("successfully created branch interfaces", "interfaces", newENIs,
		"security group used", securityGroups)

	return newENIs, nil
}

// DeleteBranchNetworkInterface deletes the branch network interface and returns an error in case of failure to delete
func (t *trunkENI) PushBranchENIsToCoolDownQueue(UID string) {
	if !t.prepared.Load() {
		t.prepareMu.Lock()
		defer t.prepareMu.Unlock()
	}

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
	if !t.prepared.Load() {
		// Preparation replaces provisional ownership with EC2-verified branches before deletion.
		return
	}

	for eni, hasENI := t.popENIFromDeleteQueue(); hasENI; eni, hasENI = t.popENIFromDeleteQueue() {
		if eni.deletionTimeStamp.IsZero() ||
			time.Now().After(eni.deletionTimeStamp.Add(cooldown.GetCoolDown().GetCoolDownPeriod())) {
			err := t.deleteENI(eni)
			if err != nil {
				eni.deleteRetryCount++
				if eni.deleteRetryCount >= MaxDeleteRetries {
					t.log.Error(err, "forgetting eni as max retries exceeded", "eni", eni)
					// TODO: free vlan id?
					continue
				}
				t.log.Error(err, "failed to delete eni, will retry", "eni", eni)
				t.PushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{eni})
				continue
			}
			t.log.V(1).Info("deleted eni successfully", "eni", eni, "deletion time", time.Now(),
				"pushed to queue time", eni.deletionTimeStamp)
		} else {
			// Since the current item is not cooled down so the items added after it would not be cooled down either
			t.PushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{eni})
			return
		}
	}
}

// deleteENIs deletes the provided ENIs and frees up the Vlan assigned to then
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

	// Free vlan id used by the branch ENI
	if eniDetail.VlanID != 0 {
		t.freeVlanId(eniDetail.VlanID)
	}

	return nil
}

// pushENIsToFrontOfDeleteQueue pushes the ENI list to the front of the delete queue
func (t *trunkENI) PushENIsToFrontOfDeleteQueue(pod *v1.Pod, eniList []*ENIDetails) {
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

// popENIFromDeleteQueue pops an ENI from delete queue, if the queue is empty then the false is returned
func (t *trunkENI) popENIFromDeleteQueue() (eni *ENIDetails, hasENI bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if len(t.deleteQueue) > 0 {
		eni = t.deleteQueue[0]
		hasENI = true
		t.deleteQueue = t.deleteQueue[1:]
	}

	return eni, hasENI
}

// addBranchToCache adds the given branch to the cache if not already present
func (t *trunkENI) addBranchToCache(UID string, branchENIs []*ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if _, ok := t.uidToBranchENIMap[UID]; ok {
		t.log.Info("branch eni already exist not adding again", "request", branchENIs)
		return
	}

	t.uidToBranchENIMap[UID] = branchENIs
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

// freeVlanId frees a vlan ID currently used by a network interface
func (t *trunkENI) freeVlanId(vlanId int) {
	t.lock.Lock()
	defer t.lock.Unlock()

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
		if tag.Key != nil && *tag.Key == config.VLandIDTag {
			if tag.Value == nil {
				return 0, fmt.Errorf("VLAN tag has no value")
			}
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

func (t *trunkENI) canCreateMore() bool {
	branchLimit := vpc.Limits[t.instance.Type()].BranchInterface

	t.lock.RLock()
	defer t.lock.RUnlock()

	var usedBranches int
	for _, branches := range t.uidToBranchENIMap {
		usedBranches += len(branches)
	}

	if usedBranches+len(t.deleteQueue) < branchLimit {
		return true
	}
	return false
}

func (t *trunkENI) TrunkENIID() string {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.trunkENIId
}

func (t *trunkENI) Introspect() IntrospectResponse {
	instanceID := t.instance.InstanceID()

	t.lock.RLock()
	defer t.lock.RUnlock()

	response := IntrospectResponse{
		TrunkENIID:     t.trunkENIId,
		InstanceID:     instanceID,
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
