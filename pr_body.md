## What changed
Migrated all Docker base images from Amazon Linux 2 (AL2) to Amazon Linux 2023 (AL2023):
- Updated `eks-distro-minimal-base-nonroot` image tag from `latest.2` to `latest.al2023` in Makefile and .ko.yaml
- Updated `go-runner` image tag from `latest.al2` to `latest.al2023` in Dockerfile and Makefile

## Why
AL2 is going EOL in June 2026. Migrating to AL2023 ensures continued support and security updates.

Fixes #647

## Testing
- All existing unit tests pass (`go test -race ./pkg/... ./controllers/... ./webhooks/...`)
- Verified no remaining AL2 image references in the codebase
