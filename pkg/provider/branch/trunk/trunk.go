/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package trunk

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	awsEC2 "github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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
	// MarkPodBeingDeleted marks that the pod is being deleted
	MarkPodBeingDeleted(UID types.UID, podNamespace string, podName string) error
	// PushBranchENIsToCoolDownQueue pushes the branch interface belonging to the pod to the cool down queue
	PushBranchENIsToCoolDownQueue(podNamespace string, podName string) error
	// DeleteCooledDownENIs deletes the interfaces that have been sitting in the queue for cool down period
	DeleteCooledDownENIs()
	// Reconcile compares the cache state with the list of pods to identify events that were missed and clean up the dangling interfaces
	Reconcile(pods []v1.Pod) error
	// PushENIsToFrontOfDeleteQueue pushes the eni network interfaces to the front of the delete queue
	PushENIsToFrontOfDeleteQueue([]*ENIDetails)
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
	// instanceId is the id of the instance that owns the trunk interface
	instanceId string
	// subnetId is the id of the subnet of the trunk network interface
	subnetId string
	// subnetCidrBlock is the cidr block of the subnet of trunk interface
	subnetCidrBlock string
	// usedVlanIds is the list of boolean value representing the used vlan ids
	usedVlanIds []bool
	// branchENIs is the list of BranchENIs associated with the trunk
	branchENIs map[string]*BranchENIs
	// deleteQueue is the queue of ENIs that are being cooled down before being deleted
	deleteQueue []*ENIDetails
}

type BranchENIs struct {
	// isPodDeleted denotes if the pod has been deleted and branch is safe to remove
	isPodBeingDeleted bool
	// UID is the unique identifier of the pod that owns the Branch ENI
	UID types.UID
	// podENI is the json convertible data structure with the pod details
	branchENIDetails []*ENIDetails
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
func NewTrunkENI(logger logr.Logger, instanceId string, instanceSubnetId string,
	instanceSubnetCidrBlock string, helper api.EC2APIHelper) TrunkENI {

	availVlans := make([]bool, MaxAllocatableVlanIds)
	// VlanID 0 cannot be assigned.
	availVlans[0] = true

	return &trunkENI{
		log:             logger,
		instanceId:      instanceId,
		subnetId:        instanceSubnetId,
		subnetCidrBlock: instanceSubnetCidrBlock,
		usedVlanIds:     availVlans,
		ec2ApiHelper:    helper,
		branchENIs:      make(map[string]*BranchENIs),
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
	log := t.log.WithValues("request", "initialize", "instance ID", t.instanceId)

	// Get trunk network interface
	trunkInterface, err := t.ec2ApiHelper.GetTrunkInterface(&t.instanceId)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("get_trunk").Inc()
		return fmt.Errorf("failed to find trunk interface: %v", err)
	}

	// Trunk interface doesn't exists, try to create a new trunk interface
	if trunkInterface == nil {
		freeIndex, err := instance.GetHighestUnusedDeviceIndex()
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("find_free_index").Inc()
			log.Error(err, "failed to find free device index")
			return err
		}

		trunk, err := t.ec2ApiHelper.CreateAndAttachNetworkInterface(&t.instanceId, &t.subnetId, nil,
			&freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, 0)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("create_trunk_eni").Inc()
			log.Error(err, "failed to create trunk interface")
			return err
		}

		t.trunkENIId = *trunk.NetworkInterfaceId
		log.Info("created a new trunk interface", "trunk id", t.trunkENIId)

		return nil
	}

	t.trunkENIId = *trunkInterface.NetworkInterfaceId

	// Get the list of branch ENIs associated with the Trunk ENI
	eniListFromEC2, err := t.GetBranchInterfacesFromEC2()
	if err != nil {
		return err
	}
	if eniListFromEC2 == nil {
		log.V(1).Info("no branch associated with the trunk", "trunk id", t.trunkENIId)
	}
	eniMapFromEC2 := t.getBranchInterfaceMap(eniListFromEC2)

	// From the list of pods on the given node, and the branch ENIs from EC2 API call rebuild the internal cache
	for _, pod := range podList {
		eniListFromPod := t.getBranchInterfacesUsedByPod(&pod)
		if len(eniListFromPod) == 0 {
			continue
		}
		namespacedName := getPodName(pod.Namespace, pod.Name)
		branchENIs := &BranchENIs{
			UID:              pod.UID,
			branchENIDetails: []*ENIDetails{},
		}
		for _, eni := range eniListFromPod {
			ec2ENICopy, isPresent := eniMapFromEC2[eni.ID]
			if !isPresent {
				t.log.Error(fmt.Errorf("eni allocated to pod not found in ec2"), "eni not found", "eni", eni)
				trunkENIOperationsErrCount.WithLabelValues("get_branch_eni_from_ec2").Inc()
				continue
			}
			t.markVlanAssigned(ec2ENICopy.VlanID)

			branchENIs.branchENIDetails = append(branchENIs.branchENIDetails, ec2ENICopy)
			delete(eniMapFromEC2, eni.ID)
		}
		t.branchENIs[namespacedName] = branchENIs
	}

	// Delete the pods that don't belong to any pod.
	for _, eni := range eniMapFromEC2 {
		t.log.Info("pushing eni to delete queue as no pod owns it", "eni", eni)
		eni.deletionTimeStamp = time.Now()
		// Even thought the ENI is going to be deleted still mark Vlan ID assigned as ENI will sit in cool down queue for a while
		t.markVlanAssigned(eni.VlanID)
		t.pushENIToDeleteQueue(eni)
	}

	log.V(1).Info("successfully initialized trunk with all associated branch interfaces",
		"trunk", t.trunkENIId, "branch interfaces", t.branchENIs)

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
		currentPodSet[getPodName(pod.Namespace, pod.Name)] = isPresent
	}

	for namespacedName, branch := range t.branchENIs {
		_, exists := currentPodSet[namespacedName]
		if !exists {
			for _, eni := range branch.branchENIDetails {
				// Pod could have been deleted recently, set the timestamp to current time as controller is not aware of the actual time.
				eni.deletionTimeStamp = time.Now()
				t.deleteQueue = append(t.deleteQueue, eni)
			}
			delete(t.branchENIs, namespacedName)

			t.log.Info("deleted pod that doesn't exist anymore", "pod", namespacedName,
				"eni", branch.branchENIDetails)
		}
	}

	return nil
}

