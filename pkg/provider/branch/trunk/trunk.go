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
	"golang.org/x/sync/singleflight"
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

// ErrInvalidRestoredLedger indicates that pod annotations cannot produce a
// valid branch ENI and VLAN ledger.
var ErrInvalidRestoredLedger = fmt.Errorf("restored pod-annotation ledger is structurally invalid")

// ErrTrunkChangedDuringResync means the authoritative rebuild did not land on the
// trunk used for restoration (e.g. the trunk no longer exists). That is
// outside the runtime ledger-resync boundary, so recovery fails without replacing
// the live state.
var ErrTrunkChangedDuringResync = fmt.Errorf("trunk changed during ledger resync")

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
	// branchENIOrphanReclaimCount records each reclaim attempt and its outcome.
	branchENIOrphanReclaimCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_eni_orphan_reclaim_total",
			Help: "The number of orphan branch ENI reclaim outcomes on associate failure",
		},
		[]string{"result"},
	)
	// trunkReinitCount records whether trunk initialization restored local state
	// or used authoritative EC2 discovery.
	trunkReinitCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trunk_reinit_total",
			Help: "The number of trunk re-initializations by path (restored|ec2) and result",
		},
		[]string{"path", "result"},
	)
	// trunkResyncCount records authoritative EC2 resyncs after restored state is
	// found to be inconsistent at runtime.
	trunkResyncCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trunk_reinit_resync_total",
			Help: "The number of per-node authoritative EC2 resyncs triggered by a runtime state contradiction",
		},
		[]string{"trigger"},
	)

	prometheusRegistered = false
)

