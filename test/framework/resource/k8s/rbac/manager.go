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

package rbac

import (
	"context"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	GetClusterRole(name string) (*v1.ClusterRole, error)
	PatchClusterRole(updatedRole *v1.ClusterRole) error
}

type defaultManager struct {
	k8sClient client.Client
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

func (d *defaultManager) GetClusterRole(name string) (*v1.ClusterRole, error) {
	clusterRole := &v1.ClusterRole{}
	err := d.k8sClient.Get(context.TODO(), types.NamespacedName{
		Name: name,
	}, clusterRole)
	return clusterRole, err
}

func (d *defaultManager) PatchClusterRole(updatedRole *v1.ClusterRole) error {
	clusterRole, err := d.GetClusterRole(updatedRole.Name)
	if err != nil {
		return err
	}
	err = d.k8sClient.Patch(context.TODO(), updatedRole, client.MergeFrom(clusterRole))
	return err
}
