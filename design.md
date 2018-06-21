# Prombench
- Assume [Prow](https://github.com/sipian/test-infra/tree/prometheus-prow/prow/) is already running on a GKE cluster
- When Prombench is triggered using `/benchmark pr` or `/benchmark release <RELEASE_TAG/Default:Latest>`, the following environment variables are set in the Prowjob [start-benchmark](https://github.com/sipian/test-infra/blob/prometheus-prow/prow/config-prometheus.yaml#L62):
	- ZONE : zone of the prow cluster
	- CLUSTER_NAME : Name of the prow cluster
	- PR_NUMBER : Number of the PR where this comment was written
	- PROMETHEUS_1_NAME
	- PROMETHEUS_1_IMAGE
	- PROMETHEUS_2_NAME
	- PROMETHEUS_2_IMAGE

- A new [nodepool](config/cluster.yaml) is created in the prow cluster with the label `prombench-<PR_NUMBER>`
- [Prombench](manifests) is deployed on this nodepool in a new namespace `prombench-<PR_NUMBER>` (Only one Prombench instance can run on a PR)

- When `/benchmark delete` is triggered, this nodepool and namespace is deleted