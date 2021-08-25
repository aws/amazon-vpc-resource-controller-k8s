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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go/aws"
	awsEC2 "github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// MaxAllocatableVlanIds is the maximum number of Vlan Ids that can be allocated per trunk.
	MaxAllocatableVlanIds = 121
	// CoolDownPeriod is the period to wait before deleting the branch ENI for propagation of ip tables rule for deleted pod
	CoolDownPeriod = time.Second * 30
	// MaxDeleteRetries is the maximum number of times the ENI will be retried before being removed from the delete queue
	MaxDeleteRetries = 3
)

var (
	InterfaceTypeTrunk   = "trunk"
	TrunkEniDescription  = "trunk-eni"
	BranchEniDescription = "branch-eni"
)

var (
	ErrCurrentlyAtMaxCapacity = fmt.Errorf("cannot create more branches at this point as used branches plus the " +
		"delete queue is at max capacity")
)

var (
	trunkENIOperationsErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trunk_eni_operations_err_count",
			Help: "The number of errors encountered for operations on Trunk ENI",
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
	Reconcile(pods []v1.Pod) error
	// PushENIsToFrontOfDeleteQueue pushes the eni network interfaces to the front of the delete queue
	PushENIsToFrontOfDeleteQueue(*v1.Pod, []*ENIDetails)
	// DeleteAllBranchENIs deletes all the branch ENI associated with the trunk and also clears the cool down queue
	DeleteAllBranchENIs()
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
}

// PodENI is a json convertible structure that stores the Branch ENI details that can be
// used by the CNI plugin or the component consuming the resource
type ENIDetails struct {
	// BranchENId is the network interface id of the branch interface
	ID string `json:"eniId"`
	// MacAdd is the MAC address of the network interface
	MACAdd string `json:"ifAddress"`
	// BranchIp is the primary IP of the branch Network interface
	IPV4Addr string `json:"privateIp"`
	// VlanId is the VlanId of the branch network interface
	VlanID int `json:"vlanId"`
	// SubnetCIDR is the CIDR block of the subnet
	SubnetCIDR string `json:"subnetCidr"`
	// deletionTimeStamp is the time when the pod was marked deleted.
	deletionTimeStamp time.Time
	// deleteRetryCount is the
	deleteRetryCount int
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
	}
}

func PrometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(trunkENIOperationsErrCount)
		prometheusRegistered = true
	}
}

// InitTrunk initializes the trunk network interface and all it's associated branch network interfaces by making calls
// to EC2 API
func (t *trunkENI) InitTrunk(instance ec2.EC2Instance, podList []v1.Pod) error {
	instanceID := t.instance.InstanceID()
	log := t.log.WithValues("request", "initialize", "instance ID", instance)

	nwInterfaces, err := t.ec2ApiHelper.GetInstanceNetworkInterface(&instanceID)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("describe_instance_nw_interface").Inc()
		return err
	}

	// Get trunk network interface
	for _, nwInterface := range nwInterfaces {
		// It's possible to get an empty network interface response if the instnace
		// is being deleted.
		if nwInterface == nil || nwInterface.InterfaceType == nil {
			return fmt.Errorf("received an empty network interface response "+
				"from EC2 %+v", nwInterface)
		}
		if *nwInterface.InterfaceType == "trunk" {
			t.trunkENIId = *nwInterface.NetworkInterfaceId
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

		trunk, err := t.ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceID, aws.String(t.instance.SubnetID()),
			t.instance.InstanceSecurityGroup(), nil, &freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, 0)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("create_trunk_eni").Inc()
			log.Error(err, "failed to create trunk interface")
			return err
		}

		t.trunkENIId = *trunk.NetworkInterfaceId
		log.Info("created a new trunk interface", "trunk id", t.trunkENIId)

		return nil
	}

	// Get the list of branch ENIs
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&t.trunkENIId)
	if err != nil {
		return err
	}

	// Convert the list of interfaces to a set
	associatedBranchInterfaces := make(map[string]*awsEC2.NetworkInterface)
	for _, branchInterface := range branchInterfaces {
		associatedBranchInterfaces[*branchInterface.NetworkInterfaceId] = branchInterface
	}

	// From the list of pods on the given node, and the branch ENIs from EC2 API call rebuild the internal cache
	for _, pod := range podList {
		eniListFromPod := t.getBranchInterfacesUsedByPod(&pod)
		if len(eniListFromPod) == 0 {
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

// Reconcile reconciles the state from the API Server to the internal cache of EC2 Branch Interfaces, if the controller
// missed some delete events the reconcile method will perform cleanup for the dangling interfaces
func (t *trunkENI) Reconcile(pods []v1.Pod) error {
	// Perform under lock to block new pods being added/removed concurrently
	t.lock.Lock()
	defer t.lock.Unlock()

	currentPodSet := make(map[string]struct{})
	var isPresent struct{}
	for _, pod := range pods {
		currentPodSet[string(pod.UID)] = isPresent
	}

	for uid, branchENIs := range t.uidToBranchENIMap {
		_, exists := currentPodSet[uid]
		if !exists {
			for _, eni := range branchENIs {
				// Pod could have been deleted recently, set the timestamp to current time as controller is not aware of the actual time.
				eni.deletionTimeStamp = time.Now()
				t.deleteQueue = append(t.deleteQueue, eni)
			}
			delete(t.uidToBranchENIMap, uid)

			t.log.Info("deleted pod that doesn't exist anymore", "pod uid", uid,
				"eni", branchENIs)
		}
	}

	return nil
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
		securityGroups = t.instance.InstanceSecurityGroup()
	}

	var newENIs []*ENIDetails
	var err error
	var nwInterface *awsEC2.NetworkInterface
	var vlanID int

	for i := 0; i < eniCount; i++ {
		// Assign VLAN
		vlanID, err = t.assignVlanId()
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("assign_vlan_id").Inc()
			break
		}

		// Vlan ID tag workaround, as describe trunk association is not supported with assumed role
		tags := []*awsEC2.Tag{
			{
				Key:   aws.String(config.VLandIDTag),
				Value: aws.String(strconv.Itoa(vlanID)),
			},
			{
				Key:   aws.String(config.TrunkENIIDTag),
				Value: &t.trunkENIId,
			},
		}
		// Create Branch ENI
		nwInterface, err = t.ec2ApiHelper.CreateNetworkInterface(&BranchEniDescription,
			aws.String(t.instance.SubnetID()), securityGroups, tags, 0, nil)
		if err != nil {
			t.freeVlanId(vlanID)
			break
		}

		newENI := &ENIDetails{ID: *nwInterface.NetworkInterfaceId, MACAdd: *nwInterface.MacAddress,
			IPV4Addr: *nwInterface.PrivateIpAddress, SubnetCIDR: t.instance.SubnetCidrBlock(), VlanID: vlanID}

		newENIs = append(newENIs, newENI)

		// Associate Branch to trunk
		_, err = t.ec2ApiHelper.AssociateBranchToTrunk(&t.trunkENIId, nwInterface.NetworkInterfaceId, vlanID)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("associate_branch").Inc()
			break
		}
	}

	if err != nil {
		log.Error(err, "failed to create ENI, moving the ENI to delete list")
		// Moving to delete list, because it has all the retrying logic in case of failure
		t.PushENIsToFrontOfDeleteQueue(nil, newENIs)
		return nil, err
	}

	t.addBranchToCache(string(pod.UID), newENIs)

	log.V(1).Info("successfully created branch interface/s", "interface/s", newENIs,
		"security group used", securityGroups)

	return newENIs, nil
}

