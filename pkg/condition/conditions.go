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
	"strconv"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type condition struct {
	hasDataStoreSynced *bool
	log                logr.Logger
	K8sAPI             k8s.K8sWrapper
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

var (
	conditionWindowsIPAMEnabled = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "windows_ipam_enabled",
			Help: "Binary value to indicate whether user has set enable-windows-ipam to true",
		})

	prometheusRegistered = false
)

func prometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(
			conditionWindowsIPAMEnabled,
		)
	}

	prometheusRegistered = true
}

func NewControllerConditions(dataStoreSyncFlag *bool, log logr.Logger, k8sApi k8s.K8sWrapper) Conditions {
	prometheusRegister()
	conditionWindowsIPAMEnabled.Set(0)

	return &condition{
		hasDataStoreSynced: dataStoreSyncFlag,
		log:                log,
		K8sAPI:             k8sApi,
	}
}

func (c *condition) WaitTillPodDataStoreSynced() {
	for !*c.hasDataStoreSynced {
		c.log.Info("waiting for controller to sync")
		time.Sleep(CheckDataStoreSyncedInterval)
	}
}

func (c *condition) IsWindowsIPAMEnabled() bool {
	// Return false if configmap not present/any errors
	vpcCniConfigMap, err := c.K8sAPI.GetConfigMap(config.VpcCniConfigMapName)

	if err == nil && len(vpcCniConfigMap.Data) > 0 {
		if val, ok := vpcCniConfigMap.Data[config.EnableWindowsIPAMKey]; ok {
			enableWinIpamVal, err := strconv.ParseBool(val)
			if err == nil && enableWinIpamVal {
				conditionWindowsIPAMEnabled.Set(1)
				return true
			}
		}
	}

	conditionWindowsIPAMEnabled.Set(0)
	return false
}

// TODO: Add implementation later
func (c *condition) IsPodSGPEnabled() bool {
	panic("implement me")
}
