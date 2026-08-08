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

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
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

	// shadowReuseWindow is the age limit for a recently-released branch ENI to count as a
	// would-have-been reuse hit in the Phase-2 shadow instrumentation (design doc section 4.2:
	// Option 2 evaluation). Deliberately a constant, not a flag: the shadow window is a
	// measurement definition, and comparing hit rates across clusters requires it to be fixed.
	shadowReuseWindow = 10 * time.Minute
	// maxShadowRecordsPerTrunk bounds the per-trunk recently-released record list (FIFO evict)
	// so the shadow instrumentation cannot grow memory unboundedly at high pod churn.
	maxShadowRecordsPerTrunk = 32

	// errorDrivenReclaimWindow is the minimum interval between two error-driven orphan reclaim
	// describes on the SAME trunk (M3, design doc section 2.4). The reclaim runs on the branch-ENI
	// addition FAILURE path, so it must be bounded: without this window a persistent EC2 error plus
	// pod-reconcile retries would turn every failure into a DescribeNetworkInterfaces call. One
	// describe per trunk per window is enough because a reclaim that found nothing will not find
	// anything on an immediate retry either, and a reclaim that did find orphans has already
	// enqueued them.
	errorDrivenReclaimWindow = 30 * time.Second
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

	// branchENIOrphanReclaimedCount counts orphan branch ENIs DISCOVERED by the orphan reclaim sweep -
	// branch ENIs attached to the trunk in EC2 but owned by no pod in the in-memory ledger, pushed to
	// the cooldown delete queue by ReconcileUnassignedBranchENIs. It makes the real orphan RATE
	// observable in Grafana (previously only a log line). Orphans arise only from rare EC2 API
	// failures, so a sustained non-zero rate here is a signal worth alerting on.
	branchENIOrphanReclaimedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_eni_orphan_reclaimed_total",
			Help: "The number of orphan branch ENIs (attached to the trunk but owned by no pod) discovered and pushed to the delete queue by the orphan reclaim sweep",
		},
		[]string{"attribute"},
	)

	// branchENIDeleteForgottenCount counts branch ENIs abandoned from the delete queue after exhausting
	// MaxDeleteRetries ("forgetting eni as max retries exceeded"). This is a class-2 PRODUCER of orphans:
	// a forgotten ENI stays attached in EC2 with no pod owner and is exactly what a later orphan reclaim
	// sweep rediscovers. Pairing this with branch_eni_orphan_reclaimed_total makes both the production
	// and the reclaim of orphans observable.
	branchENIDeleteForgottenCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_eni_delete_forgotten_total",
			Help: "The number of branch ENIs forgotten (dropped from the delete queue) after exceeding the maximum delete retries",
		},
		[]string{"attribute"},
	)

	// branchLedgerVerifyCount counts runs of the lazy branch-ledger verification gate on hydrated
	// trunks (result="verified"|"error"). The gate closes the VLAN-reuse race after a hydrate-based
	// re-init: the in-memory ledger only knows pod-owned branch ENIs, so an orphaned branch ENI
	// still occupying its VLAN on the trunk in EC2 would otherwise collide with a new allocation.
	// A "verified" sample proves the gate ran before the first allocation on a hydrated trunk.
	branchLedgerVerifyCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_ledger_verify_total",
			Help: "The number of lazy branch-ledger verification gate runs before the first allocation on hydrated trunks",
		},
		[]string{"result"},
	)

	// branchLedgerVerifyOrphanCount counts orphan branch ENIs discovered specifically by the
	// verification gate (as opposed to the periodic reclaim sweep, which increments
	// branch_eni_orphan_reclaimed_total). Restart-produced orphans (evaporated delete queue)
	// surface exactly here, at the first allocation after a hydrate-based re-init, so this
	// metric measures restart-orphan volume at the precise moment Phase-2 reuse (design doc
	// section 4.2) would adopt them instead of deleting them.
	branchLedgerVerifyOrphanCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "branch_ledger_verify_orphans_total",
			Help: "The number of orphan branch ENIs discovered by the lazy branch-ledger verification gate on hydrated trunks",
		},
	)

	// orphanReuseShadowHitCount is Phase-2 shadow instrumentation (design doc section 4.2): it
	// counts pod allocations that COULD have been served by reusing a recently-released branch
	// ENI on the same trunk, without changing any allocation behavior. sg_match="exact" means a
	// released ENI with identical security groups was available within shadowReuseWindow (reuse
	// would cost zero EC2 calls); sg_match="mismatch" means only differently-SG'd ENIs were
	// available (reuse would cost one ModifyNetworkInterfaceAttribute). The hit rate quantifies
	// the steady-state EC2 call savings of Option 2 before building the reuse pool.
	orphanReuseShadowHitCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orphan_reuse_shadow_hit_total",
			Help: "The number of branch ENI allocations that could have reused a recently-released ENI on the same trunk (observation only, no behavior change)",
		},
		[]string{"sg_match"},
	)

	// branchENIDeleteQueueDedupCount counts delete-queue enqueue attempts skipped because an
	// entry with the same ENI ID was already queued (M5 G2, design doc section 2.6). A duplicate
	// entry would later run deleteENI twice and double-free the ENI's VLAN - releasing a VLAN
	// that may meanwhile belong to a NEW pod's branch ENI (hazard H-B). A non-zero rate here
	// means the dedup guard is absorbing real high-churn races.
	branchENIDeleteQueueDedupCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "branch_eni_delete_queue_dedup_total",
			Help: "The number of delete queue enqueue attempts skipped because the branch ENI is already in the delete queue",
		},
	)

	// branchENIVlanReuseCooldownBlockedCount counts assignVlanId calls that skipped at least one
	// otherwise-free VLAN because it was still inside its M1 reuse cooldown window (design doc
	// section 2.2). This is the signal that would justify lowering reuseCooldown below its current
	// floor (design doc section 5.4) once it shows a sustained non-trivial rate.
	branchENIVlanReuseCooldownBlockedCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "branch_eni_vlan_reuse_cooldown_blocked_total",
			Help: "The number of VLAN allocation attempts that skipped an otherwise-free VLAN still inside its M1 reuse cooldown window",
		},
	)

	// branchENIErrorDrivenReclaimCount counts error-driven orphan reclaim attempts (M3, design doc
	// section 2.4) - the reactive correctness floor that runs when a branch ENI could not be added to
	// the trunk. result="reclaimed" means the describe ran and found at least one orphan (the wedge
	// was real and is now broken), "clean" means it ran and found none (EC2 agreed with the ledger,
	// so the failure was something else), "skipped_window" means another reclaim ran on this trunk
	// within errorDrivenReclaimWindow, and "describe_error" means the reclaim describe itself failed.
	// The error_class label is BEST-EFFORT observability only and never gates the reclaim.
	branchENIErrorDrivenReclaimCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_eni_error_driven_reclaim_total",
			Help: "The number of error-driven orphan reclaim attempts triggered by a failed branch ENI addition, by outcome and best-effort error class",
		},
		[]string{"result", "error_class"},
	)

	// branchENIErrorDrivenOrphanCount counts orphan branch ENIs discovered specifically by the
	// error-driven reclaim path (M3). Deliberately separate from branch_ledger_verify_orphans_total
	// (the proactive gate, M2) and branch_eni_orphan_reclaimed_total (the slow sweep, M4) so the
	// three discovery sources stay separable in Grafana: a non-zero rate here means orphans are
	// actually wedging live allocations, which is a stronger signal than the sweep finding them idle.
	branchENIErrorDrivenOrphanCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "branch_eni_error_driven_orphans_total",
			Help: "The number of orphan branch ENIs discovered by the error-driven reclaim path after a failed branch ENI addition",
		},
	)

	prometheusRegistered = false
)

