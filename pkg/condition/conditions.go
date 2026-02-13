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
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type condition struct {
	hasDataStoreSynced   bool
	log                  logr.Logger
	K8sAPI               k8s.K8sWrapper
	lock                 sync.Mutex
	windowsPDFeatureFlag bool
}

const CheckDataStoreSyncedInterval = time.Second * 10

type Conditions interface {
	// IsWindowsIPAMEnabled to process events only when Windows IPAM is enabled
	// by the user
	IsWindowsIPAMEnabled() bool

	// IsWindowsPrefixDelegationEnabled to process events only when Windows Prefix Delegation is enabled
	IsWindowsPrefixDelegationEnabled() bool

	// IsPodENIDualStackEnabled checks if dual-stack (IPv4+IPv6) is enabled for Pod ENIs
	IsPodENIDualStackEnabled() bool

	// IsOldVPCControllerDeploymentPresent returns true if the old controller deployment
	// is still present on the cluster
	IsOldVPCControllerDeploymentPresent() bool

	// GetPodDataStoreSyncStatus is used to get Pod Datastore sync status
	GetPodDataStoreSyncStatus() bool

	// SetPodDataStoreSyncStatus is used to set Pod Datastore sync status
	SetPodDataStoreSyncStatus(synced bool)
}

var (
	conditionWindowsIPAMEnabled = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "windows_ipam_enabled",
			Help: "Binary value to indicate whether user has set enable-windows-ipam to true",
		})

	conditionWindowsPrefixDelegationEnabled = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "windows_prefix_delegation_enabled",
			Help: "Binary value to indicate whether user has set enable-windows-prefix-delegation to true",
		})

	prometheusRegistered = false
)

func prometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(
			conditionWindowsIPAMEnabled,
			conditionWindowsPrefixDelegationEnabled,
		)
	}

	prometheusRegistered = true
}

func NewControllerConditions(log logr.Logger, k8sApi k8s.K8sWrapper, windowsPDFeatureFlag bool) Conditions {
	prometheusRegister()
	conditionWindowsIPAMEnabled.Set(0)
	conditionWindowsPrefixDelegationEnabled.Set(0)

	return &condition{
		log:                  log,
		K8sAPI:               k8sApi,
		windowsPDFeatureFlag: windowsPDFeatureFlag,
	}
}

func (c *condition) IsWindowsIPAMEnabled() bool {
	if c.IsOldVPCControllerDeploymentPresent() {
		return false
	}

	// Return false if configmap not present/any errors
	vpcCniConfigMap, err := c.K8sAPI.GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace)

	if err == nil && vpcCniConfigMap.Data != nil {
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

func (c *condition) IsWindowsPrefixDelegationEnabled() bool {
	if c.IsOldVPCControllerDeploymentPresent() {
		return false
	}

	// If the feature flag is OFF, Windows PD cannot be enabled via config map
	if !c.windowsPDFeatureFlag {
		return false
	}

	// Return false if configmap not present/any errors
	vpcCniConfigMap, err := c.K8sAPI.GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace)

	if err == nil && vpcCniConfigMap.Data != nil {
		if ipamVal, ok := vpcCniConfigMap.Data[config.EnableWindowsIPAMKey]; ok {
			// Check if Windows IPAM is enabled
			enableWinIpamVal, err := strconv.ParseBool(ipamVal)
			if err == nil && enableWinIpamVal {
				if pdVal, ok := vpcCniConfigMap.Data[config.EnableWindowsPrefixDelegationKey]; ok {
					enableWinPDVal, err := strconv.ParseBool(pdVal)
					// Check if Windows PD is enabled
					if err == nil && enableWinPDVal {
						conditionWindowsPrefixDelegationEnabled.Set(1)
						return true
					}
				}
			}
		}
	}

	conditionWindowsPrefixDelegationEnabled.Set(0)
	return false
}

// Watch for deployments of old VPC Resource controller, new controller will block
// till the user deletes the old controller deployment. Ideally we should block till
// old WebHook is deleted too. But the Field selectors don't support IN operator for
// selecting object names and the controller deployment doesn't have labels which removes
// possibility of using label selectors to watch for both deployments. However, Old and
// new WebHook running together should not have side effects on the existing Pods.
func (c *condition) IsOldVPCControllerDeploymentPresent() bool {
	oldController, err := c.K8sAPI.GetDeployment(config.KubeSystemNamespace,
		config.OldVPCControllerDeploymentName)
	if err == nil {
		c.log.V(1).Info("old controller deployment is present",
			"deployment", *oldController)
		return true
	} else if err != nil && !errors.IsNotFound(err) {
		c.log.Error(err, "received unexpected error while checking for old controller deployment")
		return true
	}
	return false
}

// TOOD: Check if SPG is enabled using ConfigMap.
func (c *condition) IsPodSGPEnabled() bool {
	daemonSet, err := c.K8sAPI.GetDaemonSet(config.VpcCNIDaemonSetName,
		config.KubeSystemNamespace)
	if err != nil {
		if errors.IsNotFound(err) {
			c.log.Info("aws-node ds is not present")
			return false
		}
		c.log.Error(err, "failed to get aws-node")
		return false
	}

	var isSGPEnabled bool
	for _, container := range daemonSet.Spec.Template.Spec.Containers {
		if container.Name == "aws-node" {
			for _, env := range container.Env {
				if env.Name == "ENABLE_POD_ENI" {
					isSGPEnabled, err = strconv.ParseBool(env.Value)
					if err != nil {
						c.log.Error(err, "failed to parse ENABLE_POD_ENI", "value", env.Value)
						return false
					}
					break
				}
			}
		}
	}
	return isSGPEnabled
}

// IsPodENIDualStackEnabled checks if dual-stack (IPv4+IPv6) is enabled for Pod ENIs
// by reading the ENABLE_POD_ENI_DUAL_STACK environment variable from the aws-node daemonset.
func (c *condition) IsPodENIDualStackEnabled() bool {
	daemonSet, err := c.K8sAPI.GetDaemonSet(config.VpcCNIDaemonSetName,
		config.KubeSystemNamespace)
	if err != nil {
		if errors.IsNotFound(err) {
			c.log.Info("aws-node ds is not present")
			return false
		}
		c.log.Error(err, "failed to get aws-node")
		return false
	}

	var isDualStackEnabled bool
	for _, container := range daemonSet.Spec.Template.Spec.Containers {
		if container.Name == "aws-node" {
			for _, env := range container.Env {
				if env.Name == "ENABLE_POD_ENI_DUAL_STACK" {
					isDualStackEnabled, err = strconv.ParseBool(env.Value)
					if err != nil {
						c.log.Error(err, "failed to parse ENABLE_POD_ENI_DUAL_STACK", "value", env.Value)
						return false
					}
					break
				}
			}
		}
	}
	return isDualStackEnabled
}

func (c *condition) GetPodDataStoreSyncStatus() bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.hasDataStoreSynced
}

func (c *condition) SetPodDataStoreSyncStatus(synced bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.hasDataStoreSynced = synced
}
