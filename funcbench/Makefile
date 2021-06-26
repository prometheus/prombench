INFRA_CMD        ?= ../infra/infra

PROVIDER 		 ?= gke
ifeq ($(PACKAGE_PATH),)
PACKAGE_PATH = ./...
endif

.PHONY: deploy clean
deploy: cluster_create resource_apply
# GCP sometimes takes longer than 30 tries when trying to delete nodepools
# if k8s resources are not already cleared
clean: resource_delete cluster_delete

cluster_create:
	$(INFRA_CMD) $(PROVIDER) cluster create -a ${AUTH_FILE} \
		-v GKE_PROJECT_ID:${GKE_PROJECT_ID} -v ZONE:${ZONE} -v CLUSTER_NAME:funcbench-${PR_NUMBER} -v PR_NUMBER:${PR_NUMBER} \
		-v EKS_WORKER_ROLE_ARN:${EKS_WORKER_ROLE_ARN} -v EKS_CLUSTER_ROLE_ARN:${EKS_CLUSTER_ROLE_ARN} \
		-f manifests/cluster_$(PROVIDER).yaml

cluster_delete:
	$(INFRA_CMD) $(PROVIDER) cluster delete -a ${AUTH_FILE} \
		-v GKE_PROJECT_ID:${GKE_PROJECT_ID} -v ZONE:${ZONE} -v CLUSTER_NAME:funcbench-${PR_NUMBER} -v PR_NUMBER:${PR_NUMBER} \
		-v EKS_WORKER_ROLE_ARN:${EKS_WORKER_ROLE_ARN} -v EKS_CLUSTER_ROLE_ARN:${EKS_CLUSTER_ROLE_ARN} \
		-f manifests/cluster_$(PROVIDER).yaml

resource_apply:
	$(INFRA_CMD) $(PROVIDER) resource apply -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v GKE_PROJECT_ID:${GKE_PROJECT_ID} -v CLUSTER_NAME:funcbench-${PR_NUMBER} \
		-v PR_NUMBER:${PR_NUMBER} -v GITHUB_TOKEN:${GITHUB_TOKEN} \
		-v GITHUB_ORG:${GITHUB_ORG} -v GITHUB_REPO:${GITHUB_REPO} \
		-v BRANCH:${BRANCH} -v 'BENCH_FUNC_REGEX:${BENCH_FUNC_REGEX}' \
		-v PACKAGE_PATH:${PACKAGE_PATH} \
		-f manifests/benchmark

# Removal of namespace should be at the end, after all other resources get removed.
resource_delete:
	$(INFRA_CMD) $(PROVIDER) resource delete -a ${AUTH_FILE} \
		-v ZONE:${ZONE} -v GKE_PROJECT_ID:${GKE_PROJECT_ID} -v CLUSTER_NAME:funcbench-${PR_NUMBER} \
		-v PR_NUMBER:${PR_NUMBER} -v GITHUB_TOKEN:${GITHUB_TOKEN} \
		-v GITHUB_ORG:${GITHUB_ORG} -v GITHUB_REPO:${GITHUB_REPO} \
		-v BRANCH:${BRANCH} -v 'BENCH_FUNC_REGEX:${BENCH_FUNC_REGEX}' \
		-v PACKAGE_PATH:${PACKAGE_PATH} \
		-f manifests/benchmark/3_job.yaml \
		-f manifests/benchmark/2_secrets.yaml \
		-f manifests/benchmark/1_namespace.yaml
