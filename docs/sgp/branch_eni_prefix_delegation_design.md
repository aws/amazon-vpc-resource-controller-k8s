# Design: Security Groups for Pods with Branch ENI Sharing via Prefix Delegation

## Problem Statement

Today, Security Groups for Pods (SGPP) allocates one dedicated branch ENI per pod. Each branch ENI is created, associated to the trunk ENI at a unique VLAN, used by exactly one pod, and then deleted (after cooldown) when the pod terminates. This 1:1 mapping means the maximum number of SGP pods on a node is limited by the instance type's `BranchInterface` limit (e.g., 29 for `c5.xlarge`).

This creates two key problems:
1. **Low pod density** — Large-scale workloads on SGP-enabled clusters quickly exhaust branch ENI capacity.
2. **High ENI churn** — Each pod lifecycle requires create/associate/disassociate/delete EC2 API calls, contributing to API throttling and increased pod startup latency.

## Solution Overview

Enable multiple pods to share a single branch ENI by attaching `/28` IPv4 prefixes to branch ENIs instead of individual IPs. Each prefix provides 16 usable IPs, allowing up to 16 pods to share one branch ENI (with the same security groups). The effective pod capacity becomes `BranchInterface * 16`.

This feature is gated by the `--enable-branch-eni-prefix-delegation` flag and is fully backward-compatible — existing clusters without the flag continue to use the legacy 1:1 model.

## Architecture

### High-Level Flow

```
┌──────────┐    CreateAndAnnotateResources()    ┌──────────────────┐
│  Pod     │ ──────────────────────────────────► │ branchENIProvider │
│ Admitted │                                     │ (prefixDelegation │
└──────────┘                                     │  Enabled=true)    │
                                                 └────────┬─────────┘
                                                          │
                                          AllocateIPFromSharedENI()
                                                          │
                                                          ▼
                                                 ┌────────────────┐
                                                 │   trunkENI     │
                                                 │                │
                                                 │ sgToBranchENI- │
                                                 │ Pool (per SG   │
                                                 │ combination)   │
                                                 └────────┬───────┘
                                                          │
                                    ┌─────────────────────┼──────────────────────┐
                                    │                     │                      │
                                    ▼                     ▼                      ▼
                          Has free IPs?         Expand existing ENI?    Create new branch ENI
                          (reuse existing)      (assign new /28 prefix) (with /28 prefix)
```

### Data Model

#### New Types (`prefix_pool.go`)

```go
// BranchENIWithPrefix — A shared branch ENI with one or more /28 prefixes.
// Multiple pods share the ENI by each using a unique IP from the prefix pool.
type BranchENIWithPrefix struct {
    ENIDetail      *ENIDetails
    SecurityGroups []string
    PrefixCIDRs    []string          // e.g., ["10.0.0.0/28", "10.0.0.16/28"]
    AllIPs         []string          // all IPs from all prefixes
    FreeIPs        []string          // available for allocation
    UsedIPs        map[string]string // IP → pod UID
    CoolingIPs     []CoolingIP       // freed IPs in cooldown
}

// PrefixAllocation — tracks a pod's assignment.
type PrefixAllocation struct {
    BranchENI  *BranchENIWithPrefix
    AssignedIP string
}
```

#### New State in `trunkENI`

| Field | Type | Purpose |
|-------|------|---------|
| `sgToBranchENIPool` | `map[string][]*BranchENIWithPrefix` | Pools of shared ENIs, keyed by canonical security group combination |
| `uidToPrefixAllocation` | `map[string]*PrefixAllocation` | Maps pod UID → its prefix allocation |
| `prefixDelegationEnabled` | `bool` | Feature gate |

### IP Lifecycle

```
              Allocate               Release              Cooldown expires
   FreeIPs ──────────► UsedIPs ──────────► CoolingIPs ──────────► FreeIPs
              (pod                    (pod deleted)        (configurable,
              created)                                     default 60s)
```

When all IPs in a `BranchENIWithPrefix` are in `FreeIPs` (none used, none cooling), the ENI is considered "fully drained" and is pushed to the ENI delete queue for cleanup.

## Detailed Design

### 1. Feature Flag Propagation

```
main.go (CLI flag: --enable-branch-eni-prefix-delegation)
    → condition.NewControllerConditions(..., branchENIPDFeatureFlag)
        → condition.IsBranchENIPrefixDelegationEnabled()
            → resource.NewResourceManager()
                → branch.NewBranchENIProvider(..., prefixDelegationEnabled)
                    → trunk.NewTrunkENI(..., prefixDelegationEnabled)
```

### 2. Allocation Path (`AllocateIPFromSharedENI`)

The allocation follows a three-tier strategy:

1. **Reuse existing ENI** — Find a shared ENI with matching security groups that has `FreeIPs > 0`. O(1) IP pop from the front of `FreeIPs`.

2. **Expand existing ENI** — If all matching ENIs are full but haven't reached `maxPrefixesPerENI` (= `IPv4PerInterface / 16`), assign an additional `/28` prefix via `AssignIPv4ResourcesAndWaitTillReady`. This adds 16 new IPs without consuming a new branch slot.

3. **Create new branch ENI** — If no expansion is possible, create a brand-new branch ENI with `IPv4PrefixCount: 1` on the `CreateNetworkInterface` call, associate it to the trunk, and add it to the pool.

Security group matching uses `CanonicalSGKey()` — security groups are sorted alphabetically and joined with commas, ensuring `["sg-b", "sg-a"]` and `["sg-a", "sg-b"]` map to the same pool.

### 3. Deallocation Path (`FreePrefixIP`)