type TrunkENI interface {
	// InitTrunk initializes trunk interface
	InitTrunk(instance ec2.EC2Instance, pods []v1.Pod) error
	// CreateAndAssociateBranchENIs creates and associates branch interface/s to the trunk interface, committing pod ownership via commitOwnership before the recovery gate is released
	CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int, commitOwnership func([]*ENIDetails) error) ([]*ENIDetails, error)
	// PushBranchENIsToCoolDownQueue pushes the branch interface belonging to the pod to the cool down queue
	PushBranchENIsToCoolDownQueue(UID string)
	// DeleteCooledDownENIs deletes the interfaces that have been sitting in the queue for cool down period
	DeleteCooledDownENIs()
	// Reconcile compares the cache state with the list of pods to identify events that were missed and clean up the dangling interfaces
	Reconcile(pods []v1.Pod) bool
	// PushENIsToFrontOfDeleteQueue pushes the eni network interfaces to the front of the delete queue
	PushENIsToFrontOfDeleteQueue(*v1.Pod, []*ENIDetails)
	// InitFromNodeNetworkState rebuilds trunk state from its ID and pod
	// annotations without calling EC2.
	InitFromNodeNetworkState(trunkENIID string, pods []v1.Pod) error
	// IsRestoredFromNodeNetworkState reports whether the trunk state came from
	// NodeNetworkState.
	IsRestoredFromNodeNetworkState() bool
	// ResyncTrunkLedgerFromEC2 rebuilds the branch/VLAN ledger from EC2.
	// It reads listPods after mutations quiesce and uses trigger as a metric label.
	ResyncTrunkLedgerFromEC2(listPods func() ([]v1.Pod, error), trigger string) error
	// TrunkENIID returns the trunk ENI ID.
	TrunkENIID() string
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
	// instance is the pointer to the instance details
	instance ec2.EC2Instance
	// usedVlanIds is the list of boolean value representing the used vlan ids
	usedVlanIds []bool
	// branchENIs is the list of BranchENIs associated with the trunk
	uidToBranchENIMap map[string][]*ENIDetails
	// inFlightENIs contains ENIs that exist in EC2 but have not reached the
	// ownership cache or delete queue.
	inFlightENIs map[string]struct{}
	// deleteQueue is the queue of ENIs that are being cooled down before being deleted
	deleteQueue []*ENIDetails
	// nodeName tag is the tag added to trunk and branch ENIs created on the node
	nodeIDTag []ec2types.Tag
	// restoredFromNodeNetworkState records whether NodeNetworkState remains the
	// source of the live ledger.
	restoredFromNodeNetworkState bool
	// recoveryGate serializes trunk mutations against an authoritative ledger
	// resync: mutating operations hold it shared for their whole duration, the
	// resync holds it exclusively, so no allocation, delete or reconcile can race
	// the rebuild and be lost by the swap. Distinct from lock, which only guards
	// short critical sections around the maps.
	recoveryGate sync.RWMutex
	// resyncGroup coalesces concurrent runtime resyncs for this trunk into a
	// single authoritative EC2 rebuild.
	resyncGroup singleflight.Group
	// reclaimGroup coalesces concurrent orphan-reclaim describes for this trunk.
	reclaimGroup singleflight.Group
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
		inFlightENIs:      make(map[string]struct{}),
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
		metrics.Registry.MustRegister(branchENIOrphanReclaimCount)
		metrics.Registry.MustRegister(trunkReinitCount)
		metrics.Registry.MustRegister(trunkResyncCount)

		prometheusRegistered = true
	}
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
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&t.trunkENIId, aws.String(t.instance.SubnetID()))
	if err != nil {
		return err
	}

	// Convert the list of interfaces to a set
	associatedBranchInterfaces := make(map[string]*ec2types.NetworkInterface)
	for _, branchInterface := range branchInterfaces {
		associatedBranchInterfaces[*branchInterface.NetworkInterfaceId] = branchInterface
	}

	// vlanOwner keeps the rebuilt ledger structurally sound. This path is the
	// authoritative fallback for a rejected state, so it must not reproduce the
	// shapes the state was rejected for: an out-of-range VLAN that never gets
	// reserved, or two ENIs recorded on one VLAN.
	//
	// The VLAN value itself comes from the pod annotation. Both it and the ENI's
	// VLAN tag are copies this controller writes (the tag exists only because
	// DescribeTrunkInterfaceAssociations is unavailable), but the annotation is
	// written after the association succeeds and is what the node CNI programs, so
	// it is the value the data plane actually uses. A disagreement is recorded as
	// drift rather than silently preferring the tag, since tracking a VLAN the CNI
	// never programmed would make freeVlanId release the wrong slot.
	vlanOwner := make(map[int]string)

	// ownershipComplete tracks whether every pod's ownership could be read. An
	// unreadable annotation means a pod may own an ENI we cannot attribute, and an
	// unattributed ENI is treated as belonging to no pod, so reclaiming would delete
	// a running pod's interface. When that happens the ENIs are left in place for a
	// later init to attribute instead.
	ownershipComplete := true

	// From the list of pods on the given node, and the branch ENIs from EC2 API call rebuild the internal cache
	for _, pod := range podList {
		pod := pod // Fix gosec G601, so we can use &node
		eniListFromPod, usable := t.decodeBranchInterfacesUsedByPod(&pod)
		if !usable {
			ownershipComplete = false
			trunkENIOperationsErrCount.WithLabelValues("unusable_pod_eni_annotation").Inc()
			log.Error(fmt.Errorf("pod branch eni annotation is unusable"),
				"cannot attribute this pod's branch ENIs", "pod", string(pod.UID))
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

			vlanID := eni.VlanID
			if taggedVlanID, tagErr := t.getVlanIdFromTag(branchInterface.TagSet); tagErr == nil && taggedVlanID != vlanID {
				trunkENIOperationsErrCount.WithLabelValues("branch_eni_vlan_tag_drift").Inc()
				log.Error(fmt.Errorf("vlan id tag disagrees with the pod annotation"),
					"using the annotation vlan id", "eni", eni.ID, "annotationVlanID", vlanID, "tagVlanID", taggedVlanID)
			}
			if vlanID <= 0 || vlanID >= MaxAllocatableVlanIds {
				trunkENIOperationsErrCount.WithLabelValues("branch_eni_unusable_vlan").Inc()
				log.Error(fmt.Errorf("vlan id out of range"),
					"leaving branch eni unattributed", "eni", eni.ID, "vlanID", vlanID, "pod", uid)
				continue
			}
			if other, dup := vlanOwner[vlanID]; dup {
				trunkENIOperationsErrCount.WithLabelValues("branch_eni_duplicate_vlan").Inc()
				log.Error(fmt.Errorf("vlan id already owned"),
					"leaving branch eni unattributed", "eni", eni.ID, "vlanID", vlanID, "pod", uid, "otherPod", other)
				continue
			}
			vlanOwner[vlanID] = uid
			t.markVlanAssigned(vlanID)

			branchENIs = append(branchENIs, eni)
			delete(associatedBranchInterfaces, eni.ID)
		}
		t.uidToBranchENIMap[uid] = branchENIs
	}

	// Delete the branch ENI that don't belong to any pod. Skipped entirely when some
	// pod's ownership could not be read: "owned by no pod" is only a safe conclusion
	// if every pod's ownership was legible. Leaking an ENI until the next init is
	// recoverable; deleting a live pod's ENI is not.
	if !ownershipComplete {
		log.Info("skipping branch eni reclaim, some pod ownership could not be read",
			"unreclaimed", len(associatedBranchInterfaces))
		trunkReinitCount.WithLabelValues("ec2", "reclaim_skipped").Inc()
		// Their VLANs are still occupied in EC2 even though we cannot say who owns
		// them, so reserve them. Otherwise assignVlanId hands out a VLAN the trunk is
		// already associated on, the association fails, and a resync re-runs this
		// same path and skips again.
		for _, branchInterface := range associatedBranchInterfaces {
			vlanID, err := t.getVlanIdFromTag(branchInterface.TagSet)
			if err != nil {
				trunkENIOperationsErrCount.WithLabelValues("get_vlan_from_tag").Inc()
				log.Error(err, "cannot reserve vlan for unreclaimed branch eni",
					"interface", *branchInterface.NetworkInterfaceId)
				continue
			}
			t.markVlanAssigned(vlanID)
		}
	} else {
		for _, branchInterface := range associatedBranchInterfaces {
			t.log.Info("pushing eni to delete queue as no pod owns it", "eni",
				*branchInterface.NetworkInterfaceId)

			vlanId, err := t.getVlanIdFromTag(branchInterface.TagSet)
			if err != nil {
				trunkENIOperationsErrCount.WithLabelValues("get_vlan_from_tag").Inc()
				log.Error(err, "failed to find vlan id", "interface", *branchInterface.NetworkInterfaceId)
				continue
			}

			// Even thought the ENI is going to be deleted still mark Vlan ID assigned as ENI will sit in cool down queue for a while
			t.markVlanAssigned(vlanId)
			t.pushENIToDeleteQueue(&ENIDetails{
				ID:                *branchInterface.NetworkInterfaceId,
				VlanID:            vlanId,
				deletionTimeStamp: time.Now(),
			})
		}
	}

	log.V(1).Info("successfully initialized trunk with all associated branch interfaces",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)

	trunkReinitCount.WithLabelValues("ec2", "success").Inc()
	return nil
}

