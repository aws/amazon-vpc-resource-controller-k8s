# Configuration options with Prefix Delegation mode on Windows

We provide multiple configurations which allow you to fine tune the pre-scaling and dynamic scaling behaviour. These configuration options can be set in the `amazon-vpc-cni` config map.

* **warm-ip-target** &rarr; The number of IP addresses to be allocated in excess of current need. When used with prefix delegation, the controller allocates a new prefix to the ENI if the number of free IP addresses from the existing prefixes is less than this value on the node.

   For example, consider that we set warm-ip-target to 15. Initially when the node starts, the ENI has 1 prefix i.e. 16 IP addresses allocated to it. When we launch 2 pods, then the number of available IP addresses becomes 14 and therefore, a new prefix will be allocated to the ENI, which brings the total count of available IP addresses to 30.


* **warm-prefix-target** &rarr; The number of prefixes to be allocated in excess of current need. This will cause a new prefix (/28) to be allocated even if a single IP from the existing prefix is used. Therefore, use this configuration only if needed after careful consideration. A good use case is a scenario where there can be sudden spikes leading to scheduling of substantially high number of pods.

   For example, consider that we set warm-prefix-target to 2. Initially when the node starts, 2 prefixes will be allocated to the ENI. Since there won’t be any running pods, the current need would be 0 and therefore, both the prefixes would be unused. If we run even a single pod then the current need would be 1 IP address which would come from 1 prefix. Therefore, only 1 prefix would be in excess of the current need. This would lead to 1 more prefix being allocated to the ENI bringing the total count of prefixes on the ENI to 3.


* **minimum-ip-target** &rarr; The minimum number of IP addresses to be available at any time. This behaves identically to warm-ip-target except that instead of setting a target number of free IP addresses to keep available at all times, it sets a target number for a floor on how many total IP addresses are allocated.

   For example, consider that we set minimum-ip-target to 20. This means that the total number of IP addresses (free and allocated to pods) should be at least 20. Therefore, even before the pods are scheduled, there should be at least 20 IP addresses available. Since 1 prefix has 16 IP addresses, the controller would allocate 2 prefixes bringing the total count of available IP address on the node to 32 which is greater than the set value of 20.

### Considerations while using the above configuration options
- These configuration options work only with the prefix delegation mode.
- The settings for these values would depend upon your use case. If set, `warm-ip-target` and/or `minimum-ip-target` will take precedence over `warm-prefix-target`.
- The default values used by the controller when these configurations have not been explicitly specified are-
  ```
  warm-ip-target: "1"
  minimum-ip-target: "3"
  ```
- Setting either `warm-prefix-target` or both `warm-ip-target` and `minimum-ip-target` to zero/negative values is not supported. In such cases, default values as above will be used by the controller.
- If only `minimum-ip-target` is set, `warm-ip-target` defaults to 1. If only `warm-ip-target` is set, `minimum-ip-target` defaults to 3.
- If the values of `warm-prefix-target`, `warm-ip-target` or `minimum-ip-target` are set such that the max node IPv4 capacity is exceeded, then the maximum allocated IP addresses would be limited to the max node IPv4 capacity. For example, if a node has 14 secondary IP slots and we set `warm-prefix-target` to 20, then only 14 prefixes will be allocated on node startup.

### Examples
The following table shows the various parameters when the configuration options for prefix delegation are set. You can fine-tune these values as per your expected workload.

|warm-prefix-target|warm-ip-target|minimum-ip-target|Pods|Attached Prefixes|Pod per Prefixes|Unused Prefixes|Unused IPs|
|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
|-|-|-|0|1|0|1|16|
|-|-|-|15|1|15|0|1|
|-|-|-|16|2|16,0|1|16|
|1|-|-|0|1|0|1|16|
|3|-|-|0|3|0|3|48|
|1|-|-|5|2|5|1|27|
|1|-|-|17|3|16,1|1|31|
|-|-|1|0|1|0|1|16|
|-|-|1|5|1|5|0|11|
|-|-|1|15|1|15|0|1|
|-|-|1|16|2|16,0|1|16|
|-|5|-|0|1|0|1|16|
|-|5|-|5|1|5|0|11|
|-|5|-|11|1|11|0|5|
|-|5|-|12|2|12,0|1|20|
|-|1|1|0|1|0|1|16|
|-|1|1|5|1|5|0|11|
|-|1|1|17|2|16,1|0|15|
|-|3|1|14|2|14,0|1|18|
|-|7|20|0|2|0,0|2|32|
|-|7|20|5|2|5,0|1|27|
|-|7|20|17|2|16,1|0|15|
|-|7|20|26|3|16,10,0|1|22|