// CreateAndAssociateBranchToTrunk creates a new branch network interface and associates the branch to the trunk
// network interface. It returns a Json convertible structure which has all the required details of the branch ENI
func (t *trunkENI) CreateAndAssociateBranchENIs(pod *v1.Pod, securityGroups []string, eniCount int) ([]*ENIDetails, error) {
	log := t.log.WithValues("request", "create", "pod namespace", pod.Namespace, "pod name", pod.Name)
	podNamespacedName := getPodName(pod.Namespace, pod.Name)

	branchENI, isPresent := t.getBranchFromCache(podNamespacedName)
	if isPresent {
		// Possible when older pod with same namespace and name is still being deleted
		return nil, fmt.Errorf("cannot create new eni entry already exist, older entry : %v", *branchENI)
	}

	var newENIs []*ENIDetails
	var err error
	var nwInterface *awsEC2.NetworkInterface
	var vlanID int

	for i := 0; i < eniCount; i++ {
		// Create Branch ENI
		nwInterface, err = t.ec2ApiHelper.CreateNetworkInterface(&BranchEniDescription, &t.subnetId, securityGroups,
			0, nil)
		if err != nil {
			break
		}

		newENI := &ENIDetails{ID: *nwInterface.NetworkInterfaceId, MACAdd: *nwInterface.MacAddress,
			IPV4Addr: *nwInterface.PrivateIpAddress, SubnetCIDR: t.subnetCidrBlock}

		newENIs = append(newENIs, newENI)

		// Assign VLAN
		vlanID, err = t.assignVlanId()
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("assign_vlan_id").Inc()
			break
		}
		newENI.VlanID = vlanID

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
		t.PushENIsToFrontOfDeleteQueue(newENIs)
		return nil, err
	}

	t.addBranchToCache(podNamespacedName, &BranchENIs{
		UID:              pod.UID,
		branchENIDetails: newENIs,
	})

	log.V(1).Info("successfully created branch interface/s", "interface/s", newENIs)

	return newENIs, nil
}

