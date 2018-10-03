# Automated Prometheus E2E testing and benchmarking

![Prombench Design](design.svg)

It runs with [Prow CI](https://github.com/kubernetes/test-infra/blob/master/prow/) on a [Google Kubernetes Engine Cluster](https://cloud.google.com/kubernetes-engine/).
It is designed to support adding more k8s providers.

## How to trigger tests on Github
A Prometheus maintainer can comment as follows to benchmark a PR:
- `/benchmark` equivalent to `/benchmark master`  - benchmark PR with the master branch.
- `/benchmark 2.4.0` - benchmark the PR with the 2.4.0 release(could be any release). Don't prepend `v` to the release version.

To cancel benchmarking, a mantainer should comment `/benchmark cancel`.

## Prerequisites
- Create a new Google cloud project - `prometheus-ci`
- Create a [Service Account](https://cloud.google.com/kubernetes-engine/docs/tutorials/authenticating-to-cloud-platform#step_3_create_service_account_credentials) on GKE with role `Kubernetes Engine Service Agent & Kubernetes Engine Admin` and download the json file.
- Generate a GitHub auth token that will be used to authenticate when sending requests to the GitHub API.
  * Login with the [Prombot account](https://github.com/prombot) and generate a [new auth token](https://github.com/settings/tokens).
  permissions:*public_repo, read:org, write:discussion*.
- Generate a [CircleCI project token](https://circleci.com/gh/prometheus/prometheus/edit#api) for the Prometheus project with the `All` scope. This token grants read access to all the build information.

- Set some env variable which will be used in the commands below.
  * **Note:** The `#GCLOUD_SERVICEACCOUNT_CLIENTID` is used to grant `cluster-admin-rights` to the `service-account` which needs to create RBAC roles. The `service-account` is used by the `prombench` tool when managing the cluster for each job.
```
export PROJECT_ID=prometheus-ci
export CLUSTER_NAME=prow
export ZONE=europe-west3-a
export AUTH_FILE=<path to service-account.json>
export GITHUB_ORG=prometheus
export GITHUB_REPO=prometheus
export GRAFANA_ADMIN_PASSWORD=$(openssl rand -hex 20)
export HMAC_TOKEN=$(openssl rand -hex 20)
export OAUTH_TOKEN=***Replace with the generated token from GitHub***
export CIRCLECI_TOKEN=***Replace with the project token from CircleCI***
export GCLOUD_SERVICEACCOUNT_CLIENTID=<client_id from the service-account.json>
```


- Add a [GitHub webhook](https://github.com/prometheus/prometheus/settings/hooks) where to send the events.
  * Content Type: `json`
  * Send:  `Issue comments,Pull requests`
  * Secret: `echo $HMAC_TOKEN`
  * Payload URL: `http://prombench.prometheus.io/hook`

    * **Note:** The ip DNS record for `prombench.prometheus.io` will be added once we get it from the ingress deployment in the following steps.

## Prow Setup

- Create the main k8s cluster to deploy the Prow components.

```
./prombench gke cluster create -a $AUTH_FILE -v PROJECT_ID:$PROJECT_ID \
-v ZONE:$ZONE -v CLUSTER_NAME:$CLUSTER_NAME -f components/prow/cluster.yaml
```
- Add all required tokens as k8s secrets.
  * hmac is used when verifying requests from GitHub.
  * oauth is used when sending requests to the GitHub API.
  * gke auth is used when scaling up and down the cluster.
  * circleci token is used to download artifacts from CircleCI.

```
./prombench gke resource apply -a $AUTH_FILE -v ZONE:$ZONE -v CLUSTER_NAME:$CLUSTER_NAME \
-f components/prow/manifests/secrets.yaml \
-v HMAC_TOKEN="$(printf $HMAC_TOKEN | base64 -w 0)" \
-v OAUTH_TOKEN="$(printf $OAUTH_TOKEN | base64 -w 0)" \
-v GKE_AUTH="$(cat $AUTH_FILE | base64 -w 0)"
-v CIRCLECI_TOKEN="$(printf $CIRCLECI_TOKEN | base64 -w 0)"
```

- Deploy the [nginx-ingress-controller](https://github.com/kubernetes/ingress-nginx) which will be used to access all public components.
```
./prombench gke resource apply -a $AUTH_FILE -v ZONE:$ZONE -v CLUSTER_NAME:$CLUSTER_NAME \
-v GCLOUD_SERVICEACCOUNT_CLIENTID:$GCLOUD_SERVICEACCOUNT_CLIENTID \
-f components/prow/manifests/rbac.yaml -f components/prow/manifests/nginx-controller.yaml
```

Get the ingress ip and use it to set the DNS ip record for `prombench.prometheus.io`.
```
kubectl get ingress ing -o go-template='{{ range .status.loadBalancer.ingress}}{{.ip}}{{ end }}'
```


- Deploy all internal prow components

  * **Note:** Long term plans are to use the [prombench cli tool](cmd/prombench) to deploy and manage everything, but at the moment the  k8s golang client doesn't support `CustomResourceDefinition` objects so for those it uses `kubectl`.
```
// Generate auth config so we can use kubectl.
gcloud container clusters get-credentials $CLUSTER_NAME --zone=$ZONE
kubectl apply -f components/prow/manifests/prow_internals_1.yaml

./prombench gke resource apply -a $AUTH_FILE -v PROJECT_ID:$PROJECT_ID \
-v ZONE:$ZONE -v CLUSTER_NAME:$CLUSTER_NAME \
-v GITHUB_ORG:$GITHUB_ORG -v GITHUB_REPO:$GITHUB_REPO \
-f components/prow/manifests/prow_internals_2.yaml
```

- Deploy the components that will collect and display the results.
```
export INGRESS_IP=$(kubectl get ingress ing -o go-template='{{ range .status.loadBalancer.ingress}}{{.ip}}{{ end }}')

./prombench gke resource apply -a $AUTH_FILE -v PROJECT_ID:$PROJECT_ID \
-v ZONE:$ZONE -v CLUSTER_NAME:$CLUSTER_NAME -v INGRESS_IP:$INGRESS_IP \
-v GRAFANA_ADMIN_PASSWORD:$GRAFANA_ADMIN_PASSWORD -f components/prombench/manifests/results
```

The services will be accessible at:
  * Prow dashboard :: http://prombench.prometheus.io
  * Grafana :: http://prombench.prometheus.io/grafana
  * Prometheus ::  http://prombench.prometheus.io/prometheus-meta
