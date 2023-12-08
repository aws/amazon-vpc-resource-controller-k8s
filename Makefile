# Image URL to use all building/pushing image targets
AWS_ACCOUNT ?= ${AWS_ACCOUNT_ID}
AWS_REGION ?= ${AWS_DEFAULT_REGION}
CLUSTER_NAME ?= $(shell kubectl config view --minify -o jsonpath='{.clusters[].name}' | rev | cut -d"/" -f1 | rev | cut -d"." -f1)
KUBE_CONFIG_PATH ?= ${HOME}/.kube/config
REPO=$(AWS_ACCOUNT_ID).dkr.ecr.${AWS_REGION}.amazonaws.com/aws/amazon-vpc-resource-controller-k8s
KO_DOCKER_REPO ?= ${REPO} # Used for development images

GIT_VERSION=$(shell git describe --tags --always)
MAKEFILE_PATH = $(dir $(realpath -s $(firstword $(MAKEFILE_LIST))))

VERSION ?= $(GIT_VERSION)
IMAGE ?= $(REPO):$(VERSION)
BASE_IMAGE ?= public.ecr.aws/eks-distro-build-tooling/eks-distro-minimal-base-nonroot:latest.2
BUILD_IMAGE ?= public.ecr.aws/bitnami/golang:1.21.5
GOARCH ?= amd64
PLATFORM ?= linux/amd64

help: ## Display help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

## Execute before submitting code
presubmit: verify test

## Verify dependencies, correctness, and formatting
verify:
	go mod tidy
	go generate ./...
	go vet ./...
	go fmt ./...
	controller-gen crd:trivialVersions=true rbac:roleName=controller-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	controller-gen object:headerFile="scripts/templates/boilerplate.go.txt" paths="./..."
	@git diff --quiet ||\
	{ echo "New file modification detected in the Git working tree. Please check in before commit."; git --no-pager diff --name-only | uniq | awk '{print "  - " $$0}'; \
	if [ "${CI}" = true ]; then\
		exit 1;\
	fi;}

## Run unit tests
test: verify
	go test ./pkg/... ./controllers/... ./webhooks/... -coverprofile cover.out

test-e2e:
	KUBE_CONFIG_PATH=${KUBE_CONFIG_PATH} REGION=${AWS_REGION} CLUSTER_NAME=${CLUSTER_NAME} ./scripts/test/run-integration-tests.sh

image: ## Build the images using ko build
	$(eval IMAGE=$(shell KO_DOCKER_REPO=$(KO_DOCKER_REPO) $(WITH_GOFLAGS) ko build --bare github.com/aws/amazon-vpc-resource-controller-k8s))

toolchain: ## Install developer toolchain
	./hack/toolchain.sh

apply: image check-deployment-env check-env ## Deploy controller to ~/.kube/config
	eksctl create iamserviceaccount vpc-resource-controller --namespace kube-system --cluster ${CLUSTER_NAME} \
		--role-name VPCResourceControllerRole \
		--attach-policy-arn=arn:aws:iam::aws:policy/AdministratorAccess \
		--override-existing-serviceaccounts \
		--approve
	kustomize build config/crd | kubectl apply -f -
	cd config/controller && kustomize edit set image controller=${IMAGE}
	kustomize build config/default | sed "s|CLUSTER_NAME|${CLUSTER_NAME}|g;s|USER_ROLE_ARN|${USER_ROLE_ARN}|g" | kubectl apply -f -
	kubectl patch rolebinding eks-vpc-resource-controller-rolebinding -n kube-system --patch '{"subjects":[{"kind":"ServiceAccount","name":"vpc-resource-controller","namespace":"kube-system"}]}'
	kubectl patch clusterrolebinding vpc-resource-controller-rolebinding --patch '{"subjects":[{"kind":"ServiceAccount","name":"vpc-resource-controller","namespace":"kube-system"}]}'

delete: ## Delete controller from ~/.kube/config
	kustomize build config/default | kubectl delete --ignore-not-found -f -
	eksctl delete iamserviceaccount vpc-resource-controller --namespace kube-system --cluster ${CLUSTER_NAME}
	kubectl patch rolebinding eks-vpc-resource-controller-rolebinding -n kube-system --patch '{"subjects":[{"kind":"ServiceAccount","name":"eks-vpc-resource-controller","namespace":"kube-system"},{"apiGroup":"rbac.authorization.k8s.io","kind":"User","name":"eks:vpc-resource-controller"}]}'
	kubectl create clusterrolebinding vpc-resource-controller-rolebinding --clusterrole vpc-resource-controller-role --serviceaccount kube-system:eks-vpc-resource-controller --user eks:vpc-resource-controller

# Build the docker image with buildx
docker-buildx: check-env test
	docker buildx build --platform=$(PLATFORM) -t $(IMAGE)-$(GOARCH) --build-arg BASE_IMAGE=$(BASE_IMAGE) --build-arg BUILD_IMAGE=$(BUILD_IMAGE) --build-arg $(GOARCH) --load .

# Build the docker image
docker-build: check-env test
	docker build --build-arg BASE_IMAGE=$(BASE_IMAGE) --build-arg ARCH=$(GOARCH) --build-arg BUILD_IMAGE=$(BUILD_IMAGE) . -t ${IMAGE}

# Push the docker image
docker-push: check-env
	docker push ${IMAGE}

check-env:
	@:$(call check_var, AWS_ACCOUNT, AWS account ID for publishing docker images)
	@:$(call check_var, AWS_REGION, AWS region for publishing docker images)

check-deployment-env:
	@:$(call check_var, CLUSTER_NAME, Cluster name where the controller is deployed)

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

apply-dependencies:
	bash ${MAKEFILE_PATH}/scripts/test/install-cert-manager.sh
	kubectl set env daemonset aws-node -n kube-system ENABLE_POD_ENI=true