// InitFromNodeNetworkState rebuilds the trunk's branch and VLAN ledger from the
// observed trunk id and pod annotations without calling EC2.
//
// The candidate ledger is validated before it replaces live state. Validation
// requires each VLAN to be in range and each branch ENI and VLAN to have at most
// one pod owner.
func (t *trunkENI) InitFromNodeNetworkState(trunkENIID string, podList []v1.Pod) error {
	candidateMap := make(map[string][]*ENIDetails)
	vlanOwner := make(map[int]string)   // vlan id -> pod uid
	eniOwner := make(map[string]string) // branch eni id -> pod uid

	reject := func(msg string, kv ...interface{}) error {
		trunkReinitCount.WithLabelValues("restored", "ledger_invalid").Inc()
		t.log.Error(ErrInvalidRestoredLedger, msg, kv...)
		return ErrInvalidRestoredLedger
	}

	for _, pod := range podList {
		pod := pod
		uid := string(pod.UID)
		eniList, usable := t.decodeBranchInterfacesUsedByPod(&pod)
		if !usable {
			// The EC2 fallback preserves ENIs whose ownership cannot be decoded.
			return reject("pod branch eni annotation is unusable", "pod", uid)
		}
		if len(eniList) == 0 {
			continue
		}
		for _, eni := range eniList {
			// 0 is never an assigned vlan id: NewTrunkENI pre-marks it used and the
			// delete path treats it as "no vlan". Reconstruct the same invariant the
			// allocation path maintains.
			if eni.VlanID <= 0 || eni.VlanID >= MaxAllocatableVlanIds {
				return reject("branch eni has out-of-range vlan id in pod annotation",
					"eni", eni.ID, "vlanID", eni.VlanID, "pod", uid)
			}
			if other, dup := eniOwner[eni.ID]; dup {
				return reject("branch eni claimed by two pods in annotations",
					"eni", eni.ID, "pod", uid, "otherPod", other)
			}
			if other, dup := vlanOwner[eni.VlanID]; dup {
				return reject("vlan id claimed by two pods in annotations",
					"vlanID", eni.VlanID, "pod", uid, "otherPod", other)
			}
			eniOwner[eni.ID] = uid
			vlanOwner[eni.VlanID] = uid
		}
		candidateMap[uid] = eniList
	}

	// Commit the validated ledger under one lock.
	t.lock.Lock()
	t.trunkENIId = trunkENIID
	t.uidToBranchENIMap = candidateMap
	for vlanID := range vlanOwner {
		t.usedVlanIds[vlanID] = true
	}
	t.restoredFromNodeNetworkState = true
	t.lock.Unlock()

	trunkReinitCount.WithLabelValues("restored", "success").Inc()
	t.log.Info("restored trunk ledger from NodeNetworkState",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)
	return nil
}