// branchENIErrorDrivenReclaimCount result label values.
const (
	reclaimResultReclaimed     = "reclaimed"
	reclaimResultClean         = "clean"
	reclaimResultSkippedWindow = "skipped_window"
	reclaimResultDescribeError = "describe_error"
)

// branchENIErrorDrivenReclaimCount error_class label values. BEST-EFFORT classification of the
// EC2 failure, used for observability only - it never decides whether the reclaim runs. AWS does
// not document per-API error codes for AssociateTrunkInterface, so an unrecognized error is
// reported as "other" rather than being treated as ineligible (see reclaimErrorClass).
const (
	reclaimErrorClassCapacity  = "capacity"
	reclaimErrorClassVlanInUse = "vlan_in_use"
	reclaimErrorClassOther     = "other"
)

// orphanReuseShadowHitCount sg_match label values.
const (
	shadowSGMatchExact    = "exact"
	shadowSGMatchMismatch = "mismatch"
)

// branchLedgerVerifyCount result label values.
const (
	ledgerVerifyResultVerified = "verified"
	ledgerVerifyResultError    = "error"
)

type TrunkENI interface {
	// InitTrunk initializes trunk interface
	InitTrunk(instance ec2.EC2Instance, pods []v1.Pod) error
	// InitTrunkFromStatus initializes trunk cache from CNINode status and pod annotations.
	InitTrunkFromStatus(status *rcv1alpha1.TrunkInterface, pods []v1.Pod) error
	// ReconcileUnassignedBranchENIs discovers branch ENIs missing pod annotations and pushes them to the delete queue.
	ReconcileUnassignedBranchENIs() (bool, error)
	// CNINodeStatus returns a snapshot of trunk ENI state persisted in CNINode status.
	// v1 never populates Branches (per-pod state would write-amplify at pod churn) nor
	// MacAddress/DeviceIndex (no v1 read path needs them).
	CNINodeStatus() *rcv1alpha1.TrunkInterface
	// CreateAndAssociateBranchENIs creates and associate branch interface/s to trunk interface
	CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error)
	// PushBranchENIsToCoolDownQueue pushes the branch interface belonging to the pod to the cool down queue
	PushBranchENIsToCoolDownQueue(UID string)
	// DeleteCooledDownENIs deletes the interfaces that have been sitting in the queue for cool down period
	DeleteCooledDownENIs()
	// Reconcile compares the cache state with the list of pods to identify events that were missed and clean up the dangling interfaces
	Reconcile(pods []v1.Pod) bool
	// PushENIsToFrontOfDeleteQueue pushes the eni network interfaces to the front of the delete queue
	PushENIsToFrontOfDeleteQueue(*v1.Pod, []*ENIDetails)
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
	// eniToPodUID retains ownership after an ENI leaves uidToBranchENIMap so asynchronous VLAN
	// lifecycle logs can still identify the pod. It is controller-local and never serialized.
	eniToPodUID map[string]string
	// deleteQueue is the queue of ENIs that are being cooled down before being deleted
	deleteQueue []*ENIDetails
	// nodeName tag is the tag added to trunk and branch ENIs created on the node
	nodeIDTag []ec2types.Tag
	// shadowReleased is the Phase-2 shadow-instrumentation record of recently-released branch
	// ENIs on this trunk (design doc section 4.2). Appended when an ENI enters the delete queue
	// from pod release (PushBranchENIsToCoolDownQueue) or orphan discovery
	// (pushUnassignedBranchInterfacesToDeleteQueue); read by the would-have-been reuse check in
	// CreateAndAssociateBranchENIs. Bounded by maxShadowRecordsPerTrunk (FIFO evict), entries
	// expire lazily on check. Observation only: it never affects allocation behavior. Guarded by lock.
	shadowReleased []shadowReleaseRecord
	// branchLedgerVerified records whether the in-memory VLAN/branch ledger has been verified
	// against EC2 (guarded by lock). The EC2 init path (InitTrunk) lists all branch ENIs, so it
	// sets this true. The hydrate path (InitTrunkFromStatus) rebuilds the ledger from pod
	// annotations only, so an orphaned branch ENI (its pod deleted right before the controller
	// restarted, in-memory delete queue lost) still occupies its VLAN on the trunk in EC2 while
	// the ledger believes that VLAN is free; assigning it to a new pod would fail
	// AssociateTrunkInterface. CreateAndAssociateBranchENIs therefore verifies the ledger from
	// EC2 (verifyBranchLedger) before the first allocation on a hydrated trunk.
	branchLedgerVerified bool
	// pendingCreates is the set of branch ENI IDs that exist in EC2 but are not yet in
	// uidToBranchENIMap (M5 G1, design doc section 2.6). CreateAndAssociateBranchENIs adds an ID
	// right after CreateNetworkInterface returns and removes it on EVERY exit for that ENI:
	// success (addBranchToCache) or failure (PushENIsToFrontOfDeleteQueue). The ledger-verify
	// gate and the orphan reclaim sweep never classify a pending ENI as an orphan - without this,
	// an in-flight create in the Associate-to-cache window could be deleted from under a live pod
	// (hazard H-A). Only needs to live within a single controller lifetime. Guarded by lock.
	pendingCreates map[string]struct{}
	// lastErrorDrivenReclaim is when the error-driven orphan reclaim (M3, design doc section 2.4)
	// last ran a describe for this trunk. It bounds that failure-path reclaim to at most one
	// describe per errorDrivenReclaimWindow so a persistent EC2 error cannot turn pod-reconcile
	// retries into a describe storm. Guarded by lock.
	lastErrorDrivenReclaim time.Time
	// vlanOwner maps an assigned VLAN ID to the branch ENI ID that currently owns it (M5 G3,
	// design doc section 2.6). Set wherever a VLAN is assigned or marked with a known ENI ID;
	// cleared by freeVlanId. freeVlanId refuses to free a VLAN owned by a different ENI, so a
	// duplicate delete cannot release a VLAN that meanwhile belongs to a NEW pod's branch ENI
	// (hazard H-B defense in depth). An absent/empty owner preserves legacy free behavior.
	// Reserved VLAN 0 never gets an owner and is never freed. Guarded by lock.
	vlanOwner map[int]string
	// vlanReleasedAt records, for a VLAN ID that has been released by the M1 immediate-disassociate
	// step (design doc section 2.2), the time its ENI entered the delete queue
	// (ENIDetails.deletionTimeStamp - NOT the time disassociation itself completed, so the VLAN
	// reuse cooldown reproduces today's cooldown timing exactly regardless of how long
	// disassociation took to succeed). assignVlanId refuses to hand out a free VLAN still inside
	// this window; the entry is cleared once the VLAN is reassigned. Guarded by lock.
	vlanReleasedAt map[int]time.Time
}

// shadowReleaseRecord is one recently-released branch ENI observed by the Phase-2 shadow reuse
// instrumentation: the security groups it carried (sorted) and when it was released.
type shadowReleaseRecord struct {
	sortedSecurityGroups []string
	releasedAt           time.Time
}

// recordShadowReleaseLocked appends a shadow release record for the given security groups,
// FIFO-evicting beyond maxShadowRecordsPerTrunk. Caller must hold the trunk lock.
func (t *trunkENI) recordShadowReleaseLocked(securityGroups []string) {
	sorted := slices.Clone(securityGroups)
	slices.Sort(sorted)
	t.shadowReleased = append(t.shadowReleased, shadowReleaseRecord{
		sortedSecurityGroups: sorted,
		releasedAt:           time.Now(),
	})
	if len(t.shadowReleased) > maxShadowRecordsPerTrunk {
		t.shadowReleased = t.shadowReleased[len(t.shadowReleased)-maxShadowRecordsPerTrunk:]
	}
}

