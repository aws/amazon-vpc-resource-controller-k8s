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

package pod

import (
	"fmt"
	"strings"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

// PodConverter implements the interface to convert k8s pod object to a stripped down
// version of pod to save on memory utilized
type PodConverter struct {
	K8sResource     string
	K8sResourceType runtime.Object
}

// ConcertObject converts original pod object to stripped down pod object
func (c *PodConverter) ConvertObject(originalObj interface{}) (convertedObj interface{}, err error) {
	pod, ok := originalObj.(*v1.Pod)
	if !ok {
		return nil, fmt.Errorf("failed to convert object to pod")
	}
	return c.StripDownPod(pod), nil
}

// ConcertList converts the original pod list to stripped down list of pod objects
func (c *PodConverter) ConvertList(originalList interface{}) (convertedList interface{}, err error) {
	podList, ok := originalList.(*v1.PodList)
	if !ok {
		return nil, fmt.Errorf("faield to convert object to pod list")
	}
	// We need to set continue in order to allow the pagination to work on converted
	// pod list object
	strippedPodList := v1.PodList{
		ListMeta: metaV1.ListMeta{
			Continue:        podList.Continue,
			ResourceVersion: podList.ResourceVersion,
		},
	}
	for _, pod := range podList.Items {
		strippedPod := c.StripDownPod(&pod)
		strippedPodList.Items = append(strippedPodList.Items, *strippedPod)
	}
	return &strippedPodList, nil
}

// Resource to watch and list
func (c *PodConverter) Resource() string {
	return c.K8sResource
}

// ResourceType to watch and list
func (c *PodConverter) ResourceType() runtime.Object {
	return c.K8sResourceType
}

// NodeNameIndexer returns indexer to index in the data store using node name
func NodeNameIndexer() cache.Indexers {
	indexer := map[string]cache.IndexFunc{}
	indexer[NodeNameSpec] = func(obj interface{}) (strings []string, err error) {
		return []string{obj.(*v1.Pod).Spec.NodeName}, nil
	}
	return indexer
}

// NSKeyIndexer is the key function to index the pod object using namespace/name
func (c *PodConverter) Indexer(obj interface{}) (string, error) {
	pod := obj.(*v1.Pod)
	return types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}.String(), nil
}

// StripDownPod removes all the extra details from pod that are not
// required by the controller.
func (c *PodConverter) StripDownPod(pod *v1.Pod) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Name:              pod.Name,
			Namespace:         pod.Namespace,
			UID:               pod.UID,
			DeletionTimestamp: pod.DeletionTimestamp,
			Annotations:       getVPCControllerAnnotations(pod.Annotations),
		},
		Spec: v1.PodSpec{
			Containers:         getContainersWithVPCLimits(pod.Spec.Containers),
			ServiceAccountName: pod.Spec.ServiceAccountName,
			NodeName:           pod.Spec.NodeName,
		},
	}
}

// getVPCControllerAnnotations returns only the annotations that were marked by VPC
// Resource controller
func getVPCControllerAnnotations(annotations map[string]string) map[string]string {
	strippedDownAnnotations := map[string]string{}
	for key, val := range annotations {
		if strings.HasPrefix(key, config.VPCResourcePrefix) {
			strippedDownAnnotations[key] = val
		}
	}
	return strippedDownAnnotations
}

// getContainersWithVPCLimits returns only the container limits for vpc controller
// resources
func getContainersWithVPCLimits(containers []v1.Container) []v1.Container {
	var strippedContainers []v1.Container
	for _, container := range containers {
		add := false
		strippedContainer := v1.Container{
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{},
			},
		}
		for limitKey, limitVal := range container.Resources.Requests {
			if strings.HasPrefix(limitKey.String(), config.VPCResourcePrefix) {
				strippedContainer.Resources.Requests[limitKey] = limitVal
				add = true
			}
		}
		if add {
			strippedContainers = append(strippedContainers, strippedContainer)
		}
	}
	return strippedContainers
}