// IsRestoredFromNodeNetworkState reports whether this ledger was restored from
// NodeNetworkState and has not been resynced from EC2.
func (t *trunkENI) IsRestoredFromNodeNetworkState() bool {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.restoredFromNodeNetworkState
}

// ResyncTrunkLedgerFromEC2 rebuilds this trunk's branch/VLAN ledger from EC2 when
// a restored trunk is proven inconsistent at runtime. It reuses InitTrunk
// without reloading instance details. If the rebuilt state refers to a different
// trunk ENI, it returns ErrTrunkChangedDuringResync and leaves the live state
// untouched.
//
// Ordering matters: the exclusive recovery gate is taken first so in-flight trunk
// mutations drain before the pod list is read. Reading pods earlier would race
// concurrent allocations and could omit a newly allocated ENI from the rebuilt
// ledger. listPods is a cache read, so taking it late is cheap.
//
// resyncGroup coalesces concurrent detections into one rebuild, and a successful
// resync clears the restoration flag so recovery cannot repeat or loop.
func (t *trunkENI) ResyncTrunkLedgerFromEC2(listPods func() ([]v1.Pod, error), trigger string) error {
	_, err, _ := t.resyncGroup.Do(t.instance.InstanceID(), func() (interface{}, error) {
		// Quiesce trunk mutations for the whole rebuild+swap.
		t.recoveryGate.Lock()
		defer t.recoveryGate.Unlock()

		// A concurrent resync may already have made this trunk authoritative.
		if !t.IsRestoredFromNodeNetworkState() {
			return nil, nil
		}
		existingTrunkID := t.TrunkENIID()
		trunkResyncCount.WithLabelValues(trigger).Inc()
		t.log.Info("resyncing trunk ledger from EC2 after a runtime contradiction",
			"trunk", existingTrunkID, "trigger", trigger)

		// Read pods only after mutations have drained.
		podList, err := listPods()
		if err != nil {
			return nil, err
		}

		// InitTrunk assumes exclusive, lock-free access to the trunk it builds, so
		// rebuild on a scratch trunk and swap the result in atomically.
		scratch := NewTrunkENI(t.log, t.instance, t.ec2ApiHelper).(*trunkENI)
		if err := scratch.InitTrunk(t.instance, podList); err != nil {
			// Keep the restored ledger so a later contradiction can retry.
			return nil, err
		}
		if scratch.trunkENIId != existingTrunkID {
			return nil, fmt.Errorf("%w: had %s, EC2 rebuild produced %s",
				ErrTrunkChangedDuringResync, existingTrunkID, scratch.trunkENIId)
		}

		t.lock.Lock()
		t.uidToBranchENIMap = scratch.uidToBranchENIMap
		t.usedVlanIds = scratch.usedVlanIds
		t.deleteQueue = scratch.deleteQueue
		t.restoredFromNodeNetworkState = false
		t.lock.Unlock()
		return nil, nil
	})
	return err
}

// TrunkENIID returns the trunk ENI ID.
func (t *trunkENI) TrunkENIID() string {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.trunkENIId
}

// Reconcile reconciles the state from the API Server to the internal cache of EC2 Branch Interfaces, if the controller
// missed some delete events the reconcile method will perform cleanup for the dangling interfaces
func (t *trunkENI) Reconcile(pods []v1.Pod) bool {
	t.recoveryGate.RLock()
	defer t.recoveryGate.RUnlock()

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

// CreateAndAssociateBranchENIs creates branch network interfaces, associates
// them with the trunk, and commits pod ownership before returning.
//
// The whole sequence runs under the shared recovery gate. That boundary has to
// include commitOwnership: between association and the pod annotation the ENI
// exists in EC2 but no pod claims it, and InitTrunk classifies exactly that as
// unowned and queues it for deletion. A resync reading pods in that
// window would reap a live pod's ENI. If commitOwnership fails, the ENIs are
// released under the same gate, so the ledger never holds an uncommitted
// attachment.
func (t *trunkENI) CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int,
	commitOwnership func([]*ENIDetails) error,
) ([]*ENIDetails, error) {
	t.recoveryGate.RLock()
	defer t.recoveryGate.RUnlock()

	newENIs, err := t.createAndAssociateBranchENIs(pod, securityGroups, eniCount)
	if err != nil {
		return nil, err
	}

	if commitOwnership != nil {
		if err := commitOwnership(newENIs); err != nil {
			t.pushENIsToFrontOfDeleteQueue(pod, newENIs)
			return nil, err
		}
	}
	return newENIs, nil
}