// DeleteBranchNetworkInterface deletes the branch network interface and returns an error in case of failure to delete
func (t *trunkENI) PushBranchENIsToCoolDownQueue(podNamespace string, podName string) error {
	// Lock is required as Reconciler is also performing operation concurrently
	t.lock.Lock()
	defer t.lock.Unlock()

	podNamespacedName := getPodName(podNamespace, podName)

	branchENI, isPresent := t.branchENIs[podNamespacedName]
	if !isPresent {
		trunkENIOperationsErrCount.WithLabelValues("get_branch_from_cache").Inc()
		return fmt.Errorf("failed to find branch ENI in cache for pod %s", podNamespacedName)
	}
	if !branchENI.isPodBeingDeleted {
		trunkENIOperationsErrCount.WithLabelValues("delete_event_before_deleting").Inc()
		return fmt.Errorf("recieved delete event directly for %s before recieving deleting event, not deleting the"+
			" branch/es %v", podNamespacedName, branchENI.branchENIDetails)
	}

	for _, eni := range branchENI.branchENIDetails {
		eni.deletionTimeStamp = time.Now()
		t.deleteQueue = append(t.deleteQueue, eni)
	}

	delete(t.branchENIs, podNamespacedName)

	t.log.Info("moved branch network interfaces to delete queue", "interface/s",
		branchENI.branchENIDetails, "pod namespace", podNamespace, "pod name", podName)

	return nil
}

// MarkBranchBeingDeleted marks branch ENI for the given pod as being deleted. By marking the pod as being deleted we
// can ensure we are not deleting the Branch ENI for a new pod with same namespace and name.
func (t *trunkENI) MarkPodBeingDeleted(UID types.UID, podNamespace string, podName string) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	podNamespacedName := getPodName(podNamespace, podName)
	// Get the associated branch
	branchENI, isPresent := t.branchENIs[podNamespacedName]
	if !isPresent {
		return fmt.Errorf("failed to find branch ENI for pod %s", podNamespacedName)
	}

	if branchENI.UID != UID {
		return fmt.Errorf("branch doesn't belong to the given pod %s, actual owner %s", UID, branchENI.UID)
	}

	if !branchENI.isPodBeingDeleted {
		branchENI.isPodBeingDeleted = true
		t.log.Info("marked the pod as being deleted",
			"uid", UID, "namespace", podNamespace, "name", podName)
	}

	return nil
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
				t.PushENIsToFrontOfDeleteQueue([]*ENIDetails{eni})
				continue
			}
			t.log.V(1).Info("deleted eni successfully", "eni", eni, "deletion time", time.Now(),
				"pushed to queue time", eni.deletionTimeStamp)
		} else {
			// Since the current item is not cooled down so the items added after it would not be cooled down either
			t.PushENIsToFrontOfDeleteQueue([]*ENIDetails{eni})
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

// GetBranchInterfacesFromEC2 returns the list of branch interfaces associated with the trunk ENI
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
			SubnetCIDR: t.subnetCidrBlock,
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
func (t *trunkENI) PushENIsToFrontOfDeleteQueue(eniList []*ENIDetails) {
	t.lock.Lock()
	defer t.lock.Unlock()

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

// getPodName returns the pod namespace with name
func getPodName(podNamespace string, podName string) string {
	if podNamespace == "" {
		podNamespace = "default"
	}
	return podNamespace + "/" + podName
}

// addBranchToCache adds the given branch to the cache if not already present
func (t *trunkENI) addBranchToCache(ownerID string, branchENI *BranchENIs) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if _, ok := t.branchENIs[ownerID]; ok {
		t.log.Info("branch eni already exist not adding again", "request", branchENI)
		return
	}

	t.branchENIs[ownerID] = branchENI
}

// getBranchFromCache returns the branch from the cache
func (t *trunkENI) getBranchFromCache(ownerID string) (branch *BranchENIs, isPresent bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	branch, isPresent = t.branchENIs[ownerID]
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
