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

package deployment

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
)

func CreateAndWaitForDeploymentToStart(deploymentManager Manager, ctx context.Context,
	deployment *appsv1.Deployment) *appsv1.Deployment {

	By("creating and waiting for the deployment to start")
	deployment, err := deploymentManager.CreateAndWaitUntilDeploymentReady(ctx, deployment)
	Expect(err).ToNot(HaveOccurred())

	return deployment
}