// createAndAssociateBranchENIs is the ungated allocation body; the caller holds
// the recovery gate.
func (t *trunkENI) createAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error) {
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

	var createdENIIDs []string
	defer func() {
		t.clearENIsInFlight(createdENIIDs)
	}()

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

		t.markENIInFlight(*nwInterface.NetworkInterfaceId)
		createdENIIDs = append(createdENIIDs, *nwInterface.NetworkInterfaceId)

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
			t.reclaimOrphansOnAssociateFailure()
			break
		}
		newENI.AssociationID = *associationOutput.InterfaceAssociation.AssociationId
	}

	if err != nil {
		log.Error(err, "failed to create ENI, moving the ENI to delete list")
		// Moving to delete list, because it has all the retrying logic in case of failure.
		// Stamp them so they cool down like any other released ENI instead of being
		// deleted on the next worker pass. That keeps the slot accounted for in
		// canCreateMore while the pod retries, so an allocation failing against a
		// trunk that EC2 considers full reliably surfaces as ErrCurrentlyAtMaxCapacity
		// (the runtime recovery trigger) rather than depending on which worker runs
		// first.
		//
		// Append rather than push to the front: DeleteCooledDownENIs stops at the
		// first item that has not cooled down, assuming everything behind it is
		// newer. A freshly stamped ENI at the front would hold already-cooled ENIs
		// back for another full period, and repeated failures would postpone their
		// cleanup indefinitely.
		now := time.Now()
		for _, eni := range newENIs {
			eni.deletionTimeStamp = now
			t.pushENIToDeleteQueue(eni)
		}
		return nil, err
	}

	t.addBranchToCache(string(pod.UID), newENIs)

	log.Info("successfully created branch interfaces", "interfaces", newENIs,
		"security group used", securityGroups)

	return newENIs, nil
}

// reclaimOrphansOnAssociateFailure runs at most one describe for concurrent
// association failures on the same trunk.
func (t *trunkENI) reclaimOrphansOnAssociateFailure() {
	_, _, _ = t.reclaimGroup.Do(t.trunkENIId, func() (interface{}, error) {
		t.reclaimOrphans()
		return nil, nil
	})
}

// reclaimOrphans queues attached branch ENIs that have no local owner.
func (t *trunkENI) reclaimOrphans() {
	branchENIOrphanReclaimCount.WithLabelValues("triggered").Inc()
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(
		&t.trunkENIId, aws.String(t.instance.SubnetID()))
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("reclaim_orphans_describe").Inc()
		t.log.Error(err, "failed to list branch ENIs for orphan reclaim")
		return
	}

	t.lock.RLock()
	owned := make(map[string]struct{})
	for _, enis := range t.uidToBranchENIMap {
		for _, eni := range enis {
			owned[eni.ID] = struct{}{}
		}
	}
	for _, eni := range t.deleteQueue {
		owned[eni.ID] = struct{}{}
	}
	inFlight := make(map[string]struct{}, len(t.inFlightENIs))
	for id := range t.inFlightENIs {
		inFlight[id] = struct{}{}
	}
	t.lock.RUnlock()

	for _, branchInterface := range branchInterfaces {
		if branchInterface.NetworkInterfaceId == nil {
			continue
		}
		id := *branchInterface.NetworkInterfaceId
		if _, ok := owned[id]; ok {
			continue
		}
		if _, ok := inFlight[id]; ok {
			branchENIOrphanReclaimCount.WithLabelValues("skipped_in_flight").Inc()
			t.log.Info("skipping in-flight branch eni during orphan reclaim, create in progress", "eni", id)
			continue
		}

		vlanID, err := t.getVlanIdFromTag(branchInterface.TagSet)
		if err != nil || vlanID <= 0 || vlanID >= MaxAllocatableVlanIds {
			t.log.Info("skipping orphan branch eni with unusable vlan tag", "eni", id, "vlanID", vlanID)
			continue
		}

		t.log.Info("reclaiming orphan branch eni owned by no pod", "eni", id, "vlanID", vlanID)
		branchENIOrphanReclaimCount.WithLabelValues("reclaimed").Inc()
		t.markVlanAssigned(vlanID)
		t.pushENIToDeleteQueue(&ENIDetails{
			ID:                id,
			VlanID:            vlanID,
			deletionTimeStamp: time.Now(),
		})
	}
}