// observeShadowReuse checks whether a pod allocation requesting the given security groups could
// have been served by a recently-released branch ENI on this trunk, incrementing
// orphan_reuse_shadow_hit_total accordingly (sg_match="exact" preferred over "mismatch").
// Expired records (older than shadowReuseWindow) are pruned lazily here. Observation only:
// records are not consumed on a hit, since without a real pool there is no claim to model - the
// metric measures availability of reusable ENIs, and the FIFO cap plus expiry bound any over-count.
func (t *trunkENI) observeShadowReuse(requestedSecurityGroups []string) {
	sorted := slices.Clone(requestedSecurityGroups)
	slices.Sort(sorted)

	t.lock.Lock()
	defer t.lock.Unlock()

	cutoff := time.Now().Add(-shadowReuseWindow)
	live := t.shadowReleased[:0]
	exact, mismatch := false, false
	for _, rec := range t.shadowReleased {
		if rec.releasedAt.Before(cutoff) {
			continue
		}
		live = append(live, rec)
		if slices.Equal(rec.sortedSecurityGroups, sorted) {
			exact = true
		} else {
			mismatch = true
		}
	}
	t.shadowReleased = live

	if exact {
		orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchExact).Inc()
	} else if mismatch {
		orphanReuseShadowHitCount.WithLabelValues(shadowSGMatchMismatch).Inc()
	}
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
	// securityGroups are the security group ids the branch ENI was created with. In-memory
	// only (excluded from the pod-annotation JSON, which is a wire format shared with the CNI
	// plugin); consumed by the Phase-2 shadow reuse instrumentation on pod release. Empty for
	// ENIs rebuilt from annotations or discovered via EC2 describe (SGs unknown there).
	securityGroups []string
	// slotReleased records whether this ENI's trunk slot has been positively observed as released
	// (M1, design doc section 2.2): set once DisassociateTrunkInterface succeeds (or EC2 reports
	// the association already gone), or as a fallback once the ENI is confirmed deleted (covers a
	// sweep-discovered orphan with no known AssociationID, or a disassociate that never
	// succeeded). Never inferred from AssociationID=="" - a sweep-discovered orphan has no known
	// AssociationID but is still attached in EC2, so canCreateMore must keep counting it as
	// occupying a slot until release is positively observed (over-counting is safe, under-counting
	// is not).
	slotReleased bool
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
		eniToPodUID:       make(map[string]string),
		pendingCreates:    make(map[string]struct{}),
		vlanOwner:         make(map[int]string),
		vlanReleasedAt:    make(map[int]time.Time),
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
		metrics.Registry.MustRegister(branchENIOrphanReclaimedCount)
		metrics.Registry.MustRegister(branchENIDeleteForgottenCount)
		metrics.Registry.MustRegister(branchLedgerVerifyCount)
		metrics.Registry.MustRegister(branchLedgerVerifyOrphanCount)
		metrics.Registry.MustRegister(orphanReuseShadowHitCount)
		metrics.Registry.MustRegister(branchENIDeleteQueueDedupCount)
		metrics.Registry.MustRegister(branchENIVlanReuseCooldownBlockedCount)
		metrics.Registry.MustRegister(branchENIErrorDrivenReclaimCount)
		metrics.Registry.MustRegister(branchENIErrorDrivenOrphanCount)

		prometheusRegistered = true
	}
}

func (t *trunkENI) InitTrunkFromStatus(status *rcv1alpha1.TrunkInterface, podList []v1.Pod) error {
	if status == nil || status.ID == "" {
		return fmt.Errorf("missing trunk ENI ID in CNINode status")
	}
	if status.SubnetID != "" && status.SubnetID != t.instance.SubnetID() {
		return fmt.Errorf("trunk subnet %s from CNINode status does not match instance subnet %s",
			status.SubnetID, t.instance.SubnetID())
	}

	t.lock.Lock()
	defer t.lock.Unlock()

	t.trunkENIId = status.ID
	t.uidToBranchENIMap = make(map[string][]*ENIDetails)
	t.eniToPodUID = make(map[string]string)
	t.deleteQueue = nil
	// The hydrated ledger only knows pod-owned branch ENIs; it has not been checked against the
	// trunk's actual branch ENIs in EC2. Leave it unverified so the first allocation runs
	// verifyBranchLedger before any VLAN is handed out.
	t.branchLedgerVerified = false

	for _, pod := range podList {
		pod := pod
		eniListFromPod := t.getBranchInterfacesUsedByPod(&pod)
		if len(eniListFromPod) == 0 {
			continue
		}

		for _, eni := range eniListFromPod {
			t.eniToPodUID[eni.ID] = string(pod.UID)
			if err := t.markVlanAssignedWithOwnerLocked(eni.VlanID, eni.ID); err != nil {
				return fmt.Errorf("invalid VLAN ID in pod annotation for pod %s/%s: %w",
					pod.Namespace, pod.Name, err)
			}
		}
		t.uidToBranchENIMap[string(pod.UID)] = eniListFromPod
	}

	t.log.V(1).Info("successfully initialized trunk cache from CNINode status",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)
	return nil
}

func (t *trunkENI) ReconcileUnassignedBranchENIs() (bool, error) {
	t.lock.RLock()
	trunkENIID := t.trunkENIId
	// M5 G1+G2 (design doc section 2.6): the "known" set is ledger UNION delete queue UNION
	// pending creates. An ENI awaiting deletion is being processed, not an orphan (hazard H-B),
	// and an ENI still being created must never be deleted from under a live pod (hazard H-A).
	knownBranchENIs := t.knownBranchENIsLocked()
	t.lock.RUnlock()

	if trunkENIID == "" {
		return false, fmt.Errorf("missing trunk ENI ID")
	}

	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&trunkENIID, aws.String(t.instance.SubnetID()))
	if err != nil {
		return false, err
	}

	unassignedBranchInterfaces := make(map[string]*ec2types.NetworkInterface)
	for _, branchInterface := range branchInterfaces {
		if branchInterface.NetworkInterfaceId == nil {
			continue
		}
		branchENIID := *branchInterface.NetworkInterfaceId
		if _, known := knownBranchENIs[branchENIID]; known {
			continue
		}
		unassignedBranchInterfaces[branchENIID] = branchInterface
	}

	return t.pushUnassignedBranchInterfacesToDeleteQueue(unassignedBranchInterfaces), nil
}

// reclaimErrorClass classifies a failed branch-ENI addition for METRIC LABELLING ONLY.
//
// It deliberately does NOT gate the reclaim. AWS does not document per-API error codes for
// AssociateTrunkInterface, so any hard-coded allowlist of codes would be unverifiable and, worse,
// would fail SILENTLY: a renamed or previously unseen code would mean the reclaim never runs and the
// node stays wedged forever (design doc hazard E4) - the exact bug M3 exists to fix. Missing a
// trigger is unbounded damage; an extra describe is one cheap read, already bounded by
// errorDrivenReclaimWindow. So every addition failure is eligible and this function only records
// what the error looked like.
func reclaimErrorClass(err error) string {
	if err == nil {
		return reclaimErrorClassOther
	}
	msg := strings.ToLower(err.Error())
	// API request throttling also says "limit exceeded" but is a rate problem, not a trunk-capacity
	// problem. Classify it as "other" so the capacity label stays meaningful; it is still eligible
	// for reclaim (this function never gates).
	if strings.Contains(msg, "requestlimitexceeded") || strings.Contains(msg, "throttl") {
		return reclaimErrorClassOther
	}
	switch {
	case strings.Contains(msg, "capacity"), strings.Contains(msg, "limitexceeded"),
		strings.Contains(msg, "limit exceeded"):
		return reclaimErrorClassCapacity
	case strings.Contains(msg, "vlan"), strings.Contains(msg, "duplicate"),
		strings.Contains(msg, "already in use"), strings.Contains(msg, "alreadyexists"):
		return reclaimErrorClassVlanInUse
	default:
		return reclaimErrorClassOther
	}
}