// DeleteAllBranchENIs deletes all the branch ENIs associated with the trunk and all the ENIs present in the cool down
// queue, this is the last API call to the the Trunk ENI before it is removed from cache
func (t *trunkENI) DeleteAllBranchENIs() {
	// Delete all the branch used by the pod on this trunk ENI
	for _, podENIs := range t.uidToBranchENIMap {
		for _, eni := range podENIs {
			err := t.deleteENI(eni)
			if err != nil {
				// Just log, if the ENI still exists it can be removed by the dangling ENI cleaner routine
				t.log.Error(err, "failed to delete eni", "eni id", eni.ID)
			}
		}
	}

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
		t.log.Info("failed to find Branch ENI in cache, it could have been released if pod"+
			"succeeded/failed before being deleted", "uid", UID)
		trunkENIOperationsErrCount.WithLabelValues("get_branch_from_cache").Inc()
		return
	}

	for _, eni := range branchENIs {
		eni.deletionTimeStamp = time.Now()
		t.deleteQueue = append(t.deleteQueue, eni)
	}

	delete(t.uidToBranchENIMap, UID)

	t.log.Info("moved branch network interfaces to delete queue", "interface/s",
		branchENIs, "uid", UID)
}

func (t *trunkENI) DeleteCooledDownENIs() {
	for eni, hasENI := t.popENIFromDeleteQueue(); hasENI; eni, hasENI = t.popENIFromDeleteQueue() {
		if eni.deletionTimeStamp.IsZero() ||
			time.Now().After(eni.deletionTimeStamp.Add(CoolDownPeriod)) {
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
	// Delete Branch network interface first
	err = t.ec2ApiHelper.DeleteNetworkInterface(&eniDetail.ID)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("delete_branch").Inc()
		return err
	}

	t.log.Info("deleted eni", "eni details", eniDetail)

	// Free vlan id used by the branch ENI
	if eniDetail.VlanID != 0 {
		t.freeVlanId(eniDetail.VlanID)
	}

	return
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

// GetBranchInterfacesFromEC2 returns the list of branch interfaces associated with the trunk ENI. This is not supported
// yet
func (t *trunkENI) GetBranchInterfacesFromEC2() (eniDetails []*ENIDetails, err error) {
	// Get the branch associated with the trunk and store the result in the cache
	associations, err := t.ec2ApiHelper.DescribeTrunkInterfaceAssociation(&t.trunkENIId)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("describe_trunk_assoc").Inc()
		err = fmt.Errorf("failed to describe associations for trunk %s: %v", t.trunkENIId, err)
		return
	}

	// Return if no branches are associated with the trunk
	if associations == nil || len(associations) == 0 {
		t.log.V(1).Info("trunk has no associated branch interfaces", "trunk id", t.trunkENIId)
		return
	}

	// For each association build the map of branch ENIs with the interface id and the vlan id
	for _, association := range associations {
		eniDetail := &ENIDetails{
			ID:         *association.BranchInterfaceId,
			VlanID:     int(*association.VlanId),
			SubnetCIDR: t.instance.SubnetCidrBlock(),
		}
		eniDetails = append(eniDetails, eniDetail)
	}

	t.log.V(1).Info("loaded trunk associations", "trunk id", t.trunkENIId, "associations", eniDetails)

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

func (t *trunkENI) getVlanIdFromTag(tags []*awsEC2.Tag) (int, error) {

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
