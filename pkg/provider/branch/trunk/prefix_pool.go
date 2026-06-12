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

// MaxIPv6PerPrefix is the maximum number of IPv6 addresses used from each /80 prefix.
const MaxIPv6PerPrefix = 256

// BranchENIWithPrefix represents a shared branch ENI with one or more IPv4 /28 prefixes
// and/or IPv6 /80 prefixes attached, allowing multiple pods to share the same ENI.
type BranchENIWithPrefix struct {
	ENIDetail      *ENIDetails
	SecurityGroups []string

	// IPv4 pool
	PrefixCIDRs []string
	AllIPs      []string
	FreeIPs     []string
	UsedIPs     map[string]string // IP -> pod UID
	CoolingIPs  []CoolingIP

	// IPv6 pool
	IPv6PrefixCIDRs []string
	AllIPv6s        []string
	FreeIPv6s       []string
	UsedIPv6s       map[string]string // IPv6 -> pod UID
	CoolingIPv6s    []CoolingIP
}

// CoolingIP represents an IP that was freed from a pod and is in cooldown
// before being made available for reuse.
type CoolingIP struct {
	IP                string
	PodUID            string
	DeletionTimestamp time.Time
}

// PrefixAllocation tracks which shared ENI and IP(s) a pod is using.
type PrefixAllocation struct {
	BranchENI    *BranchENIWithPrefix
	AssignedIP   string // IPv4 (empty if ipv6-only)
	AssignedIPv6 string // IPv6 (empty if ipv4-only)
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

// HasFreeIPv6s returns true if this shared ENI has IPv6 addresses available.
func (b *BranchENIWithPrefix) HasFreeIPv6s() bool {
	return len(b.FreeIPv6s) > 0
}

// AllocateIPv6 removes an IPv6 from the free pool and assigns it to the given pod UID.
func (b *BranchENIWithPrefix) AllocateIPv6(podUID string) string {
	if len(b.FreeIPv6s) == 0 {
		return ""
	}
	ip := b.FreeIPv6s[0]
	b.FreeIPv6s = b.FreeIPv6s[1:]
	if b.UsedIPv6s == nil {
		b.UsedIPv6s = make(map[string]string)
	}
	b.UsedIPv6s[ip] = podUID
	return ip
}

// ReleaseIPv6 moves an IPv6 from used to the cooling queue.
func (b *BranchENIWithPrefix) ReleaseIPv6(podUID string) string {
	for ip, uid := range b.UsedIPv6s {
		if uid == podUID {
			delete(b.UsedIPv6s, ip)
			b.CoolingIPv6s = append(b.CoolingIPv6s, CoolingIP{
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
// Returns true if the ENI is fully drained (all pools are free, none used or cooling).
func (b *BranchENIWithPrefix) ProcessCoolDown(cooldownPeriod time.Duration) bool {
	now := time.Now()

	// Process IPv4 cooldowns
	var remaining []CoolingIP
	for _, coolingIP := range b.CoolingIPs {
		if now.After(coolingIP.DeletionTimestamp.Add(cooldownPeriod)) {
			b.FreeIPs = append(b.FreeIPs, coolingIP.IP)
		} else {
			remaining = append(remaining, coolingIP)
		}
	}
	b.CoolingIPs = remaining

	// Process IPv6 cooldowns
	var remainingV6 []CoolingIP
	for _, coolingIP := range b.CoolingIPv6s {
		if now.After(coolingIP.DeletionTimestamp.Add(cooldownPeriod)) {
			b.FreeIPv6s = append(b.FreeIPv6s, coolingIP.IP)
		} else {
			remainingV6 = append(remainingV6, coolingIP)
		}
	}
	b.CoolingIPv6s = remainingV6

	return b.IsFullyDrained()
}

// IsFullyDrained returns true if no IPs (IPv4 or IPv6) are in use or cooling down.
func (b *BranchENIWithPrefix) IsFullyDrained() bool {
	return len(b.UsedIPs) == 0 && len(b.CoolingIPs) == 0 &&
		len(b.UsedIPv6s) == 0 && len(b.CoolingIPv6s) == 0
}

// AddPrefix adds a new IPv4 prefix's IPs to the ENI's free pool.
func (b *BranchENIWithPrefix) AddPrefix(prefixCIDR string, ips []string) {
	b.PrefixCIDRs = append(b.PrefixCIDRs, prefixCIDR)
	b.AllIPs = append(b.AllIPs, ips...)
	b.FreeIPs = append(b.FreeIPs, ips...)
}

// AddIPv6Prefix adds a new IPv6 prefix's addresses to the ENI's free IPv6 pool.
func (b *BranchENIWithPrefix) AddIPv6Prefix(prefixCIDR string, ips []string) {
	b.IPv6PrefixCIDRs = append(b.IPv6PrefixCIDRs, prefixCIDR)
	b.AllIPv6s = append(b.AllIPv6s, ips...)
	b.FreeIPv6s = append(b.FreeIPv6s, ips...)
}

// PrefixCount returns the number of IPv4 prefixes assigned to this ENI.
func (b *BranchENIWithPrefix) PrefixCount() int {
	return len(b.PrefixCIDRs)
}

// IPv6PrefixCount returns the number of IPv6 prefixes assigned to this ENI.
func (b *BranchENIWithPrefix) IPv6PrefixCount() int {
	return len(b.IPv6PrefixCIDRs)
}
