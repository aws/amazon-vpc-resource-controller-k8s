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

package utils

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

/* This is a shared k8s cache helper for retrieving security group or other objects which can be shared access by controllers
or webhooks.
*/

type k8sCacheHelper struct {
	Client client.Client
	Log    logr.Logger
}

// NewK8sCacheHelper creates and returns a controller-runtime cache operator.
func NewK8sCacheHelper(client client.Client, log logr.Logger) K8sCacheHelper {
	return &k8sCacheHelper{
		Client: client,
		Log:    log,
	}
}

// GetSecurityGroupsFromPod returns security groups assigned to a testPod based on it's NamespacedName.
func (kch *k8sCacheHelper) GetSecurityGroupsFromPod(podId types.NamespacedName) ([]string, error) {
	pod := &corev1.Pod{}

	if err := kch.Client.Get(context.Background(), podId, pod); err != nil {
		return nil, err
	} else {
		return kch.GetPodSecurityGroups(pod)
	}
}
