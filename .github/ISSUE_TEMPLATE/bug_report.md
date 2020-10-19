---
name: Bug report
about: Create a report to help us improve
title: ''
labels: bug
assignees: ''

---

<!--
For urgent operational issues, please contact AWS Support directly at https://aws.amazon.com/premiumsupport/

If you think you have found a potential security issue, please do not post it as an issue. Instead, follow the instructions at https://aws.amazon.com/security/vulnerability-reporting/ or email AWS Security directly at aws-security@amazon.com
-->

**Describe the Bug**:
<!--
Include following details for Security Group for Pod if possible
- [CNI Plugin Logs](https://docs.aws.amazon.com/eks/latest/userguide/troubleshooting.html#troubleshoot-cni)
- Description of the Pod including events (use `kubectl describe pod -n <pod-namespace> <pod-name>`)
- Security Group Policy Policy label selectors (use `kubectl get sgp <sgp-name> -n <sgp-namespace> -o json | jq 'del(.items[].spec.securityGroups)'`) [Relies on jq - https://stedolan.github.io/jq/download/]
 -->

**Observed Behavior**:

**Expected Behavior**:

**How to reproduce it (as minimally and precisely as possible)**:

**Additional Context**:

**Environment**:
- Kubernetes version (use `kubectl version`):
- CNI Version
- OS (Linux/Windows):
