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
)

func CreateConfigMap(manager Manager, ctx context.Context, configmap *v1.ConfigMap) {
	By("creating the configmap")
	err := manager.CreateConfigMap(ctx, configmap)
	Expect(err).NotTo(HaveOccurred())
}

func DeleteConfigMap(manager Manager, ctx context.Context, configmap *v1.ConfigMap) {
	By("deleting the configmap")
	err := manager.DeleteConfigMap(ctx, configmap)
	Expect(err).NotTo(HaveOccurred())
}

func UpdateConfigMap(manager Manager, ctx context.Context, configmap *v1.ConfigMap) {
	By("updating the configmap")
	err := manager.UpdateConfigMap(ctx, configmap)
	Expect(err).NotTo(HaveOccurred())
}
