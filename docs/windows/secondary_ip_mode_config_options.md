# Configuration options when using secondary IP addresses Windows

We provide multiple configuration options that allow you to fine-tune the IP address allocation behavior on Windows
nodes using the secondary IP address mode. These configuration options can be set in the `amazon-vpc-cni` ConfigMap in
the `kube-system` namespace.

- `windows-warm-ip-target` → The total number of IP addresses that should be allocated to each Windows node in excess of
  the current need at any given time. The excess IPs can be used by newly launched pods, which aids in faster pod
  startup times since there is no wait time for additional IP addresses to be allocated. The VPC Resource Controller
  will attempt to ensure that this excess desired threshold is always met.

  Defaults to 3 if unspecified or invalid. Must be greater than or equal to 1.

  For example, if no pods were running on a given Windows node, and if you set `windows-warm-ip-target` to 5, the VPC
  Resource Controller will aim to ensure that each Windows node always has at least 5 IP addresses in excess, ready for
  use, allocated to its ENI. If 2 pods are scheduled on the node, the controller will allocate 2 additional IP addresses
  to the ENI, maintaining the 5 warm IP address target.

- `windows-minimum-ip-target` → Defaults to 3 if unspecified or invalid. The minimum number of IP addresses, both in use
  by running pods and available as warm IPs, that should be allocated to each Windows node at any given time. The
  controller will attempt to ensure that this minimum threshold is always met.

  Defaults to 3 if unspecified or invalid. Must be greater than or equal to 0.

  For example, if no pods were running on a given Windows node, and if you set `windows-minimum-ip-target` to 10, the
  VPC Resource Controller will aim to ensure that the total number of IP addresses on the Windows node should be at
  least 10. Therefore, before pods are scheduled, there should be at least 10 IP addresses available. If 5 pods are
  scheduled on a given node, they will consume 5 of the 10 available IPs. The VPC Resource Controller will keep 5 the
  remaining available IPs available in addition to the 5 already in use to meet the target of 10.

### Considerations while using the above configuration options

- These configuration options only apply when the VPC Resource Controller is operating in the secondary IP mode. They do
  not affect the prefix delegation mode. More explicitly, if `enable-windows-prefix-delegation` is set to false, or is
  not specified, then the VPC Resource Controller operates in secondary IP mode.
- Setting either `windows-warm-ip-target` or `windows-minimum-ip-target` to a negative value will result in the
  respective default value being used.
- If the values of `windows-warm-ip-target` or `windows-minimum-ip-target` are set such that the maximum node IP
  capacity would be exceeded, the controller will limit the allocation to the maximum capacity possible.
- The `warm-prefix-target` configuration option will be ignored when using the secondary IP mode, as it only applies to
  the prefix delegation mode.
- If `windows-warm-ip-target` is set to 0, the system will implicitly set `windows-warm-ip-target` to 1. This is
  because on-demand IP allocation whereby an IP is allocated on the Windows node as the pods are scheduled is currently
  not supported. Implicitly Setting `windows-warm-ip-target` to 1 ensures the minimum acceptable non-zero value is set
  since the `windows-warm-ip-target` should always be at least 1.
- The configuration options `warm-ip-target` and `minimum-ip-target` are deprecated in favor of the new
  options `windows-warm-ip-target` and `windows-minimum-ip-target`.

### Examples

| `windows-warm-ip-target` | `windows-minimum-ip-target` | Running Pods | Total Allocated IPs | Warm IPs |
|--------------------------|-----------------------------|--------------|---------------------|----------|
| 1                        | 0                           | 0            | 1                   | 1        |
| 1                        | 0                           | 5            | 6                   | 1        |
| 5                        | 0                           | 0            | 5                   | 5        |
| 1                        | 1                           | 0            | 1                   | 1        |
| 1                        | 1                           | 1            | 2                   | 1        |
| 1                        | 3                           | 3            | 4                   | 1        |
| 1                        | 3                           | 5            | 6                   | 1        |
| 5                        | 10                          | 0            | 10                  | 10       |
| 10                       | 10                          | 0            | 10                  | 10       |
| 10                       | 10                          | 10           | 20                  | 10       |
| 15                       | 10                          | 10           | 25                  | 15       |
