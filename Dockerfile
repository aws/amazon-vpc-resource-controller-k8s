# Build the controller binary
FROM public.ecr.aws/bitnami/golang:1.13 as builder

WORKDIR /workspace
ENV GOPROXY direct

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# Internal folder contains the aws-sdk-go with private API calls for ENI Trunking
COPY internal/ internal/
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY .git/ .git/
COPY main.go main.go
COPY apis/ apis/
COPY pkg/ pkg/
COPY controllers/ controllers/
COPY webhooks/ webhooks/

# Version package for passing the ldflags
ENV VERSION_PKG=github.com/aws/amazon-vpc-resource-controller-k8s/pkg/version
# Build
RUN GIT_VERSION=$(git describe --tags --dirty --always) && \
        GIT_COMMIT=$(git rev-parse HEAD) && \
        BUILD_DATE=$(date +%Y-%m-%dT%H:%M:%S%z) && \
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build \
        -ldflags="-X ${VERSION_PKG}.GitVersion=${GIT_VERSION} -X ${VERSION_PKG}.GitCommit=${GIT_COMMIT} -X ${VERSION_PKG}.BuildDate=${BUILD_DATE}" -a -o controller main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM public.ecr.aws/eks-distro-build-tooling/eks-distro-minimal-base:2021-08-22-1629654770

WORKDIR /
COPY --from=builder /workspace/controller .

ENTRYPOINT ["/controller"]
