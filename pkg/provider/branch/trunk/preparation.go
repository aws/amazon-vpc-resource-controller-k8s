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

package trunk

import (
	"errors"
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	v1 "k8s.io/api/core/v1"
)

const (
	preparationPollPeriod = time.Second
	maxPreparationBackoff = 30 * time.Second
)

type PreparationDeferred struct {
	After time.Duration
	Err   error
}

func (deferred *PreparationDeferred) Error() string {
	if deferred.Err != nil {
		return deferred.Err.Error()
	}
	return "trunk preparation is in progress or waiting to retry"
}

func (deferred *PreparationDeferred) Unwrap() error {
	return deferred.Err
}

func (t *trunkENI) PrepareForAllocation(
	listRunningPods func() ([]v1.Pod, error),
) (ec2.EC2Instance, error) {
	if t.prepared.Load() {
		return nil, nil
	}
	if !t.prepareMu.TryLock() {
		return nil, &PreparationDeferred{After: preparationPollPeriod}
	}
	defer t.prepareMu.Unlock()
	if t.prepared.Load() {
		return nil, nil
	}
	if remaining := time.Until(t.nextPreparationAttempt); remaining > 0 {
		return nil, &PreparationDeferred{After: remaining}
	}
	instance, err := t.prepareBranchState(listRunningPods)
	if err != nil {
		t.preparationBackoff = min(max(2*t.preparationBackoff, preparationPollPeriod), maxPreparationBackoff)
		t.nextPreparationAttempt = time.Now().Add(t.preparationBackoff)
		trunkENIOperationsErrCount.WithLabelValues("recover_branch_state").Inc()
		return nil, &PreparationDeferred{After: t.preparationBackoff, Err: err}
	}
	t.preparationBackoff = 0
	t.nextPreparationAttempt = time.Time{}
	t.prepared.Store(true)
	branchENIOperationsSuccessCount.WithLabelValues("recover_branch_state").Inc()
	return instance, nil
}

func (t *trunkENI) prepareBranchState(
	listRunningPods func() ([]v1.Pod, error),
) (ec2.EC2Instance, error) {
	pods, err := listRunningPods()
	if err != nil {
		return nil, fmt.Errorf("listing running pods before branch inventory: %w", err)
	}
	restoredTrunkENIID := t.instance.RestoredTrunkENIID()
	if restoredTrunkENIID != "" {
		t.log.Info("starting restored trunk validation", "trunk", restoredTrunkENIID)
		err = t.validateRestoredNetworkInterfaces(restoredTrunkENIID)
	}
	if restoredTrunkENIID == "" || errors.Is(err, errNeedsColdInit) {
		t.log.Info("restored trunk requires cold initialization", "trunk", restoredTrunkENIID, "error", err)
		if err := t.instance.LoadDetails(t.ec2ApiHelper); err != nil {
			return nil, fmt.Errorf("loading instance for cold initialization: %w", err)
		}
		if err := t.coldInit(pods); err != nil {
			return nil, fmt.Errorf("cold initializing restored trunk: %w", err)
		}
		return t.instance, nil
	}
	if err != nil {
		return nil, fmt.Errorf("validating restored trunk before allocation: %w", err)
	}
	branchInterfaces, err := t.ec2ApiHelper.GetBranchNetworkInterface(&restoredTrunkENIID)
	if err != nil {
		return nil, fmt.Errorf("describing branch ENIs before allocation: %w", err)
	}
	state := t.buildRecoveredBranchState(pods, branchInterfaces)
	t.commitBranchState(restoredTrunkENIID, state)
	t.log.Info("validated restored trunk and recovered branch state before allocation",
		"trunk", restoredTrunkENIID,
		"ownedPods", len(state.uidToBranchENIMap),
		"orphanENIs", len(state.deleteQueue))
	return nil, nil
}