// reclaimOrphansAfterAddFailure is the M3 reactive correctness floor (design doc section 2.4).
//
// When a branch ENI could not be added to the trunk, EC2 and the in-memory ledger may disagree: an
// orphan (a restart leftover, a delete-retry "forgotten" ENI, or a create/associate partial) can be
// attached in EC2 occupying a real branch slot or VLAN while the ledger believes that resource is
// free. canCreateMore only counts the ledger, so without this path the node retries forever -
// creating and deleting a fresh ENI each time while the orphan keeps the slot (hazard E4).
//
// This runs ONE describe, classifies orphans through the shared M5 known set (ledger UNION delete
// queue UNION pending creates, so an in-flight create or an ENI already awaiting deletion is never
// reclaimed - hazards H-A/H-B), and enqueues what it finds through the existing dedup-aware delete
// path. It never blocks the allocation: the caller still returns its original error so the pod
// reconcile retries against the reclaimed capacity.
//
// It does NOT replace the proactive gate (M2, verifyBranchLedger). Per the captain's A-tier decision
// recorded in design doc section 2.4, both are kept: the gate avoids hitting this failure path at
// all, while this path guarantees recovery whenever an orphan actually wedges a live allocation.
//
// Locking mirrors verifyBranchLedger: the EC2 describe runs unlocked, ledger mutation takes the lock.
func (t *trunkENI) reclaimOrphansAfterAddFailure(cause error) {
	errClass := reclaimErrorClass(cause)

	t.lock.Lock()
	if !t.lastErrorDrivenReclaim.IsZero() && time.Since(t.lastErrorDrivenReclaim) < errorDrivenReclaimWindow {
		t.lock.Unlock()
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultSkippedWindow, errClass).Inc()
		return
	}
	// Stamp BEFORE the describe so concurrent failures on this trunk collapse onto one call.
	t.lastErrorDrivenReclaim = time.Now()
	trunkENIID := t.trunkENIId
	t.lock.Unlock()

	if trunkENIID == "" {
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultDescribeError, errClass).Inc()
		t.log.Error(fmt.Errorf("missing trunk ENI ID"), "skipping error-driven orphan reclaim")
		return
	}

	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&trunkENIID, aws.String(t.instance.SubnetID()))
	if err != nil {
		// Never mask the original allocation failure: log and return so the caller's error stands.
		trunkENIOperationsErrCount.WithLabelValues("error_driven_reclaim_describe").Inc()
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultDescribeError, errClass).Inc()
		t.log.Error(err, "error-driven orphan reclaim describe failed", "trunk", trunkENIID)
		return
	}

	t.lock.Lock()
	knownBranchENIs := t.knownBranchENIsLocked()
	unassignedBranchInterfaces := make(map[string]*ec2types.NetworkInterface)
	for _, branchInterface := range branchInterfaces {
		if branchInterface.NetworkInterfaceId == nil {
			continue
		}
		branchENIID := *branchInterface.NetworkInterfaceId
		if _, known := knownBranchENIs[branchENIID]; known {
			continue
		}
		unassignedBranchInterfaces[branchENIID] = branchInterface
	}
	t.lock.Unlock()

	if len(unassignedBranchInterfaces) == 0 {
		branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultClean, errClass).Inc()
		t.log.Info("error-driven orphan reclaim found no orphans; EC2 agrees with the ledger",
			"trunk", trunkENIID, "attachedBranchENIs", len(branchInterfaces))
		return
	}

	// Takes its own lock; enqueues through the shared dedup-aware path so an ENI already queued is
	// not duplicated and its VLAN is re-marked idempotently.
	t.pushUnassignedBranchInterfacesToDeleteQueue(unassignedBranchInterfaces)

	branchENIErrorDrivenOrphanCount.Add(float64(len(unassignedBranchInterfaces)))
	branchENIErrorDrivenReclaimCount.WithLabelValues(reclaimResultReclaimed, errClass).Inc()
	t.log.Info("error-driven orphan reclaim enqueued orphans after a failed branch ENI addition",
		"trunk", trunkENIID, "orphans", len(unassignedBranchInterfaces),
		"attachedBranchENIs", len(branchInterfaces), "cause", cause)
}

// knownBranchENIsLocked builds the set of branch ENI IDs the controller knows about: the
// pod-owned ledger UNION the delete queue UNION the pending-creates set (M5 G1+G2, design doc
// section 2.6). Only an attached branch ENI OUTSIDE this set is an orphan. Caller must hold the
// trunk lock (read or write).
func (t *trunkENI) knownBranchENIsLocked() map[string]struct{} {
	knownBranchENIs := make(map[string]struct{})
	for _, branchENIs := range t.uidToBranchENIMap {
		for _, eni := range branchENIs {
			knownBranchENIs[eni.ID] = struct{}{}
		}
	}
	for _, eni := range t.deleteQueue {
		knownBranchENIs[eni.ID] = struct{}{}
	}
	for eniID := range t.pendingCreates {
		knownBranchENIs[eniID] = struct{}{}
	}
	return knownBranchENIs
}

func (t *trunkENI) CNINodeStatus() *rcv1alpha1.TrunkInterface {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return &rcv1alpha1.TrunkInterface{
		ID:             t.trunkENIId,
		SubnetID:       t.instance.SubnetID(),
		SecurityGroups: slices.Clone(t.instance.CurrentInstanceSecurityGroups()),
	}
}

// InitTrunk initializes the trunk network interface and all it's associated branch network interfaces by making calls
// to EC2 API
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

		// A freshly created trunk has no branch ENIs, so the (empty) ledger is verified.
		t.setBranchLedgerVerified()
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

	// From the list of pods on the given node, and the branch ENIs from EC2 API call rebuild the internal cache
	for _, pod := range podList {
		pod := pod // Fix gosec G601, so we can use &node
		eniListFromPod := t.getBranchInterfacesUsedByPod(&pod)
		if len(eniListFromPod) == 0 {
			continue
		}
		var branchENIs []*ENIDetails
		for _, eni := range eniListFromPod {
			t.rememberPodUID(eni.ID, string(pod.UID))
			_, isPresent := associatedBranchInterfaces[eni.ID]
			if !isPresent {
				t.log.Error(fmt.Errorf("eni allocated to pod not found in ec2"), "eni not found", "eni", eni)
				trunkENIOperationsErrCount.WithLabelValues("get_branch_eni_from_ec2").Inc()
				continue
			}
			// Mark the Vlan ID from the pod's annotation
			t.markVlanAssignedWithOwner(eni.VlanID, eni.ID)

			branchENIs = append(branchENIs, eni)
			delete(associatedBranchInterfaces, eni.ID)
		}
		t.uidToBranchENIMap[string(pod.UID)] = branchENIs
	}

	t.pushUnassignedBranchInterfacesToDeleteQueue(associatedBranchInterfaces)

	// The EC2 path listed every branch ENI on the trunk, so the ledger reflects EC2 reality.
	t.setBranchLedgerVerified()

	log.V(1).Info("successfully initialized trunk with all associated branch interfaces",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)

	return nil
}

// setBranchLedgerVerified marks the in-memory branch/VLAN ledger as verified against EC2.
func (t *trunkENI) setBranchLedgerVerified() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.branchLedgerVerified = true
}

