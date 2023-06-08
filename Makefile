# Image URL to use all building/pushing image targets
IMAGE_NAME=aws/amazon-vpc-resource-controller-k8s
AWS_ACCOUNT_ID ?= $(AWS_ACCOUNT) # Left in for backwards compatibility
AWS_DEFAULT_REGION ?= $(AWS_REGION) # Left in for backwards compatibility
REPO=$(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_DEFAULT_REGION).amazonaws.com/$(IMAGE_NAME)
GIT_VERSION=$(shell git describe --tags --always)
MAKEFILE_PATH = $(dir $(realpath -s $(firstword $(MAKEFILE_LIST))))

export GOPROXY = direct

VERSION ?= $(GIT_VERSION)
IMAGE ?= $(REPO):$(VERSION)
BASE_IMAGE ?= public.ecr.aws/eks-distro-build-tooling/eks-distro-minimal-base-nonroot:latest.2
BUILD_IMAGE ?= public.ecr.aws/bitnami/golang:1.20.5
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"
GOARCH ?= amd64
PLATFORM ?= linux/amd64


# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: controller

# Run tests
test: generate fmt vet manifests
	go test ./pkg/... ./controllers/... ./webhooks/... -coverprofile cover.out

# Build controller binary
controller: generate fmt vet
	go build -o bin/controller main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
install: manifests
	kustomize build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests
	kustomize build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: check-deployment-env check-env manifests
	cd config/controller && kustomize edit set image controller=${IMAGE}
	kustomize build config/default | sed "s|CLUSTER_NAME|${CLUSTER_NAME}|g;s|USER_ROLE_ARN|${USER_ROLE_ARN}|g" | kubectl apply -f -

undeploy: check-env
	cd config/controller && kustomize edit set image controller=${IMAGE}
	kustomize build config/default | kubectl delete -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=controller-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="scripts/templates/boilerplate.go.txt" paths="./..."

# Build the docker image with buildx
docker-buildx: check-env test
	docker buildx build --platform=$(PLATFORM) -t $(IMAGE)-$(GOARCH) --build-arg BASE_IMAGE=$(BASE_IMAGE) --build-arg BUILD_IMAGE=$(BUILD_IMAGE) --build-arg $(GOARCH) --load .

image:
	KO_DOCKER_REPO=${REPO} ko build --bare main.go

# Build the docker image
docker-build: check-env test
	docker build --build-arg BASE_IMAGE=$(BASE_IMAGE) --build-arg BUILD_IMAGE=$(BUILD_IMAGE) . -t ${IMAGE}

# Push the docker image
docker-push: check-env
	docker push ${IMAGE}

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(findstring v0.6.2,$(shell controller-gen --version)))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.6.2 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

# If more than 1 files need formatting then error out
check-format:
	@exit $(shell gofmt -l . | grep -v internal | wc -l)

check-env:
	@:$(call check_var, AWS_ACCOUNT, AWS account ID for publishing docker images)
	@:$(call check_var, AWS_REGION, AWS region for publishing docker images)

check-deployment-env:
	@:$(call check_var, CLUSTER_NAME, Cluster name where the controller is deployed)
	@:$(call check_var, USER_ROLE_ARN, User Role ARN which is assumed to manage Trunk/Branch ENI for users)

check_var = \
    $(strip $(foreach 1,$1, \
        $(call __check_var,$1,$(strip $(value 2)))))
__check_var = \
    $(if $(value $1),, \
      $(error Undefined variable $1$(if $2, ($2))))

build-test-binaries:
	mkdir -p ${MAKEFILE_PATH}build
	find ${MAKEFILE_PATH} -name '*suite_test.go' -type f  | xargs dirname  | xargs ginkgo build
	find ${MAKEFILE_PATH} -name "*.test" -print0 | xargs -0 -I {} mv {} ${MAKEFILE_PATH}build