// DeleteBranchNetworkInterface deletes the branch network interface and returns an error in case of failure to delete
func (t *trunkENI) PushBranchENIsToCoolDownQueue(UID string) {
	t.recoveryGate.RLock()
	defer t.recoveryGate.RUnlock()

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
	t.recoveryGate.RLock()
	defer t.recoveryGate.RUnlock()

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
				t.pushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{eni})
				continue
			}
			t.log.V(1).Info("deleted eni successfully", "eni", eni, "deletion time", time.Now(),
				"pushed to queue time", eni.deletionTimeStamp)
		} else {
			// Since the current item is not cooled down so the items added after it would not be cooled down either
			t.pushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{eni})
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

func (t *trunkENI) getBranchInterfaceMap(eniList []*ENIDetails) map[string]*ENIDetails {
	eniMap := make(map[string]*ENIDetails)
	for _, eni := range eniList {
		eniMap[eni.ID] = eni
	}
	return eniMap
}

// decodeBranchInterfacesUsedByPod decodes the pod's branch ENI annotation and
// reports whether it is usable. usable is false only when the annotation exists
// but cannot be trusted: it does not decode, or it decodes to an entry with no ENI
// id. An absent annotation is usable with no entries - that pod simply owns
// nothing. Callers that rebuild a ledger from annotations alone must not treat an
// undecodable annotation as "no allocation", because the pod may in fact own an
// ENI that would then look unowned.
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
	t.recoveryGate.RLock()
	defer t.recoveryGate.RUnlock()

	t.pushENIsToFrontOfDeleteQueue(pod, eniList)
}

// pushENIsToFrontOfDeleteQueue is the ungated form, for callers that already hold
// the recovery gate. Taking it again here would be a recursive RLock, which
// deadlocks if a resync is waiting for the exclusive side.
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

func (t *trunkENI) markENIInFlight(id string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.inFlightENIs == nil {
		t.inFlightENIs = make(map[string]struct{})
	}
	t.inFlightENIs[id] = struct{}{}
}

func (t *trunkENI) clearENIsInFlight(ids []string) {
	if len(ids) == 0 {
		return
	}

	t.lock.Lock()
	defer t.lock.Unlock()
	for _, id := range ids {
		delete(t.inFlightENIs, id)
	}
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

	// Guard against ids outside the assignable range reaching the fixed-size
	// ledger. 0 is excluded because it is reserved, never handed out by
	// assignVlanId. Ids this controller assigns are always in range, so a hit here
	// can only come from corrupted external data (e.g. a tampered pod annotation)
	// or a write-side bug: it is an incident signal, not a normal event.
	if vlanId <= 0 || vlanId >= MaxAllocatableVlanIds {
		trunkENIOperationsErrCount.WithLabelValues("vlan_id_out_of_range").Inc()
		t.log.Error(fmt.Errorf("vlan id out of range"), "refusing to mark vlan as assigned", "vlan id", vlanId)
		return
	}
	t.usedVlanIds[vlanId] = true
}

// freeVlanId frees a vlan ID currently used by a network interface
func (t *trunkENI) freeVlanId(vlanId int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	// See markVlanAssigned. Excluding 0 also keeps the reserved slot reserved: a
	// stray free(0) must not put it back in circulation.
	if vlanId <= 0 || vlanId >= MaxAllocatableVlanIds {
		trunkENIOperationsErrCount.WithLabelValues("vlan_id_out_of_range").Inc()
		t.log.Error(fmt.Errorf("vlan id out of range"), "refusing to free vlan", "vlan id", vlanId)
		return
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
			return strconv.Atoi(*tag.Value)
		}
	}

	return 0, fmt.Errorf("failed to find vlan tag from the list of tags")
}

func (t *trunkENI) canCreateMore() bool {
	t.lock.RLock()
	defer t.lock.RUnlock()

	var usedBranches int
	for _, branches := range t.uidToBranchENIMap {
		usedBranches += len(branches)
	}

	if usedBranches+len(t.deleteQueue) < vpc.Limits[t.instance.Type()].BranchInterface {
		return true
	}
	return false
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