When a pod is deleted:
1. `DeleteBranchUsedByPods` checks `HasPrefixAllocation(UID)`.
2. If true → `FreePrefixIP(UID)`: moves the IP from `UsedIPs` to `CoolingIPs` with a timestamp.
3. If false → falls back to legacy `PushBranchENIsToCoolDownQueue` (handles pre-existing pods that were allocated under the legacy model).

### 4. Cooldown and Cleanup (`processPrefixCoolDowns`)

Invoked at the start of every `DeleteCooledDownENIs()` cycle:
- For each `BranchENIWithPrefix`, iterate `CoolingIPs`. If `time.Now() > DeletionTimestamp + cooldownPeriod`, move IP back to `FreeIPs`.
- If the ENI becomes "fully drained" (`UsedIPs == 0 && CoolingIPs == 0`), remove from pool and push `ENIDetail` to the delete queue for eventual `DisassociateTrunkInterface` + `DeleteNetworkInterface`.

### 5. Capacity Reporting

```go
// In UpdateResourceCapacity:
capacity := vpc.Limits[instanceType].BranchInterface
if prefixDelegationEnabled && capacity != 0 {
    capacity = capacity * 16
}
```

This advertises the multiplied capacity to the Kubernetes scheduler via the extended resource `vpc.amazonaws.com/pod-eni`.

### 6. Reconciliation

The existing `Reconcile(pods)` method is extended to iterate `uidToPrefixAllocation`. Any pod UID not present in the current pod set is treated as leaked — the IP is released via `ReleaseIP` and enters cooldown.

### 7. Node Teardown (`DeleteAllBranchENIs`)

Extended to delete all shared prefix ENIs (in addition to legacy branch ENIs and the delete queue).

## Configuration

| Parameter | Source | Default | Description |
|-----------|--------|---------|-------------|
| `--enable-branch-eni-prefix-delegation` | CLI flag | `false` | Cluster-wide feature gate |
| `branch-eni-cooldown` | ConfigMap `amazon-vpc-cni` | `60s` | Cooldown before freed IPs/ENIs are recycled/deleted |

## Capacity Calculation

| Instance Type | Branch ENIs | Legacy Capacity | PD Capacity (×16) |
|---------------|-------------|-----------------|---------------------|
| c5.xlarge     | 29          | 29 pods         | 464 pods           |
| m5.2xlarge    | 29          | 29 pods         | 464 pods           |
| c5.4xlarge    | 59          | 59 pods         | 944 pods           |

## Backward Compatibility

- **Flag off (default)**: No behavioral change. The new code paths are fully gated.
- **Flag on, existing pods**: Pods created before the flag was enabled remain in `uidToBranchENIMap` (legacy). Deletion falls back to `PushBranchENIsToCoolDownQueue` when `HasPrefixAllocation` returns false.
- **Mixed mode during rollout**: Both legacy and prefix allocations coexist on the same node. `canCreateMore` / `canCreateMoreLocked` count both shared ENIs and legacy ENIs against the branch interface limit.
- **Annotation format**: The pod annotation continues to use the same `[]*ENIDetails` JSON array format. The new `PrefixCIDR` field is `omitempty` and transparent to consumers that don't use it.

## Files Changed

| File | Change |
|------|--------|
| `main.go` | New CLI flag `--enable-branch-eni-prefix-delegation` |
| `pkg/condition/conditions.go` | New `IsBranchENIPrefixDelegationEnabled()` method |
| `pkg/config/type.go` | New constant `EnableBranchENIPrefixDelegationKey` |
| `pkg/resource/manager.go` | Passes flag to `NewBranchENIProvider` |
| `pkg/provider/branch/provider.go` | Branches allocation/deallocation based on PD flag |
| `pkg/provider/branch/trunk/trunk.go` | Core prefix allocation, cooldown, reconciliation logic |
| `pkg/provider/branch/trunk/prefix_pool.go` | **New** — `BranchENIWithPrefix`, `PrefixAllocation`, IP pool operations |
| `docs/sgp/sgp_config_options.md` | User-facing documentation for the new flag |
| `mocks/*/mock_condition.go` | Generated mock for new interface method |
| `mocks/*/mock_trunk.go` | Generated mock for new `TrunkENI` methods |

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Prefix exhaustion under burst | Three-tier allocation (reuse → expand → create) minimizes API calls. Requeue with backoff when at max capacity. |
| ENI leak if controller restarts | `Reconcile()` iterates both `uidToBranchENIMap` and `uidToPrefixAllocation` against live pods; leaked IPs enter cooldown. |
| Security group drift | Pools are keyed by canonical SG set. Pods with different SG requirements get different ENIs — no cross-contamination. |
| EC2 API throttling from prefix assignment | Expanding existing ENIs is preferred over creating new ones, reducing create/delete churn. |
| Cooldown period too short for iptables propagation | Same configurable cooldown (`branch-eni-cooldown`, min 30s) applies to prefix IPs, matching legacy behavior. |

## Testing

- **Unit tests**: `prefix_pool_test.go` covers pool operations (allocate, release, cooldown, drain, canonical SG key).
- **Unit tests**: `provider_test.go` covers provider-level paths (PD allocation, capacity error, annotation failure, delete path).
- **Reconciliation tests**: Verify leaked prefix allocations are cleaned up alongside legacy ENI leaks.
- **Full lifecycle test**: Allocate → free → cooldown → re-allocate cycle validates IP recycling end-to-end.

## Future Work

- **Warm prefix pool**: Pre-allocate a configurable number of shared ENIs to reduce cold-start latency for the first pod with a given SG set.
- **Dynamic prefix scaling**: Release prefixes from under-utilized ENIs to reduce IP address consumption.
- **IPv6 prefix support**: Extend to assign `/80` IPv6 prefixes for dual-stack SGP pods.
- **Metrics**: Expose Prometheus metrics for prefix utilization (free/used/cooling per ENI).
