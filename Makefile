IMG ?= statgate-controller:latest
DEMO_IMG ?= statgate-demo
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.1

.PHONY: all
all: generate manifests build

##@ Development

.PHONY: generate
generate: ## Generate deepcopy implementations
	$(CONTROLLER_GEN) object paths="./api/v1alpha1"

.PHONY: manifests
manifests: ## Generate CRD manifests
	$(CONTROLLER_GEN) crd paths="./api/v1alpha1" output:crd:artifacts:config=config/crd/bases
	rm -f config/crd/bases/_.yaml

.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: generate fmt vet ## Run tests
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: generate fmt vet ## Build controller binary
	go build -o bin/statgate-controller ./cmd/

.PHONY: run
run: generate fmt vet ## Run controller from host (for development)
	go run ./cmd/

.PHONY: docker-build
docker-build: ## Build controller Docker image
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push controller Docker image
	docker push $(IMG)

.PHONY: docker-build-demo
docker-build-demo: ## Build demo app images v1 and v2
	docker build --build-arg VERSION=v1 -t $(DEMO_IMG):v1 demo/app/
	docker build --build-arg VERSION=v2 -t $(DEMO_IMG):v2 demo/app/

##@ Deployment

.PHONY: install
install: manifests ## Install CRD into the cluster
	kubectl apply -f config/crd/bases/

.PHONY: uninstall
uninstall: ## Uninstall CRD from the cluster
	kubectl delete -f config/crd/bases/

.PHONY: deploy
deploy: ## Deploy controller to cluster via Helm
	helm upgrade --install statgate helm/statgate/

.PHONY: undeploy
undeploy: ## Remove controller from cluster
	helm uninstall statgate

.PHONY: demo
demo: ## Apply demo manifests
	kubectl apply -f demo/manifests/

.PHONY: demo-clean
demo-clean: ## Remove demo resources
	kubectl delete -f demo/manifests/ --ignore-not-found

##@ Help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
