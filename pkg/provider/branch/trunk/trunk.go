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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
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
	MaxDeleteRetries = 3
)

var (
	InterfaceTypeTrunk   = "trunk"
	TrunkEniDescription  = "trunk-eni"
	BranchEniDescription = "branch-eni"
)

var ErrCurrentlyAtMaxCapacity = fmt.Errorf("cannot create more branches at this point as used branches plus the " +
	"delete queue is at max capacity")

// prefixPodRef holds the mapping between a pod and its prefix-delegated IP(s) on a shared ENI,
// used during state recovery in InitTrunk.
type prefixPodRef struct {
	podUID     string
	ip         string
	ipv6       string
	eniDetails *ENIDetails
}

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

	prometheusRegistered = false
)

type TrunkENI interface {
	// InitTrunk initializes trunk interface
	InitTrunk(instance ec2.EC2Instance, pods []v1.Pod) error
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
	// DeleteAllBranchENIs deletes all the branch ENI associated with the trunk and also clears the cool down queue
	DeleteAllBranchENIs()
	// Introspect returns the state of the Trunk ENI
	Introspect() IntrospectResponse
	// AllocateIPFromSharedENI allocates an IP from a shared branch ENI with prefix delegation.
	// Automatically detects IPv4 vs IPv6 from the instance's subnet configuration.
	AllocateIPFromSharedENI(pod *v1.Pod, securityGroups []string) (*ENIDetails, error)
	// FreePrefixIP releases a pod's prefix IP back to the shared ENI pool
	FreePrefixIP(UID string)
	// HasPrefixAllocation returns true if the given UID has a prefix-based allocation
	HasPrefixAllocation(UID string) bool
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
	// deleteQueue is the queue of ENIs that are being cooled down before being deleted
	deleteQueue []*ENIDetails
	// nodeName tag is the tag added to trunk and branch ENIs created on the node
	nodeIDTag []ec2types.Tag
	// sgToBranchENIPool maps canonical SG key to shared branch ENIs with prefix delegation
	sgToBranchENIPool map[string][]*BranchENIWithPrefix
	// uidToPrefixAllocation maps pod UID to its prefix allocation (shared ENI + assigned IP)
	uidToPrefixAllocation map[string]*PrefixAllocation
	// prefixDelegationEnabled indicates whether branch ENI prefix delegation is active
	prefixDelegationEnabled bool
	// isIPv6 indicates the cluster is running in IPv6 mode (from VPC CNI ENABLE_IPv6)
	isIPv6 bool
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
	// PrefixCIDR is the /28 IPv4 prefix attached to this branch ENI (set when prefix delegation is enabled)
	PrefixCIDR string `json:"prefixCidr,omitempty"`
	// IPv6PrefixCIDR is the /80 IPv6 prefix attached to this branch ENI
	IPv6PrefixCIDR string `json:"ipv6PrefixCidr,omitempty"`
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
func NewTrunkENI(logger logr.Logger, instance ec2.EC2Instance, helper api.EC2APIHelper, prefixDelegationEnabled bool, isIPv6 bool) TrunkENI {
	availVlans := make([]bool, MaxAllocatableVlanIds)
	// VlanID 0 cannot be assigned.
	availVlans[0] = true

	return &trunkENI{
		log:                     logger,
		usedVlanIds:             availVlans,
		ec2ApiHelper:            helper,
		instance:                instance,
		uidToBranchENIMap:       make(map[string][]*ENIDetails),
		sgToBranchENIPool:       make(map[string][]*BranchENIWithPrefix),
		uidToPrefixAllocation:   make(map[string]*PrefixAllocation),
		prefixDelegationEnabled: prefixDelegationEnabled,
		isIPv6:                  isIPv6,
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
			unreconciledTrunkENICount.WithLabelValues("security_groups").Inc()
		}

		if mismatchedSubnets {
			unreconciledTrunkENICount.WithLabelValues("subnet").Inc()
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

	// When prefix delegation is enabled, collect prefix-mode pods to rebuild the shared ENI pool.
	prefixENIPods := make(map[string][]prefixPodRef)

	// From the list of pods on the given node, and the branch ENIs from EC2 API call rebuild the internal cache
	for _, pod := range podList {
		pod := pod // Fix gosec G601, so we can use &node
		eniListFromPod := t.getBranchInterfacesUsedByPod(&pod)
		if len(eniListFromPod) == 0 {
			continue
		}

		// Check if this pod is a prefix-delegated allocation (shared ENI mode)
		// Detected by having PrefixCIDR (IPv4) or IPv6PrefixCIDR (IPv6) set in annotation
		if t.prefixDelegationEnabled && len(eniListFromPod) == 1 &&
			(eniListFromPod[0].PrefixCIDR != "" || eniListFromPod[0].IPv6PrefixCIDR != "") {
			eni := eniListFromPod[0]
			_, inEC2 := associatedBranchInterfaces[eni.ID]
			_, alreadyClaimed := prefixENIPods[eni.ID]
			if !inEC2 && !alreadyClaimed {
				t.log.Error(fmt.Errorf("prefix eni allocated to pod not found in ec2"), "eni not found",
					"eni", eni.ID, "pod", pod.Name)
				trunkENIOperationsErrCount.WithLabelValues("get_branch_eni_from_ec2").Inc()
				continue
			}
			t.markVlanAssigned(eni.VlanID)
			prefixENIPods[eni.ID] = append(prefixENIPods[eni.ID], prefixPodRef{
				podUID:     string(pod.UID),
				ip:         eni.IPV4Addr,
				ipv6:       eni.IPV6Addr,
				eniDetails: eni,
			})
			// Remove from the unaccounted set — this ENI is claimed by prefix pods
			delete(associatedBranchInterfaces, eni.ID)
			continue
		}

		var branchENIs []*ENIDetails
		for _, eni := range eniListFromPod {
			_, isPresent := associatedBranchInterfaces[eni.ID]
			if !isPresent {
				t.log.Error(fmt.Errorf("eni allocated to pod not found in ec2"), "eni not found", "eni", eni)
				trunkENIOperationsErrCount.WithLabelValues("get_branch_eni_from_ec2").Inc()
				continue
			}
			// Mark the Vlan ID from the pod's annotation
			t.markVlanAssigned(eni.VlanID)

			branchENIs = append(branchENIs, eni)
			delete(associatedBranchInterfaces, eni.ID)
		}
		t.uidToBranchENIMap[string(pod.UID)] = branchENIs
	}

	// Rebuild prefix delegation pools from collected pod references and EC2 state
	if t.prefixDelegationEnabled && len(prefixENIPods) > 0 {
		t.rebuildPrefixPools(prefixENIPods, branchInterfaces, log)
	}

	// Delete the branch ENI that don't belong to any pod.
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

	log.V(1).Info("successfully initialized trunk with all associated branch interfaces",
		"trunk", t.trunkENIId, "branch interfaces", t.uidToBranchENIMap)

	return nil
}

// rebuildPrefixPools reconstructs the in-memory prefix delegation state from pod annotations
// and EC2 branch interface data after a controller restart.
func (t *trunkENI) rebuildPrefixPools(prefixENIPods map[string][]prefixPodRef, branchInterfaces []*ec2types.NetworkInterface, log logr.Logger) {
	// Index EC2 branch interfaces by ID for lookup
	ec2ENIByID := make(map[string]*ec2types.NetworkInterface)
	for _, iface := range branchInterfaces {
		ec2ENIByID[*iface.NetworkInterfaceId] = iface
	}

	for eniID, podRefs := range prefixENIPods {
		ec2Iface := ec2ENIByID[eniID]
		if ec2Iface == nil {
			log.Error(fmt.Errorf("branch interface not found in EC2 response"),
				"skipping prefix pool rebuild for ENI", "eni", eniID)
			continue
		}

		// Get VLAN ID from tags
		vlanId, err := t.getVlanIdFromTag(ec2Iface.TagSet)
		if err != nil {
			log.Error(err, "failed to get vlan id for prefix ENI", "eni", eniID)
			continue
		}

		// Get security groups from EC2 interface
		var securityGroups []string
		for _, group := range ec2Iface.Groups {
			if group.GroupId != nil {
				securityGroups = append(securityGroups, *group.GroupId)
			}
		}

		// Collect IPv4 prefixes from EC2
		var prefixCIDRs []string
		var allIPs []string
		for _, prefix := range ec2Iface.Ipv4Prefixes {
			if prefix.Ipv4Prefix != nil {
				prefixCIDR := *prefix.Ipv4Prefix
				prefixCIDRs = append(prefixCIDRs, prefixCIDR)
				ips, err := utils.DeconstructIPsFromPrefix(prefixCIDR)
				if err != nil {
					log.Error(err, "failed to deconstruct IPv4 prefix during recovery", "prefix", prefixCIDR)
					continue
				}
				allIPs = append(allIPs, ips...)
			}
		}

		// Collect IPv6 prefixes from EC2
		var ipv6PrefixCIDRs []string
		var allIPv6s []string
		for _, prefix := range ec2Iface.Ipv6Prefixes {
			if prefix.Ipv6Prefix != nil {
				prefixCIDR := *prefix.Ipv6Prefix
				ipv6PrefixCIDRs = append(ipv6PrefixCIDRs, prefixCIDR)
				ips, err := utils.DeconstructIPv6sFromPrefix(prefixCIDR, MaxIPv6PerPrefix)
				if err != nil {
					log.Error(err, "failed to deconstruct IPv6 prefix during recovery", "prefix", prefixCIDR)
					continue
				}
				allIPv6s = append(allIPv6s, ips...)
			}
		}

		if len(prefixCIDRs) == 0 && len(ipv6PrefixCIDRs) == 0 {
			log.Error(fmt.Errorf("no prefixes found on ENI during recovery"),
				"eni has no prefix data from EC2", "eni", eniID)
			continue
		}

		// Use the first pod's annotation to fill in ENI metadata (MAC, subnet, association)
		firstDetails := podRefs[0].eniDetails

		baseENIDetail := &ENIDetails{
			ID:            eniID,
			MACAdd:        firstDetails.MACAdd,
			VlanID:        vlanId,
			SubnetCIDR:    firstDetails.SubnetCIDR,
			SubnetV6CIDR:  firstDetails.SubnetV6CIDR,
			AssociationID: firstDetails.AssociationID,
		}
		if len(prefixCIDRs) > 0 {
			baseENIDetail.PrefixCIDR = prefixCIDRs[0]
		}
		if len(ipv6PrefixCIDRs) > 0 {
			baseENIDetail.IPv6PrefixCIDR = ipv6PrefixCIDRs[0]
		}

		// Build UsedIPs from the pod references (IPv4)
		usedIPs := make(map[string]string)
		for _, ref := range podRefs {
			if ref.ip != "" {
				usedIPs[ref.ip] = ref.podUID
			}
		}

		// Build UsedIPv6s from the pod references
		usedIPv6s := make(map[string]string)
		for _, ref := range podRefs {
			if ref.ipv6 != "" {
				usedIPv6s[ref.ipv6] = ref.podUID
			}
		}

		// FreeIPs = allIPs minus usedIPs
		usedIPBare := make(map[string]struct{})
		for ip := range usedIPs {
			usedIPBare[stripCIDRSuffix(ip)] = struct{}{}
		}
		var freeIPs []string
		for _, ip := range allIPs {
			if _, used := usedIPBare[stripCIDRSuffix(ip)]; !used {
				freeIPs = append(freeIPs, ip)
			}
		}

		// FreeIPv6s = allIPv6s minus usedIPv6s
		usedIPv6Bare := make(map[string]struct{})
		for ip := range usedIPv6s {
			usedIPv6Bare[stripCIDRSuffix(ip)] = struct{}{}
		}
		var freeIPv6s []string
		for _, ip := range allIPv6s {
			if _, used := usedIPv6Bare[stripCIDRSuffix(ip)]; !used {
				freeIPv6s = append(freeIPv6s, ip)
			}
		}

		sharedENI := &BranchENIWithPrefix{
			ENIDetail:       baseENIDetail,
			SecurityGroups:  securityGroups,
			PrefixCIDRs:     prefixCIDRs,
			AllIPs:          allIPs,
			FreeIPs:         freeIPs,
			UsedIPs:         usedIPs,
			IPv6PrefixCIDRs: ipv6PrefixCIDRs,
			AllIPv6s:        allIPv6s,
			FreeIPv6s:       freeIPv6s,
			UsedIPv6s:       usedIPv6s,
		}

		// Add to the SG pool
		sgKey := CanonicalSGKey(securityGroups)
		t.sgToBranchENIPool[sgKey] = append(t.sgToBranchENIPool[sgKey], sharedENI)

		// Populate uidToPrefixAllocation for each pod
		for _, ref := range podRefs {
			t.uidToPrefixAllocation[ref.podUID] = &PrefixAllocation{
				BranchENI:    sharedENI,
				AssignedIP:   ref.ip,
				AssignedIPv6: ref.ipv6,
			}
		}

		log.Info("rebuilt prefix pool for shared ENI", "eni", eniID,
			"ipv4Prefixes", prefixCIDRs, "ipv6Prefixes", ipv6PrefixCIDRs,
			"usedIPv4", len(usedIPs), "freeIPv4", len(freeIPs),
			"usedIPv6", len(usedIPv6s), "freeIPv6", len(freeIPv6s))
	}
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

	// Reconcile prefix allocations
	for uid, alloc := range t.uidToPrefixAllocation {
		if _, exists := currentPodSet[uid]; !exists {
			leakedENIs += 1
			if alloc.AssignedIP != "" {
				alloc.BranchENI.ReleaseIP(uid)
			}
			if alloc.AssignedIPv6 != "" {
				alloc.BranchENI.ReleaseIPv6(uid)
			}
			delete(t.uidToPrefixAllocation, uid)
			t.log.Info("leaked prefix IP released to cooldown", "pod uid", uid, "ip", alloc.AssignedIP,
				"ipv6", alloc.AssignedIPv6, "eni", alloc.BranchENI.ENIDetail.ID)
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

// AllocateIPFromSharedENI allocates an IP from a shared branch ENI.
// Automatically uses IPv6 if aws-node has ENABLE_IPV6 = true
func (t *trunkENI) AllocateIPFromSharedENI(pod *v1.Pod, securityGroups []string) (*ENIDetails, error) {
	log := t.log.WithValues("request", "prefix-allocate", "pod namespace", pod.Namespace, "pod name", pod.Name)
	podUID := string(pod.UID)

	needsIPv6 := t.isIPv6
	needsIPv4 := !needsIPv6

	t.lock.Lock()
	defer t.lock.Unlock()

	if _, exists := t.uidToPrefixAllocation[podUID]; exists {
		return nil, fmt.Errorf("prefix allocation already exists for pod UID %s", podUID)
	}

	if securityGroups == nil || len(securityGroups) == 0 {
		securityGroups = t.instance.CurrentInstanceSecurityGroups()
	}

	sgKey := CanonicalSGKey(securityGroups)

	// Try to find an existing shared ENI with free capacity for the requested IP family
	if pool, ok := t.sgToBranchENIPool[sgKey]; ok {
		for _, sharedENI := range pool {
			if t.sharedENIHasCapacity(sharedENI, needsIPv4, needsIPv6) {
				return t.allocateFromSharedENI(sharedENI, podUID, needsIPv4, needsIPv6, log)
			}
		}
	}

	// Try to expand an existing ENI by assigning an additional prefix
	if pool, ok := t.sgToBranchENIPool[sgKey]; ok {
		for _, sharedENI := range pool {
			if t.tryExpandENI(sharedENI, needsIPv4, needsIPv6, log) {
				return t.allocateFromSharedENI(sharedENI, podUID, needsIPv4, needsIPv6, log)
			}
		}
	}

	// No existing ENI can be expanded — create a new one
	if !t.canCreateMoreLocked() {
		return nil, ErrCurrentlyAtMaxCapacity
	}

	sharedENI, err := t.createSharedENI(securityGroups, needsIPv4, needsIPv6, log)
	if err != nil {
		return nil, err
	}

	t.sgToBranchENIPool[sgKey] = append(t.sgToBranchENIPool[sgKey], sharedENI)
	return t.allocateFromSharedENI(sharedENI, podUID, needsIPv4, needsIPv6, log)
}

// sharedENIHasCapacity returns true if the ENI can satisfy the requested allocation.
func (t *trunkENI) sharedENIHasCapacity(eni *BranchENIWithPrefix, needsIPv4, needsIPv6 bool) bool {
	if needsIPv4 && !eni.HasFreeIPs() {
		return false
	}
	if needsIPv6 && !eni.HasFreeIPv6s() {
		return false
	}
	return needsIPv4 || needsIPv6
}

// allocateFromSharedENI allocates the requested IP(s) from the shared ENI and returns ENIDetails.
func (t *trunkENI) allocateFromSharedENI(sharedENI *BranchENIWithPrefix, podUID string, needsIPv4, needsIPv6 bool, log logr.Logger) (*ENIDetails, error) {
	alloc := &PrefixAllocation{BranchENI: sharedENI}

	if needsIPv4 {
		alloc.AssignedIP = sharedENI.AllocateIP(podUID)
	}
	if needsIPv6 {
		alloc.AssignedIPv6 = sharedENI.AllocateIPv6(podUID)
	}

	t.uidToPrefixAllocation[podUID] = alloc

	eniDetail := &ENIDetails{
		ID:             sharedENI.ENIDetail.ID,
		MACAdd:         sharedENI.ENIDetail.MACAdd,
		VlanID:         sharedENI.ENIDetail.VlanID,
		SubnetCIDR:     sharedENI.ENIDetail.SubnetCIDR,
		SubnetV6CIDR:   sharedENI.ENIDetail.SubnetV6CIDR,
		PrefixCIDR:     sharedENI.ENIDetail.PrefixCIDR,
		IPv6PrefixCIDR: sharedENI.ENIDetail.IPv6PrefixCIDR,
		AssociationID:  sharedENI.ENIDetail.AssociationID,
	}
	if alloc.AssignedIP != "" {
		eniDetail.IPV4Addr = stripCIDRSuffix(alloc.AssignedIP)
	}
	if alloc.AssignedIPv6 != "" {
		eniDetail.IPV6Addr = stripCIDRSuffix(alloc.AssignedIPv6)
	}

	log.Info("allocated from shared ENI", "ipv4", alloc.AssignedIP, "ipv6", alloc.AssignedIPv6,
		"eni", sharedENI.ENIDetail.ID)
	return eniDetail, nil
}

// tryExpandENI attempts to expand the shared ENI by assigning additional prefixes.
// Returns true if expansion succeeded and the ENI now has capacity.
func (t *trunkENI) tryExpandENI(sharedENI *BranchENIWithPrefix, needsIPv4, needsIPv6 bool, log logr.Logger) bool {
	maxPrefixesPerENI := vpc.Limits[t.instance.Type()].IPv4PerInterface
	if maxPrefixesPerENI < 1 {
		maxPrefixesPerENI = 1
	}

	// Expand IPv4 if needed and possible
	if needsIPv4 && !sharedENI.HasFreeIPs() && sharedENI.PrefixCount() < maxPrefixesPerENI {
		newPrefixes, err := t.ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(
			sharedENI.ENIDetail.ID, config.ResourceTypeIPv4Prefix, 1)
		if err != nil {
			log.Error(err, "failed to assign additional IPv4 prefix", "eni", sharedENI.ENIDetail.ID)
			return false
		}
		newIPs, err := utils.DeconstructIPsFromPrefix(newPrefixes[0])
		if err != nil {
			log.Error(err, "failed to deconstruct IPv4 prefix", "prefix", newPrefixes[0])
			return false
		}
		sharedENI.AddPrefix(newPrefixes[0], newIPs)
	}

	// Expand IPv6 if needed and possible
	if needsIPv6 && !sharedENI.HasFreeIPv6s() {
		newPrefixes, err := t.ec2ApiHelper.AssignIPv6PrefixAndWaitTillReady(sharedENI.ENIDetail.ID, 1)
		if err != nil {
			log.Error(err, "failed to assign additional IPv6 prefix", "eni", sharedENI.ENIDetail.ID)
			return false
		}
		newIPs, err := utils.DeconstructIPv6sFromPrefix(newPrefixes[0], MaxIPv6PerPrefix)
		if err != nil {
			log.Error(err, "failed to deconstruct IPv6 prefix", "prefix", newPrefixes[0])
			return false
		}
		sharedENI.AddIPv6Prefix(newPrefixes[0], newIPs)
	}

	return t.sharedENIHasCapacity(sharedENI, needsIPv4, needsIPv6)
}

// createSharedENI creates a new branch ENI with the requested prefix type(s).
func (t *trunkENI) createSharedENI(securityGroups []string, needsIPv4, needsIPv6 bool, log logr.Logger) (*BranchENIWithPrefix, error) {
	connectionTrackingSpec := t.getConnectionTrackingSpec()

	vlanID, err := t.assignVlanIdLocked()
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("assign_vlan_id").Inc()
		return nil, fmt.Errorf("assigning vlan id, %w", err)
	}

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
	tags = append(tags, t.nodeIDTag...)

	ipResourceCount := &config.IPResourceCount{}
	if needsIPv4 {
		ipResourceCount.IPv4PrefixCount = 1
	}
	if needsIPv6 {
		ipResourceCount.IPv6PrefixCount = 1
	}

	nwInterface, err := t.ec2ApiHelper.CreateNetworkInterface(&BranchEniDescription,
		aws.String(t.instance.SubnetID()), securityGroups, tags, ipResourceCount, nil, connectionTrackingSpec)
	if err != nil {
		t.freeVlanIdLocked(vlanID)
		branchENIOperationsFailureCount.WithLabelValues("creating_branch_eni_failed").Inc()
		return nil, fmt.Errorf("creating network interface with prefix, %w", err)
	}
	branchENIOperationsSuccessCount.WithLabelValues("created_branch_eni_succeeded").Inc()

	// Associate branch to trunk
	associationOutput, err := t.ec2ApiHelper.AssociateBranchToTrunk(&t.trunkENIId, nwInterface.NetworkInterfaceId, vlanID)
	if err != nil {
		t.freeVlanIdLocked(vlanID)
		_ = t.ec2ApiHelper.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
		trunkENIOperationsErrCount.WithLabelValues("associate_branch").Inc()
		return nil, fmt.Errorf("associating branch to trunk, %w", err)
	}

	baseENIDetail := &ENIDetails{
		ID:            *nwInterface.NetworkInterfaceId,
		MACAdd:        *nwInterface.MacAddress,
		VlanID:        vlanID,
		SubnetCIDR:    t.instance.SubnetCidrBlock(),
		SubnetV6CIDR:  t.instance.SubnetV6CidrBlock(),
		AssociationID: *associationOutput.InterfaceAssociation.AssociationId,
	}

	sharedENI := &BranchENIWithPrefix{
		ENIDetail:      baseENIDetail,
		SecurityGroups: securityGroups,
		UsedIPs:        make(map[string]string),
		UsedIPv6s:      make(map[string]string),
	}

	// Extract and populate IPv4 prefix pool
	if needsIPv4 {
		if len(nwInterface.Ipv4Prefixes) == 0 {
			t.freeVlanIdLocked(vlanID)
			_ = t.ec2ApiHelper.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
			return nil, fmt.Errorf("no IPv4 prefix returned from CreateNetworkInterface")
		}
		prefixCIDR := *nwInterface.Ipv4Prefixes[0].Ipv4Prefix
		allIPs, err := utils.DeconstructIPsFromPrefix(prefixCIDR)
		if err != nil {
			t.freeVlanIdLocked(vlanID)
			_ = t.ec2ApiHelper.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
			return nil, fmt.Errorf("deconstructing IPv4 prefix %s, %w", prefixCIDR, err)
		}
		baseENIDetail.PrefixCIDR = prefixCIDR
		sharedENI.PrefixCIDRs = []string{prefixCIDR}
		sharedENI.AllIPs = allIPs
		sharedENI.FreeIPs = make([]string, len(allIPs))
		copy(sharedENI.FreeIPs, allIPs)
	}

	// Extract and populate IPv6 prefix pool
	if needsIPv6 {
		if len(nwInterface.Ipv6Prefixes) == 0 {
			t.freeVlanIdLocked(vlanID)
			_ = t.ec2ApiHelper.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
			return nil, fmt.Errorf("no IPv6 prefix returned from CreateNetworkInterface")
		}
		ipv6PrefixCIDR := *nwInterface.Ipv6Prefixes[0].Ipv6Prefix
		allIPv6s, err := utils.DeconstructIPv6sFromPrefix(ipv6PrefixCIDR, MaxIPv6PerPrefix)
		if err != nil {
			t.freeVlanIdLocked(vlanID)
			_ = t.ec2ApiHelper.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
			return nil, fmt.Errorf("deconstructing IPv6 prefix %s, %w", ipv6PrefixCIDR, err)
		}
		baseENIDetail.IPv6PrefixCIDR = ipv6PrefixCIDR
		sharedENI.IPv6PrefixCIDRs = []string{ipv6PrefixCIDR}
		sharedENI.AllIPv6s = allIPv6s
		sharedENI.FreeIPv6s = make([]string, len(allIPv6s))
		copy(sharedENI.FreeIPv6s, allIPv6s)
	}

	log.Info("created new shared branch ENI", "eni", baseENIDetail.ID,
		"ipv4Prefix", baseENIDetail.PrefixCIDR, "ipv6Prefix", baseENIDetail.IPv6PrefixCIDR)

	return sharedENI, nil
}

// FreePrefixIP releases a pod's prefix IP(s) back to the shared ENI's cooling queue.
func (t *trunkENI) FreePrefixIP(UID string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	alloc, exists := t.uidToPrefixAllocation[UID]
	if !exists {
		t.log.Info("no prefix allocation found for pod", "UID", UID)
		return
	}

	if alloc.AssignedIP != "" {
		alloc.BranchENI.ReleaseIP(UID)
	}
	if alloc.AssignedIPv6 != "" {
		alloc.BranchENI.ReleaseIPv6(UID)
	}
	delete(t.uidToPrefixAllocation, UID)

	t.log.Info("released prefix IP to cooldown", "ip", alloc.AssignedIP, "ipv6", alloc.AssignedIPv6,
		"eni", alloc.BranchENI.ENIDetail.ID, "UID", UID)
}

// HasPrefixAllocation returns true if the given UID has a prefix-based allocation.
func (t *trunkENI) HasPrefixAllocation(UID string) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	_, exists := t.uidToPrefixAllocation[UID]
	return exists
}

func stripCIDRSuffix(ip string) string {
	if idx := strings.Index(ip, "/"); idx != -1 {
		return ip[:idx]
	}
	return ip
}

// canCreateMoreLocked checks capacity without acquiring lock (caller must hold lock).
func (t *trunkENI) canCreateMoreLocked() bool {
	var usedBranches int
	for _, branches := range t.uidToBranchENIMap {
		usedBranches += len(branches)
	}
	// Count shared prefix ENIs
	for _, pool := range t.sgToBranchENIPool {
		usedBranches += len(pool)
	}

	if usedBranches+len(t.deleteQueue) < vpc.Limits[t.instance.Type()].BranchInterface {
		return true
	}
	return false
}

// assignVlanIdLocked assigns a free vlan id (caller must hold lock).
func (t *trunkENI) assignVlanIdLocked() (int, error) {
	for index, used := range t.usedVlanIds {
		if !used {
			t.usedVlanIds[index] = true
			return index, nil
		}
	}
	return 0, fmt.Errorf("failed to find free vlan id in the available %d ids", len(t.usedVlanIds))
}

// freeVlanIdLocked frees a vlan id (caller must hold lock).
func (t *trunkENI) freeVlanIdLocked(vlanId int) {
	isUsed := t.usedVlanIds[vlanId]
	if !isUsed {
		trunkENIOperationsErrCount.WithLabelValues("free_unused_vlan_id").Inc()
		t.log.Error(fmt.Errorf("failed to free a unused vlan id"), "", "vlan id", vlanId)
		return
	}
	t.usedVlanIds[vlanId] = false
}

// DeleteAllBranchENIs deletes all the branch ENIs associated with the trunk and all the ENIs present in the cool down
// queue, this is the last API call to the the Trunk ENI before it is removed from cache
func (t *trunkENI) DeleteAllBranchENIs() {
	// Delete all the branch used by the pod on this trunk ENI
	// Since after this call, the trunk will be removed from cache. No need to clean up its branch map
	for _, podENIs := range t.uidToBranchENIMap {
		for _, eni := range podENIs {
			err := t.deleteENI(eni)
			if err != nil {
				// Just log, if the ENI still exists it can be removed by the dangling ENI cleaner routine
				t.log.Error(err, "failed to delete eni", "eni id", eni.ID)
			}
		}
	}

	// Delete all shared prefix branch ENIs
	for _, pool := range t.sgToBranchENIPool {
		for _, sharedENI := range pool {
			err := t.deleteENI(sharedENI.ENIDetail)
			if err != nil {
				t.log.Error(err, "failed to delete shared prefix eni", "eni id", sharedENI.ENIDetail.ID)
			}
		}
	}
	t.sgToBranchENIPool = make(map[string][]*BranchENIWithPrefix)
	t.uidToPrefixAllocation = make(map[string]*PrefixAllocation)

	// Delete all the branch ENI present in the cool down queue
	for _, eni := range t.deleteQueue {
		err := t.deleteENI(eni)
		if err != nil {
			// Just log, if the ENI still exists it can be removed by the dangling ENI cleaner routine
			t.log.Error(err, "failed to delete eni", "eni id", eni.ID)
		}
	}
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
		eni.deletionTimeStamp = time.Now()
		t.deleteQueue = append(t.deleteQueue, eni)
	}

	delete(t.uidToBranchENIMap, UID)

	t.log.Info("moved branch network interfaces to delete queue", "Interfaces",
		branchENIs, "UID", UID)
}

func (t *trunkENI) DeleteCooledDownENIs() {
	// Process prefix IP cooldowns and drain fully-freed ENIs
	if len(t.sgToBranchENIPool) > 0 {
		t.processPrefixCoolDowns()
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

// processPrefixCoolDowns processes IP-level cooldowns for shared prefix ENIs.
// Fully drained ENIs are pushed to the ENI delete queue.
func (t *trunkENI) processPrefixCoolDowns() {
	t.lock.Lock()
	defer t.lock.Unlock()

	cooldownPeriod := cooldown.GetCoolDown().GetCoolDownPeriod()

	for sgKey, pool := range t.sgToBranchENIPool {
		var remaining []*BranchENIWithPrefix
		for _, sharedENI := range pool {
			fullyDrained := sharedENI.ProcessCoolDown(cooldownPeriod)
			if fullyDrained {
				// All IPs freed and cooled down — push the ENI itself to delete queue
				sharedENI.ENIDetail.deletionTimeStamp = time.Now()
				t.deleteQueue = append(t.deleteQueue, sharedENI.ENIDetail)
				t.log.Info("shared prefix ENI fully drained, queued for deletion",
					"eni", sharedENI.ENIDetail.ID, "prefixes", sharedENI.PrefixCIDRs)
			} else {
				remaining = append(remaining, sharedENI)
			}
		}
		if len(remaining) == 0 {
			delete(t.sgToBranchENIPool, sgKey)
		} else {
			t.sgToBranchENIPool[sgKey] = remaining
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

	t.deleteQueue = append(t.deleteQueue, eni)
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
	for _, pool := range t.sgToBranchENIPool {
		usedBranches += len(pool)
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
