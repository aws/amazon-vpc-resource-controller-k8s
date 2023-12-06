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

package cooldown

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
)

// Global variable for CoolDownPeriod allows packages to Get and Set the coolDown period
var coolDown *cooldown

type cooldown struct {
	mu sync.RWMutex
	// CoolDownPeriod is the period to wait before deleting the branch ENI for propagation of ip tables rule for deleted pod
	coolDownPeriod time.Duration
}

type CoolDown interface {
	GetCoolDownPeriod() time.Duration
	SetCoolDownPeriod(time.Duration)
}

const (
	DefaultCoolDownPeriod = time.Second * 60
	MinimalCoolDownPeriod = time.Second * 30
)

// Initialize coolDown period by setting the value in configmap or to default
func InitCoolDownPeriod(k8sApi k8s.K8sWrapper, log logr.Logger) {
	coolDown = &cooldown{}
	coolDownPeriod, err := GetVpcCniConfigMapCoolDownPeriodOrDefault(k8sApi, log)
	if err != nil {
		log.Info("setting coolDown period to default", "cool down period", coolDownPeriod)
	}
	coolDown.SetCoolDownPeriod(coolDownPeriod)
}

func GetCoolDown() CoolDown {
	return coolDown
}

func GetVpcCniConfigMapCoolDownPeriodOrDefault(k8sApi k8s.K8sWrapper, log logr.Logger) (time.Duration, error) {
	vpcCniConfigMap, err := k8sApi.GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace)
	if err == nil && vpcCniConfigMap.Data != nil {
		if val, ok := vpcCniConfigMap.Data[config.BranchENICooldownPeriodKey]; ok {
			coolDownPeriodInt, err := strconv.Atoi(val)
			if err != nil {
				log.Error(err, "failed to parse branch ENI coolDown period", "cool down period", val)
			} else {
				return time.Second * time.Duration(coolDownPeriodInt), nil
			}
		}
	}
	// If configmap not found, or configmap data not found, or error in parsing coolDown period, return default coolDown period and error
	return DefaultCoolDownPeriod, fmt.Errorf("failed to get cool down period:%v", err)
}

func (c *cooldown) GetCoolDownPeriod() time.Duration {
	if c.coolDownPeriod < 30*time.Second {
		return MinimalCoolDownPeriod
	}
	return c.coolDownPeriod
}

func (c *cooldown) SetCoolDownPeriod(newCoolDownPeriod time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.coolDownPeriod = newCoolDownPeriod
}
