# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

A Kubernetes controller (built with controller-runtime/kubebuilder) that runs on the EKS control plane. It manages:
1. **Branch/Trunk ENIs** for Security Groups for Pods (SGP)
2. **IPv4 address management (IPAM)** for Windows nodes (secondary IPs and prefix delegation)

It watches Pods, Nodes, ConfigMaps, and Deployments, and exposes mutating/validating webhooks.

## Build & Test Commands

```bash
make toolchain          # Install dev dependencies (controller-gen, ko, setup-envtest)
make verify             # go mod tidy, go generate, go vet, go fmt, controller-gen CRD/RBAC/webhook/object generation
make test               # Unit tests with race detector (runs verify first)
make presubmit          # verify + test (run before submitting PRs)

# Run a single unit test:
go test -race -run TestFunctionName ./pkg/path/to/package/...

# Integration tests (require a live EKS cluster):
make apply-dependencies # Install cert-manager, enable pod ENI on aws-node
make apply              # Deploy controller to cluster
make test-e2e           # Run integration suite
```

## Architecture

### Controllers (`controllers/`)
- **PodReconciler** (`core/pod_controller.go`) — Must start first; gates other controllers until cache syncs. Manages resource allocation/deallocation for pods.
- **NodeReconciler** (`core/node_controller.go`) — Handles node lifecycle (init/deinit resources).
- **ConfigMapReconciler** (`core/configmap_controller.go`) — Watches `amazon-vpc-cni` ConfigMap for feature flags (Windows IPAM, prefix delegation, warm pool targets).
- **DeploymentReconciler** (`apps/deployment_controller.go`) — Watches old VPC controller deployment for migration.
- **CNINodeReconciler** (`crds/cninode_controller.go`) — Manages CNINode CRD lifecycle and resource cleanup on node termination.

### Resource Providers (`pkg/provider/`)
Each implements `ResourceProvider` interface (`pkg/provider/provider.go`):
- **branch** — Branch ENI provider for SGP. Manages trunk ENI initialization and branch ENI creation/deletion with VLAN tagging.
- **ip** — Secondary IPv4 address provider for Windows nodes.
- **prefix** — IPv4 prefix delegation provider for Windows nodes.

### Key Packages
- `pkg/node/manager/` — Node manager maintains in-memory state of all nodes, dispatches async operations via worker pools.
- `pkg/pool/` — Warm pool implementation for pre-provisioning resources.
- `pkg/handler/` — On-demand and warm-pool resource handlers that decide how to fulfill pod resource requests.
- `pkg/resource/` — ResourceManager registry that maps resource names to their providers and handlers.
- `pkg/aws/ec2/api/` — EC2 API wrapper with rate limiting (separate QPS for user-context and instance-context calls).
- `pkg/condition/` — Controller conditions that gate features (e.g., Windows IPAM only enabled when old controller is absent).
- `pkg/k8s/` — Kubernetes API helpers and custom pod data store with node-name indexer.
- `pkg/config/` — Constants (resource names, labels, tags, ConfigMap keys) and runtime config loader.

### Webhooks (`webhooks/`)
- **Pod mutation** (`core/pod_webhook.go`) — Injects ENI annotation/limits for SGP-matched pods.
- **Node validation** (`core/node_update_webhook.go`) — Validates node label updates.
- **Annotation validation** (`core/annotation_validation_webhook.go`) — Validates pod annotations.

### CRD APIs (`apis/vpcresources/`)
- `v1beta1` — SecurityGroupPolicy
- `v1alpha1` — CNINode

### Mocks (`mocks/`)
Generated with `golang/mock` (gomock). Unit tests use `gomock` + `testify/assert`. Integration tests use Ginkgo/Gomega.

## Code Generation

`make verify` runs all generation. Key outputs:
- CRD manifests → `config/crd/bases/`
- RBAC manifests → `config/rbac/`
- Webhook manifests → `config/webhook/`
- DeepCopy methods → `zz_generated.deepcopy.go` files

After modifying API types or adding `+kubebuilder` markers, run `make verify` and commit generated files.

## Testing Patterns

- Unit tests: standard `go test` with gomock for interface mocking, testify for assertions.
- Integration tests (`test/integration/`): Ginkgo suites requiring a live cluster. Organized by feature (perpodsg, windows, webhook, cninode, metrics, scale).
- The test framework (`test/framework/`) provides helpers for creating K8s resources and AWS resource managers.
