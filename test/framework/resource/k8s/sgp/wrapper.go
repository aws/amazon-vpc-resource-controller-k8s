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

package sgp

import (
	"context"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateSecurityGroupPolicy(k8sClient client.Client, ctx context.Context,
	securityGroupPolicy *v1beta1.SecurityGroupPolicy) {
	By("creating the security group policy")
	err := k8sClient.Create(ctx, securityGroupPolicy)
	Expect(err).NotTo(HaveOccurred())
}

func UpdateSecurityGroupPolicy(k8sClient client.Client, ctx context.Context,
	securityGroupPolicy *v1beta1.SecurityGroupPolicy, securityGroups []string) {

	// Before updating the SGP wait for some time to allow the cache to update with
	// recently created SGP
	time.Sleep(utils.PollIntervalShort)

	By("updating the security group policy")
	updatedSgp := &v1beta1.SecurityGroupPolicy{}
	err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      securityGroupPolicy.Name,
		Namespace: securityGroupPolicy.Namespace,
	}, updatedSgp)
	Expect(err).ToNot(HaveOccurred())

	updatedSgp.Spec.SecurityGroups.Groups = securityGroups
	err = k8sClient.Update(ctx, updatedSgp)
	Expect(err).NotTo(HaveOccurred())
}
