PROMBENCH_CMD        = ./prombench
DOCKER_TAG = docker.io/prombench/prombench:2.0.1
GOLANG_IMG = golang:1.12
USERID = $(shell id -u ${USER})
USERGROUP = $(shell id -g ${USER})
DOCKER_CMD = docker run --rm \
			  -e GOPATH='/go' \
			  -e GO111MODULE='on' \
			  -e GOCACHE='/tmp/.cache' \
			  -v ${PWD}:/prombench \
			  -v ${GOPATH}:/go \
			  -w /prombench \
			  -u $(USERID):$(USERGROUP) \
			  $(GOLANG_IMG)

ifeq ($(AUTH_FILE),)
AUTH_FILE = "/etc/serviceaccount/service-account.json"
endif

deploy: nodepool_create resource_apply
clean: resource_delete nodepool_delete

nodepool_create:
	$(PROMBENCH_CMD) gke nodepool create -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v PROJECT_ID:${PROJECT_ID} -v CLUSTER_NAME:${CLUSTER_NAME} -v PR_NUMBER:${PR_NUMBER} \
		-f manifests/prombench/nodepools.yaml

resource_apply:
	$(PROMBENCH_CMD) gke resource apply -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v PROJECT_ID:${PROJECT_ID} -v CLUSTER_NAME:${CLUSTER_NAME} \
		-v PR_NUMBER:${PR_NUMBER} -v RELEASE:${RELEASE} -v DOMAIN_NAME:${DOMAIN_NAME} \
		-f manifests/prombench/benchmark

resource_delete:
	$(PROMBENCH_CMD) gke resource delete -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v PROJECT_ID:${PROJECT_ID} -v CLUSTER_NAME:${CLUSTER_NAME} -v PR_NUMBER:${PR_NUMBER} \
		-f manifests/prombench/benchmark/1a_namespace.yaml \
        -f manifests/prombench/benchmark/1c_cluster-role-binding.yaml

nodepool_delete:
	$(PROMBENCH_CMD) gke nodepool delete -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v PROJECT_ID:${PROJECT_ID} -v CLUSTER_NAME:${CLUSTER_NAME} -v PR_NUMBER:${PR_NUMBER} \
		-f manifests/prombench/nodepools.yaml

cluster_delete: prometheusmeta_resource_delete
	$(PROMBENCH_CMD) gke cluster delete -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v PROJECT_ID:${PROJECT_ID} -v CLUSTER_NAME:${CLUSTER_NAME} \
		-f manifests/cluster.yaml

prometheusmeta_resource_delete:
	$(PROMBENCH_CMD) gke resource delete -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v PROJECT_ID:${PROJECT_ID} -v CLUSTER_NAME:${CLUSTER_NAME} \
		-v DOMAIN_NAME:${DOMAIN_NAME} -v GRAFANA_ADMIN_PASSWORD:${GRAFANA_ADMIN_PASSWORD} \
		-v GCLOUD_SERVICEACCOUNT_CLIENT_EMAIL:${GCLOUD_SERVICEACCOUNT_CLIENT_EMAIL} \
		-f manifests/cluster-infra/3b_prometheus-meta.yaml

build:
	@$(DOCKER_CMD) go build ./cmd/prombench/

docker: build
	@docker build -t $(DOCKER_TAG) .
	@docker push $(DOCKER_TAG)

.PHONY: deploy clean build docker prometheusmeta_resource_delete cluster_delete