// verifyBranchLedger is the lazy correctness gate for hydrated trunks. InitTrunkFromStatus rebuilds
// the ledger from pod annotations only, so a branch ENI orphaned right around the controller restart
// (pod deleted, in-memory delete queue lost) still occupies its VLAN on the trunk in EC2 while the
// hydrated ledger believes that VLAN is free - handing it to a new pod would fail
// AssociateTrunkInterface. Before the first allocation on an unverified ledger, this lists the
// trunk's branch ENIs from EC2 (the same describe the orphan sweep uses), marks every attached
// ENI's VLAN as used, enqueues the unowned ones for deletion, and only then flips
// branchLedgerVerified. On describe failure the ledger stays unverified and the error propagates so
// the allocation fails and the pod reconcile retries - never allocate on an unverified ledger.
// Locking mirrors ReconcileUnassignedBranchENIs: the EC2 describe runs without the lock, the ledger
// mutation takes the lock.
func (t *trunkENI) verifyBranchLedger() error {
	t.lock.RLock()
	verified := t.branchLedgerVerified
	trunkENIID := t.trunkENIId
	t.lock.RUnlock()

	if verified {
		return nil
	}

	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&trunkENIID, aws.String(t.instance.SubnetID()))
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("verify_branch_ledger").Inc()
		branchLedgerVerifyCount.WithLabelValues(ledgerVerifyResultError).Inc()
		return fmt.Errorf("verifying branch ledger from EC2, %w", err)
	}

	t.lock.Lock()
	if t.branchLedgerVerified {
		// A concurrent allocation completed the verification while we were describing.
		t.lock.Unlock()
		return nil
	}
	// Under the lock: mark EVERY attached branch ENI's VLAN as used (same tag parsing and
	// reserved-VLAN-0 fallback as the orphan delete-queue path) and partition the ENIs into
	// known and orphaned. Marking must complete before branchLedgerVerified flips so no
	// concurrent allocation can grab a VLAN that is occupied in EC2. Owned ENIs' VLANs were
	// already marked by hydrate from the pod annotation; markVlanAssignedLocked is idempotent,
	// so re-marking from the tag is harmless and covers annotation/tag divergence.
	// M5 G1+G2 (design doc section 2.6): the "known" set is ledger UNION delete queue UNION
	// pending creates - an ENI awaiting deletion (hazard H-B) or still being created (hazard
	// H-A) must never be classified as an orphan here.
	knownBranchENIs := t.knownBranchENIsLocked()
	unassignedBranchInterfaces := make(map[string]*ec2types.NetworkInterface)
	for _, branchInterface := range branchInterfaces {
		if branchInterface.NetworkInterfaceId == nil {
			continue
		}
		branchENIID := *branchInterface.NetworkInterfaceId
		vlanId, err := t.getVlanIdFromTag(branchInterface.TagSet)
		if err != nil || vlanId < 0 || vlanId >= MaxAllocatableVlanIds {
			// Same fallback as pushUnassignedBranchInterfacesToDeleteQueue: reserved VLAN 0 is
			// permanently marked, so there is nothing extra to reserve for this ENI.
			t.log.Info("could not determine a valid vlan id at ledger verification, treating as reserved vlan id 0",
				"interface", branchENIID, "error", err)
			vlanId = 0
		}
		if err := t.markVlanAssignedWithOwnerLocked(vlanId, branchENIID); err != nil {
			t.log.Error(err, "failed to mark vlan id at ledger verification", "interface", branchENIID)
		}
		if _, known := knownBranchENIs[branchENIID]; !known {
			unassignedBranchInterfaces[branchENIID] = branchInterface
		}
	}
	t.branchLedgerVerified = true
	t.lock.Unlock()

	// Orphans: enqueue for deletion through the shared path (it takes its own locks, so it is
	// called outside the critical section above; its VLAN re-mark is an idempotent no-op here).
	t.pushUnassignedBranchInterfacesToDeleteQueue(unassignedBranchInterfaces)

	// Gate-discovered orphans measured separately from the periodic sweep's discoveries:
	// restart-produced orphans surface exactly here (design doc section 4.2).
	branchLedgerVerifyOrphanCount.Add(float64(len(unassignedBranchInterfaces)))

	branchLedgerVerifyCount.WithLabelValues(ledgerVerifyResultVerified).Inc()
	t.log.Info("verified hydrated branch ledger against EC2 before first allocation",
		"trunk", trunkENIID, "attachedBranchENIs", len(branchInterfaces), "orphans", len(unassignedBranchInterfaces))
	return nil
}

// Reconcile reconciles the state from the API Server to the internal cache of EC2 Branch Interfaces, if the controller
// missed some delete events the reconcile method will perform cleanup for the dangling interfaces
func (t *trunkENI) Reconcile(pods []v1.Pod) bool {
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

	// On a hydrated trunk, verify the ledger against EC2 before assigning any VLAN. Failing the
	// verification fails the allocation (the pod reconcile retries); allocating on an unverified
	// ledger could hand out a VLAN still occupied by an orphaned branch ENI in EC2.
	if err := t.verifyBranchLedger(); err != nil {
		log.Error(err, "failed to verify the branch ledger against EC2, not allocating")
		return nil, err
	}

	// If the security group is empty use the instance security group
	if securityGroups == nil || len(securityGroups) == 0 {
		securityGroups = t.instance.CurrentInstanceSecurityGroups()
	}

	// Phase-2 shadow instrumentation (design doc section 4.2): record whether this allocation
	// could have reused a recently-released branch ENI. Observation only, no behavior change.
	t.observeShadowReuse(securityGroups)

	connectionTrackingSpec := t.getConnectionTrackingSpec()

	var newENIs []*ENIDetails
	var err error
	var nwInterface *ec2types.NetworkInterface
	var vlanID int

	for i := 0; i < eniCount; i++ {
		// Assign VLAN
		vlanID, err = t.assignVlanId(string(pod.UID))
		if err != nil {
			err = fmt.Errorf("assigning vlad id, %w", err)
			trunkENIOperationsErrCount.WithLabelValues("assign_vlan_id").Inc()
			// M3 (design doc section 2.4): the ledger has no free VLAN. Orphaned branch ENIs hold
			// VLANs the ledger marks used, so reclaiming them is what eventually frees one.
			t.reclaimOrphansAfterAddFailure(err)
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
			// The ENI was never created, so the VLAN has no owner yet (legacy ownerless free).
			t.freeVlanId(vlanID, "", string(pod.UID))
			branchENIOperationsFailureCount.WithLabelValues("creating_branch_eni_failed").Inc()
			break
		} else {
			branchENIOperationsSuccessCount.WithLabelValues("created_branch_eni_succeeded").Inc()
		}

		t.log.Info("assigned VLAN ID to branch ENI",
			"vlanID", vlanID, "eniID", *nwInterface.NetworkInterfaceId, "podUID", string(pod.UID))

		// M5 G1 (design doc section 2.6): the ENI now exists in EC2 but is not yet in
		// uidToBranchENIMap. Record it as in-flight so the ledger-verify gate and the orphan
		// reclaim sweep never classify it as an orphan (hazard H-A). Removed on every exit for
		// this ENI: success via addBranchToCache, failure via PushENIsToFrontOfDeleteQueue.
		// The ENI ID is known now, so also record it as the owner of its VLAN (M5 G3).
		t.addPendingCreate(*nwInterface.NetworkInterfaceId, vlanID, string(pod.UID))

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
			securityGroups: slices.Clone(securityGroups),
		}
		newENIs = append(newENIs, newENI)

		// Associate Branch to trunk
		var associationOutput *awsEc2.AssociateTrunkInterfaceOutput
		associationOutput, err = t.ec2ApiHelper.AssociateBranchToTrunk(&t.trunkENIId, nwInterface.NetworkInterfaceId, vlanID)
		if err != nil {
			err = fmt.Errorf("associating branch to trunk, %w", err)
			trunkENIOperationsErrCount.WithLabelValues("associate_branch").Inc()
			// M3 (design doc section 2.4): the branch could not be added to the trunk, so EC2 and
			// the ledger disagree - an orphan may be holding the slot or this VLAN. Reclaim it so
			// the pod's retry has capacity, instead of looping create/delete forever (hazard E4).
			// This ENI is still in pendingCreates here, so the shared known set keeps the reclaim
			// from enqueueing the ENI we are about to hand to the delete queue ourselves.
			t.reclaimOrphansAfterAddFailure(err)
			break
		}
		newENI.AssociationID = *associationOutput.InterfaceAssociation.AssociationId
	}

	if err != nil {
		log.Error(err, "failed to create ENI, moving the ENI to delete list")
		// Moving to delete list, because it has all the retrying logic in case of failure
		t.PushENIsToFrontOfDeleteQueue(pod, newENIs)
		return nil, err
	}

	t.addBranchToCache(string(pod.UID), newENIs)

	log.Info("successfully created branch interfaces", "interfaces", newENIs,
		"security group used", securityGroups)

	return newENIs, nil
}

