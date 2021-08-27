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

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/deployment"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func RestartController(ctx context.Context, manager Manager, depManager deployment.Manager) string {

	ScaleControllerDeployment(ctx, depManager, 0)
	ScaleControllerDeployment(ctx, depManager, 2)

	By(fmt.Sprintf("waiting for the leader to take over lease"))
	time.Sleep(time.Second * 60)

	By("waiting till the new controller has the leader lease")
	leaderPod, err := manager.WaitTillControllerHasLeaderLease(ctx)
	Expect(err).ToNot(HaveOccurred())

	By(fmt.Sprintf("waiting for the leader %s to initalize", leaderPod))
	time.Sleep(time.Second * 30)

	return leaderPod
}

func ScaleControllerDeployment(ctx context.Context, deploymentManager deployment.Manager, replica int) {
	By(fmt.Sprintf("scaling down the controller deployment to %d", replica))
	err := deploymentManager.ScaleDeploymentAndWaitTillReady(ctx,
		Namespace,
		DeploymentName,
		int32(replica))
	Expect(err).ToNot(HaveOccurred())
}

func VerifyLeaseHolderIsSame(ctx context.Context, manager Manager,
	previousLeaseHolder string) {
	By("verifying the lease didn't switch")
	newLeaderName, err := manager.WaitTillControllerHasLeaderLease(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(newLeaderName).To(Equal(previousLeaseHolder))
}
