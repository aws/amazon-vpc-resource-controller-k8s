# Configuration options for Security groups for pods

Users are able to configure the controller functionality related to security group for pods by updating the `data` fields in EKS-managed configmap `amazon-vpc-cni`.

* **branch-eni-cooldown**: Cooldown period for the branch ENIs, the period of time to wait before deleting the branch ENI for propagation of iptables rules for the deleted pod. The default cooldown period is 60s, and the minimum value for the cool period is 30s. If user updates configmap to a lower value than 30s, this will be overridden and set to 30s.

Add `branch-eni-cooldown` field in the configmap to set the cooldown period, example:
```
apiVersion: v1
data:
  branch-eni-cooldown: "60"
kind: ConfigMap
metadata:
  name: amazon-vpc-cni
  namespace: kube-system
```

After changing the value of `branch-eni-cooldown`, you can verify if the change has been applied by the controller. You need describe any node in your cluster and check node events in Events list. Note: this value is applied to the cluster instead of only certain nodes.

For example, after setting the value to `90`, the change will be reflected immediately in node events:
```
Events:
  Type    Reason                          Age   From                     Message
  ----    ------                          ----  ----                     -------
  Normal  BranchENICoolDownPeriodUpdated  18s   vpc-resource-controller  Branch ENI cool down period has been updated to 1m30s
```