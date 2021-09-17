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

package condition

import (
	"github.com/go-logr/logr"
	"time"
)

type condition struct {
	hasDataStoreSynced *bool
	log                logr.Logger
}

const CheckDataStoreSyncedInterval = time.Second * 10

type Conditions interface {
	// WaitTillPodDataStoreSynced waits till the Pod Data Store has synced
	// using the custom controller
	WaitTillPodDataStoreSynced()
	// IsWindowsIPAMEnabled to process events only when Windows IPAM is enabled
	// by the user
	IsWindowsIPAMEnabled() bool
	// IsPodSGPEnabled to process events only when Security Group for Pods feature
	// is enabled by the user
	IsPodSGPEnabled() bool
}

func NewControllerConditions(dataStoreSyncFlag *bool, log logr.Logger) Conditions {
	return &condition{
		hasDataStoreSynced: dataStoreSyncFlag,
		log:                log,
	}
}

func (c *condition) WaitTillPodDataStoreSynced() {
	for !*c.hasDataStoreSynced {
		c.log.Info("waiting for controller to sync")
		time.Sleep(CheckDataStoreSyncedInterval)
	}
}

// TODO: Add implementation later
func (c *condition) IsWindowsIPAMEnabled() bool {
	// TODO: Switch using ConfigMap
	return true
}

// TODO: Add implementation later
func (c *condition) IsPodSGPEnabled() bool {
	panic("implement me")
}
