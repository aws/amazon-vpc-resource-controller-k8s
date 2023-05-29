ARG BASE_IMAGE
ARG BUILD_IMAGE
ARG ARCH=amd64
# Build the controller binary
FROM $BUILD_IMAGE as builder

WORKDIR /workspace
ENV GOPROXY direct

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
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
RUN GIT_VERSION=$(git describe --tags --always) && \
        GIT_COMMIT=$(git rev-parse HEAD) && \
        BUILD_DATE=$(date +%Y-%m-%dT%H:%M:%S%z) && \
        CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} GO111MODULE=on go build \
        -ldflags="-X ${VERSION_PKG}.GitVersion=${GIT_VERSION} -X ${VERSION_PKG}.GitCommit=${GIT_COMMIT} -X ${VERSION_PKG}.BuildDate=${BUILD_DATE}" -a -o controller main.go

FROM $BASE_IMAGE

WORKDIR /
COPY --from=public.ecr.aws/eks-distro/kubernetes/go-runner:v0.9.0-eks-1-21-4 /usr/local/bin/go-runner /usr/local/bin/go-runner
COPY --from=builder /workspace/controller .

ENTRYPOINT ["/controller"]
