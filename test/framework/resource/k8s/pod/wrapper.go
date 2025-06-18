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
	"context"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

func CreateAndWaitForPodToStart(podManager Manager, ctx context.Context, pod *v1.Pod) *v1.Pod {
	By("create the pod")
	pod, err := podManager.CreateAndWaitTillPodIsRunning(ctx, pod, utils.ResourceOperationTimeout)
	Expect(err).NotTo(HaveOccurred())

	return pod
}