// DeleteBranchNetworkInterface deletes the branch network interface and returns an error in case of failure to delete
func (t *trunkENI) PushBranchENIsToCoolDownQueue(UID string) {
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
		t.rememberPodUIDLocked(eni.ID, UID)
		eni.deletionTimeStamp = time.Now()
		t.deleteQueue = append(t.deleteQueue, eni)
		// Phase-2 shadow instrumentation: this ENI is exactly what a reuse pool would hold.
		t.recordShadowReleaseLocked(eni.securityGroups)
	}

	delete(t.uidToBranchENIMap, UID)

	t.log.Info("moved branch network interfaces to delete queue", "Interfaces",
		branchENIs, "UID", UID)
}

func (t *trunkENI) DeleteCooledDownENIs() {
	// M1 (design doc section 2.2): a not-yet-cooled-down ENI is set aside here instead of being
	// requeued (and immediately re-popped) inline, so every entry in the queue still gets its
	// immediate-disassociate step this pass - not just the front one - regardless of delete
	// cooldown. All set-aside entries are pushed back once the pass finishes draining the queue.
	var notYetCooledDown []*ENIDetails

	for eni, hasENI := t.popENIFromDeleteQueue(); hasENI; eni, hasENI = t.popENIFromDeleteQueue() {
		// Disassociate as soon as the ENI is processed, with NO cooldown wait, so the trunk slot
		// (and, subject to the VLAN reuse cooldown, the VLAN) is freed immediately instead of being
		// held hostage for the full cooldown period. DeleteNetworkInterface timing below is
		// deliberately left unchanged.
		t.disassociateIfNeeded(eni)

		if eni.deletionTimeStamp.IsZero() ||
			time.Now().After(eni.deletionTimeStamp.Add(cooldown.GetCoolDown().GetCoolDownPeriod())) {
			err := t.deleteENI(eni)
			if err != nil {
				eni.deleteRetryCount++
				if eni.deleteRetryCount >= MaxDeleteRetries {
					t.log.Error(err, "forgetting eni as max retries exceeded", "eni", eni)
					// This forgotten ENI stays attached in EC2 with no pod owner: it is a class-2
					// orphan PRODUCER that a later orphan reclaim sweep will rediscover. Count it so
					// orphan production is observable alongside branch_eni_orphan_reclaimed_total.
					branchENIDeleteForgottenCount.WithLabelValues("max_delete_retries_exceeded").Inc()
					continue
				}
				t.log.Error(err, "failed to delete eni, will retry", "eni", eni)
				t.PushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{eni})
				continue
			}
			t.log.V(1).Info("deleted eni successfully", "eni", eni, "deletion time", time.Now(),
				"pushed to queue time", eni.deletionTimeStamp)
		} else {
			notYetCooledDown = append(notYetCooledDown, eni)
		}
	}

	if len(notYetCooledDown) > 0 {
		t.PushENIsToFrontOfDeleteQueue(nil, notYetCooledDown)
	}
}

// disassociateIfNeeded is the M1 immediate-disassociate step (design doc section 2.2): called on
// every processing pass through the delete queue, independent of the delete cooldown gate below it.
// On success (or EC2 reporting the association already gone) it releases the trunk slot and starts
// the VLAN reuse cooldown right away, instead of waiting for DeleteNetworkInterface to also
// complete. A real failure is left for the next pass to retry; the slot stays counted as occupied
// until release is positively observed (requirement R3/canCreateMore conservatism). An ENI with no
// known AssociationID (a sweep-discovered orphan, see pushUnassignedBranchInterfacesToDeleteQueue)
// has nothing for us to disassociate - its slot is released as a fallback at successful delete
// instead (see deleteENI).
func (t *trunkENI) disassociateIfNeeded(eni *ENIDetails) {
	if eni.slotReleased || eni.AssociationID == "" {
		return
	}

	err := t.ec2ApiHelper.DisassociateTrunkInterface(&eni.AssociationID)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("disassociate_trunk_error").Inc()
		if !strings.Contains(err.Error(), ec2Errors.NotFoundAssociationID) {
			branchENIOperationsFailureCount.WithLabelValues("immediate_disassociate_failed").Inc()
			t.log.Error(err, "failed to immediately disassociate branch ENI from trunk, will retry", "eni", eni.ID)
			return
		}
		t.log.Info("AssociationID not found when disassociating branch from trunk ENI, it is already disassociated", "eni", eni.ID)
	}

	branchENIOperationsSuccessCount.WithLabelValues("immediate_disassociate_succeeded").Inc()
	t.log.Info("immediately disassociated branch ENI from trunk",
		"vlanID", eni.VlanID, "eniID", eni.ID, "podUID", t.podUIDForENI(eni.ID))
	t.releaseSlot(eni)
}

// releaseSlot marks eni's trunk slot as positively released (M1, design doc section 2.2) and, for a
// real VLAN, frees it subject to the VLAN reuse cooldown starting from eni.deletionTimeStamp - not
// from now - so the cooldown reproduces today's cooldown timing exactly regardless of how long
// release itself took (see assignVlanId). Idempotent: a no-op if the VLAN is not currently owned by
// this ENI (already freed by a previous call, M5 G3).
func (t *trunkENI) releaseSlot(eni *ENIDetails) {
	eni.slotReleased = true
	if eni.VlanID == 0 {
		return
	}

	t.lock.Lock()
	defer t.lock.Unlock()

	if !t.freeVlanIdLocked(eni.VlanID, eni.ID) {
		return
	}
	t.vlanReleasedAt[eni.VlanID] = eni.deletionTimeStamp
}

