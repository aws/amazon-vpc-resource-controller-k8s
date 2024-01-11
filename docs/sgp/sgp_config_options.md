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