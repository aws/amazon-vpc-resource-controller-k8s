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

package configmap

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TODO: create a manager and poll to wait for operation
func CreateConfigMap(k8sClient client.Client, ctx context.Context, configmap *v1.ConfigMap) {
	By("creating the configmap")
	err := k8sClient.Create(ctx, configmap)
	Expect(err).NotTo(HaveOccurred())

}

func DeleteConfigMap(k8sClient client.Client, ctx context.Context, configmap *v1.ConfigMap) {
	By("deleting the configmap")
	err := k8sClient.Delete(ctx, configmap)
	Expect(err).NotTo(HaveOccurred())
}

func UpdateConfigMap(k8sClient client.Client, ctx context.Context, configmap *v1.ConfigMap, data map[string]string) {
	By("updating the configmap")
	observedConfigMap := &v1.ConfigMap{}
	err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: configmap.Namespace,
		Name:      configmap.Name,
	}, observedConfigMap)
	Expect(err).NotTo(HaveOccurred())

	updatedConfigmap := observedConfigMap.DeepCopy()
	updatedConfigmap.Data = data
	err = k8sClient.Update(ctx, updatedConfigmap)
	Expect(err).NotTo(HaveOccurred())
}