// deleteENIs deletes the provided ENIs. Disassociation is handled separately and earlier by
// disassociateIfNeeded (M1, design doc section 2.2); this only calls DeleteNetworkInterface, whose
// timing stays gated behind the existing cooldown check in DeleteCooledDownENIs.
func (t *trunkENI) deleteENI(eniDetail *ENIDetails) (err error) {
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

	// Fallback release (M1, design doc section 2.2): the ENI is now confirmed gone from EC2, so its
	// slot and VLAN are definitely free even if disassociateIfNeeded never positively observed a
	// release - covers a sweep-discovered orphan with no known AssociationID, and a disassociate
	// that kept failing right up until delete itself succeeded. A no-op if already released.
	if !eniDetail.slotReleased {
		t.releaseSlot(eniDetail)
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

func (t *trunkENI) getBranchInterfacesUsedByPod(pod *v1.Pod) (eniDetails []*ENIDetails) {
	branchAnnotation, isPresent := pod.Annotations[config.ResourceNamePodENI]
	if !isPresent {
		return
	}

	if err := json.Unmarshal([]byte(branchAnnotation), &eniDetails); err != nil {
		t.log.Error(err, "failed to unmarshal resource annotation", "annotation", branchAnnotation)
	}
	return
}

// pushENIToDeleteQueue pushes an ENI to a delete queue
func (t *trunkENI) pushENIToDeleteQueue(eni *ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.pushENIToDeleteQueueLocked(eni)
}

// pushENIToDeleteQueueLocked appends an ENI to the delete queue. M5 G2 (design doc section 2.6):
// an ENI ID already present in the queue is skipped (logged and counted) - a duplicate entry
// would later run deleteENI twice and double-free the ENI's VLAN (hazard H-B). Caller must hold
// the trunk lock. Returns whether the ENI was actually queued.
func (t *trunkENI) pushENIToDeleteQueueLocked(eni *ENIDetails) bool {
	if t.isENIInDeleteQueueLocked(eni.ID) {
		branchENIDeleteQueueDedupCount.Inc()
		t.log.Info("skipping delete queue push, eni is already in the delete queue", "eni", eni.ID)
		return false
	}

	t.deleteQueue = append(t.deleteQueue, eni)
	// Phase-2 shadow instrumentation: an orphan entering the delete queue is a reuse candidate
	// too. Its security groups are unknown from the describe (empty), so it can only ever count
	// as an sg_match="mismatch" availability - a real pool would describe or modify before reuse.
	t.recordShadowReleaseLocked(eni.securityGroups)
	return true
}

// isENIInDeleteQueueLocked returns whether an ENI with the given ID is already in the delete
// queue. Caller must hold the trunk lock (read or write).
func (t *trunkENI) isENIInDeleteQueueLocked(eniID string) bool {
	for _, queued := range t.deleteQueue {
		if queued.ID == eniID {
			return true
		}
	}
	return false
}

func (t *trunkENI) pushUnassignedBranchInterfacesToDeleteQueue(branchInterfaces map[string]*ec2types.NetworkInterface) bool {
	foundUnassignedBranchENI := false

	// One lock for the whole batch so the G1/G2 checks and the enqueue are atomic: a create or a
	// concurrent enqueue cannot slip in between the classification and the queue insert.
	t.lock.Lock()
	defer t.lock.Unlock()

	for _, branchInterface := range branchInterfaces {
		if branchInterface.NetworkInterfaceId == nil {
			continue
		}
		branchENIID := *branchInterface.NetworkInterfaceId

		// M5 G1 (design doc section 2.6): an ENI still being created (in EC2 but not yet in the
		// ledger) is in-flight, not an orphan - deleting it would pull the ENI from under a live
		// pod (hazard H-A). Re-checked here at enqueue time under the lock because the caller's
		// known-set snapshot may predate a create that finished during the EC2 describe. Not
		// counted as a discovered orphan.
		if _, pending := t.pendingCreates[branchENIID]; pending {
			t.log.Info("skipping in-flight branch eni, create in progress", "eni", branchENIID)
			continue
		}
		// M5 G2 (design doc section 2.6): an ENI already awaiting deletion is being processed,
		// not an orphan - re-enqueueing it would double-free its VLAN later (hazard H-B). Do not
		// re-mark the VLAN and do not count it as a discovered orphan.
		if t.isENIInDeleteQueueLocked(branchENIID) {
			branchENIDeleteQueueDedupCount.Inc()
			t.log.Info("skipping branch eni already in the delete queue", "eni", branchENIID)
			continue
		}

		t.log.Info("pushing eni to delete queue as no pod owns it", "eni", branchENIID)
		// An attached branch ENI owned by no pod in the ledger is an orphan. Count each discovery so
		// the real orphan rate is observable in Grafana (previously only the log line above existed).
		branchENIOrphanReclaimedCount.WithLabelValues("discovered").Inc()

		// The VLAN ID is parsed from an ENI tag, so it may be missing/unparseable
		// (getVlanIdFromTag error) or out of range (corrupt or unexpectedly formatted
		// tag). In either case we must still enqueue the discovered orphan for deletion -
		// returning early here would leave a real orphan attached in EC2 indefinitely and
		// make the metric above (already incremented) inconsistent with "pushed to delete
		// queue". markVlanAssigned logs-and-continues on an invalid ID, but if we enqueue
		// the ENI with an out-of-range ID, deleteENI later calls freeVlanId(vlanId) which
		// indexes usedVlanIds and would panic on an out-of-bounds index. So fall back to
		// the reserved VLAN ID 0 (never freed by deleteENI) whenever the tag is missing or
		// invalid, so deletion still proceeds without panicking.
		vlanId, err := t.getVlanIdFromTag(branchInterface.TagSet)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("get_vlan_from_tag").Inc()
			t.log.Error(err, "failed to find vlan id; using reserved vlan id 0 for delete queue", "interface", branchENIID)
			vlanId = 0
		} else if vlanId < 0 || vlanId >= MaxAllocatableVlanIds {
			trunkENIOperationsErrCount.WithLabelValues("invalid_vlan_id_on_delete").Inc()
			t.log.Error(fmt.Errorf("vlan id %d is outside allocatable range [0,%d)", vlanId, MaxAllocatableVlanIds),
				"using reserved vlan id 0 for delete queue", "interface", branchENIID)
			vlanId = 0
		} else {
			// Even though the ENI is going to be deleted, keep the VLAN reserved while it sits in
			// the cool down queue, owned by this ENI (M5 G3).
			if err := t.markVlanAssignedWithOwnerLocked(vlanId, branchENIID); err != nil {
				trunkENIOperationsErrCount.WithLabelValues("mark_invalid_vlan_id").Inc()
				t.log.Error(err, "failed to mark vlan id assigned", "vlan id", vlanId)
			}
		}
		t.pushENIToDeleteQueueLocked(&ENIDetails{
			ID:                branchENIID,
			VlanID:            vlanId,
			deletionTimeStamp: time.Now(),
		})
		foundUnassignedBranchENI = true
	}
	return foundUnassignedBranchENI
}

// pushENIsToFrontOfDeleteQueue pushes the ENI list to the front of the delete queue
func (t *trunkENI) PushENIsToFrontOfDeleteQueue(pod *v1.Pod, eniList []*ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

	// M5 G1 (design doc section 2.6): an in-flight create pushed here (create/associate failure
	// path) has terminally exited the create flow - it must not stay in the pending set.
	t.removePendingCreatesLocked(eniList)

	if pod != nil {
		for _, eni := range eniList {
			t.rememberPodUIDLocked(eni.ID, string(pod.UID))
		}
		t.log.Info("pushing ENIs to delete queue and removing pod from cache",
			"uid", pod.UID, "ENIs", eniList)
		delete(t.uidToBranchENIMap, string(pod.UID))
	} else {
		t.log.Info("pushing ENIs to delete queue", "ENIs", eniList)
	}

	// M5 G2 (design doc section 2.6): never insert an ENI ID already present in the queue - a
	// duplicate entry would later double-free the ENI's VLAN (hazard H-B).
	var toQueue []*ENIDetails
	for _, eni := range eniList {
		if t.isENIInDeleteQueueLocked(eni.ID) {
			branchENIDeleteQueueDedupCount.Inc()
			t.log.Info("skipping delete queue push, eni is already in the delete queue", "eni", eni.ID)
			continue
		}
		toQueue = append(toQueue, eni)
	}

	t.deleteQueue = append(toQueue, t.deleteQueue...)
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

	// M5 G1 (design doc section 2.6): the ENIs are entering the pod-owned ledger, so they are no
	// longer in-flight. Done before the duplicate-UID early return so a pending entry can never
	// be left behind on any exit.
	t.removePendingCreatesLocked(branchENIs)

	if _, ok := t.uidToBranchENIMap[UID]; ok {
		t.log.Info("branch eni already exist not adding again", "request", branchENIs)
		return
	}

	t.uidToBranchENIMap[UID] = branchENIs
	for _, eni := range branchENIs {
		t.rememberPodUIDLocked(eni.ID, UID)
	}
}

// getBranchFromCache returns the branch from the cache
func (t *trunkENI) getBranchFromCache(UID string) (branchENIs []*ENIDetails, isPresent bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	branchENIs, isPresent = t.uidToBranchENIMap[UID]
	return
}

// assignVlanId assigns a free vlan id from the list of available vlan ids. In the future this can be changed to LL
func (t *trunkENI) assignVlanId(podUID ...string) (int, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	uid := ""
	if len(podUID) > 0 {
		uid = podUID[0]
	}

	reuseCooldown := cooldown.GetCoolDown().GetCoolDownPeriod()
	now := time.Now()
	blockedByCooldown := false
	for index, used := range t.usedVlanIds {
		if used {
			continue
		}
		// M1 (design doc section 2.2): a VLAN released by disassociateIfNeeded/releaseSlot is free
		// in the ledger, but must not be handed out again until reuseCooldown has elapsed since its
		// ENI entered the delete queue - the node dataplane needs that window to forget the old
		// pod's rules before the VLAN number means something different.
		if releasedAt, cooling := t.vlanReleasedAt[index]; cooling && now.Before(releasedAt.Add(reuseCooldown)) {
			if !blockedByCooldown {
				t.log.Info("VLAN reuse blocked by cooldown",
					"vlanID", index, "eniID", "", "podUID", uid)
			}
			blockedByCooldown = true
			continue
		}
		t.usedVlanIds[index] = true
		// Fresh assignment: the owning ENI does not exist yet; the owner is recorded once
		// CreateNetworkInterface returns the ENI ID (M5 G3). Clear any stale record.
		delete(t.vlanOwner, index)
		delete(t.vlanReleasedAt, index)
		if blockedByCooldown {
			branchENIVlanReuseCooldownBlockedCount.Inc()
		}
		return index, nil
	}
	if blockedByCooldown {
		branchENIVlanReuseCooldownBlockedCount.Inc()
	}
	return 0, fmt.Errorf("failed to find free vlan id in the available %d ids", len(t.usedVlanIds))
}

// markVlanAssigned marks a vlan Id as assigned if not used
func (t *trunkENI) markVlanAssigned(vlanId int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if err := t.markVlanAssignedLocked(vlanId); err != nil {
		trunkENIOperationsErrCount.WithLabelValues("mark_invalid_vlan_id").Inc()
		t.log.Error(err, "failed to mark vlan id assigned", "vlan id", vlanId)
	}
}

func (t *trunkENI) markVlanAssignedLocked(vlanId int) error {
	if vlanId < 0 || vlanId >= len(t.usedVlanIds) {
		return fmt.Errorf("vlan id %d is outside allocatable range [0,%d)", vlanId, len(t.usedVlanIds))
	}
	t.usedVlanIds[vlanId] = true
	return nil
}

// markVlanAssignedWithOwner marks a vlan Id as assigned and records eniID as its owner (M5 G3,
// design doc section 2.6). Reserved vlan id 0 never gets an owner (it is permanently marked and
// never freed), and an empty eniID leaves any existing owner record untouched.
func (t *trunkENI) markVlanAssignedWithOwner(vlanId int, eniID string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if err := t.markVlanAssignedWithOwnerLocked(vlanId, eniID); err != nil {
		trunkENIOperationsErrCount.WithLabelValues("mark_invalid_vlan_id").Inc()
		t.log.Error(err, "failed to mark vlan id assigned", "vlan id", vlanId)
	}
}

// markVlanAssignedWithOwnerLocked is markVlanAssignedWithOwner's caller-locked counterpart.
// Caller must hold the trunk lock.
func (t *trunkENI) markVlanAssignedWithOwnerLocked(vlanId int, eniID string) error {
	if err := t.markVlanAssignedLocked(vlanId); err != nil {
		return err
	}
	if vlanId != 0 && eniID != "" {
		t.vlanOwner[vlanId] = eniID
	}
	return nil
}

// addPendingCreate records eniID as an in-flight branch ENI create (M5 G1, design doc section
// 2.6) and, since the ENI ID is now known, records it as the owner of vlanID (M5 G3). Must be
// removed via removePendingCreatesLocked on every exit for this ENI.
func (t *trunkENI) addPendingCreate(eniID string, vlanID int, podUID ...string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.pendingCreates[eniID] = struct{}{}
	if len(podUID) > 0 {
		t.rememberPodUIDLocked(eniID, podUID[0])
	}
	if vlanID != 0 {
		t.vlanOwner[vlanID] = eniID
	}
}

func (t *trunkENI) rememberPodUID(eniID, podUID string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.rememberPodUIDLocked(eniID, podUID)
}

func (t *trunkENI) rememberPodUIDLocked(eniID, podUID string) {
	if eniID == "" || podUID == "" {
		return
	}
	if t.eniToPodUID == nil {
		t.eniToPodUID = make(map[string]string)
	}
	t.eniToPodUID[eniID] = podUID
}

func (t *trunkENI) podUIDForENI(eniID string) string {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.eniToPodUID[eniID]
}

// removePendingCreatesLocked removes each ENI's ID from the pending-creates set (M5 G1, design
// doc section 2.6). Caller must hold the trunk lock.
func (t *trunkENI) removePendingCreatesLocked(enis []*ENIDetails) {
	for _, eni := range enis {
		delete(t.pendingCreates, eni.ID)
	}
}

// freeVlanId frees a vlan ID currently used by a network interface. eniID is the ENI releasing
// the vlan; if the vlan is owned by a DIFFERENT ENI, the free is refused (M5 G3, design doc
// section 2.6) because a duplicate delete-queue entry must never release a vlan that meanwhile
// belongs to a new pod's branch ENI (hazard H-B). An empty eniID or an unrecorded owner preserves
// legacy ownerless-free behavior. Reserved vlan id 0 is never freed in production: callers
// (deleteENI, and assignVlanId's never-returns-0 guarantee on a real trunk) never pass it here.
func (t *trunkENI) freeVlanId(vlanId int, eniID string, podUID ...string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.freeVlanIdLocked(vlanId, eniID, podUID...)
}

// freeVlanIdLocked is freeVlanId's caller-locked counterpart, returning whether the VLAN was
// actually freed (false if it was already unused, or owned by a different ENI - M5 G3). Caller
// must hold the trunk lock.
func (t *trunkENI) freeVlanIdLocked(vlanId int, eniID string, podUID ...string) bool {
	isUsed := t.usedVlanIds[vlanId]
	if !isUsed {
		trunkENIOperationsErrCount.WithLabelValues("free_unused_vlan_id").Inc()
		t.log.Error(fmt.Errorf("failed to free a unused vlan id"), "", "vlan id", vlanId)
		return false
	}

	if owner, hasOwner := t.vlanOwner[vlanId]; hasOwner && eniID != "" && owner != eniID {
		trunkENIOperationsErrCount.WithLabelValues("free_vlan_owner_mismatch").Inc()
		t.log.Error(fmt.Errorf("refusing to free vlan owned by another eni"), "",
			"vlan id", vlanId, "owner", owner, "requester", eniID)
		return false
	}

	t.usedVlanIds[vlanId] = false
	delete(t.vlanOwner, vlanId)
	uid := ""
	if len(podUID) > 0 {
		uid = podUID[0]
	} else if t.eniToPodUID != nil {
		uid = t.eniToPodUID[eniID]
	}
	t.log.Info("freed VLAN ID", "vlanID", vlanId, "eniID", eniID, "podUID", uid)
	delete(t.eniToPodUID, eniID)
	return true
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

	// M1 (design doc section 2.2): a delete-queue entry whose slot has been positively released
	// (disassociateIfNeeded succeeded, or as a fallback, delete succeeded) no longer occupies a
	// trunk slot. An entry never counted as released - including a sweep-discovered orphan with no
	// known AssociationID, which is still attached in EC2 despite having nothing for us to
	// disassociate - keeps counting as occupied. Over-counting here is safe (refuses a create that
	// could have succeeded); under-counting is not (would over-subscribe the trunk).
	var occupiedInQueue int
	for _, eni := range t.deleteQueue {
		if !eni.slotReleased {
			occupiedInQueue++
		}
	}

	if usedBranches+occupiedInQueue < vpc.Limits[t.instance.Type()].BranchInterface {
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
