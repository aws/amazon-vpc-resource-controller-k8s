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
	"sort"
	"strings"
	"time"
)

// BranchENIWithPrefix represents a shared branch ENI with one or more /28 prefixes attached,
// allowing multiple pods to share the same ENI by using individual IPs from the prefixes.
type BranchENIWithPrefix struct {
	ENIDetail      *ENIDetails
	SecurityGroups []string
	PrefixCIDRs    []string
	AllIPs         []string
	FreeIPs        []string
	UsedIPs        map[string]string // IP -> pod UID
	CoolingIPs     []CoolingIP
}

// CoolingIP represents an IP that was freed from a pod and is in cooldown
// before being made available for reuse.
type CoolingIP struct {
	IP                string
	PodUID            string
	DeletionTimestamp time.Time
}

// PrefixAllocation tracks which shared ENI and IP a pod is using.
type PrefixAllocation struct {
	BranchENI  *BranchENIWithPrefix
	AssignedIP string
}

// CanonicalSGKey returns a canonical string key for a set of security groups.
// The groups are sorted to ensure consistent lookup regardless of input order.
func CanonicalSGKey(securityGroups []string) string {
	sorted := make([]string, len(securityGroups))
	copy(sorted, securityGroups)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// HasFreeIPs returns true if this shared ENI has IPs available for allocation.
func (b *BranchENIWithPrefix) HasFreeIPs() bool {
	return len(b.FreeIPs) > 0
}

// AllocateIP removes an IP from the free pool and assigns it to the given pod UID.
// Returns the allocated IP or empty string if none available.
func (b *BranchENIWithPrefix) AllocateIP(podUID string) string {
	if len(b.FreeIPs) == 0 {
		return ""
	}
	ip := b.FreeIPs[0]
	b.FreeIPs = b.FreeIPs[1:]
	b.UsedIPs[ip] = podUID
	return ip
}

// ReleaseIP moves an IP from used to the cooling queue.
func (b *BranchENIWithPrefix) ReleaseIP(podUID string) string {
	for ip, uid := range b.UsedIPs {
		if uid == podUID {
			delete(b.UsedIPs, ip)
			b.CoolingIPs = append(b.CoolingIPs, CoolingIP{
				IP:                ip,
				PodUID:            podUID,
				DeletionTimestamp: time.Now(),
			})
			return ip
		}
	}
	return ""
}

// ProcessCoolDown moves IPs whose cooldown has expired back to the free pool.
// Returns true if the ENI is fully drained (all IPs are free, none used or cooling).
func (b *BranchENIWithPrefix) ProcessCoolDown(cooldownPeriod time.Duration) bool {
	now := time.Now()
	var remaining []CoolingIP
	for _, coolingIP := range b.CoolingIPs {
		if now.After(coolingIP.DeletionTimestamp.Add(cooldownPeriod)) {
			b.FreeIPs = append(b.FreeIPs, coolingIP.IP)
		} else {
			remaining = append(remaining, coolingIP)
		}
	}
	b.CoolingIPs = remaining
	return len(b.UsedIPs) == 0 && len(b.CoolingIPs) == 0
}

// IsFullyDrained returns true if no IPs are in use or cooling down.
func (b *BranchENIWithPrefix) IsFullyDrained() bool {
	return len(b.UsedIPs) == 0 && len(b.CoolingIPs) == 0
}

// AddPrefix adds a new prefix's IPs to the ENI's free pool.
func (b *BranchENIWithPrefix) AddPrefix(prefixCIDR string, ips []string) {
	b.PrefixCIDRs = append(b.PrefixCIDRs, prefixCIDR)
	b.AllIPs = append(b.AllIPs, ips...)
	b.FreeIPs = append(b.FreeIPs, ips...)
}

// PrefixCount returns the number of prefixes assigned to this ENI.
func (b *BranchENIWithPrefix) PrefixCount() int {
	return len(b.PrefixCIDRs)
}
