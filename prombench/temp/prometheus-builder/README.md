### Prometheus-Builder

This is used for building prometheus binaries from Pull Requests and running them on containers.
Prombench uses this to build binaries for the Pull Request being benchmarked.

### Running the docker image locally
```
docker run --rm -v $(pwd)/somedir:/prom -e VOLUME_DIR=/prom -e GITHUB_ORG=prometheus -e GITHUB_REPO=prometheus -e PR_NUMBER
=<pr_no> -e LAST_COMMIT_SHA=<last_sha_from_pr> docker.io/prombench/prometheus-builder:2.0.3
```

### Example for building the docker image
From the repository root:
```
$ make docker DOCKERFILE_PATH=prombench/temp/prometheus-builder/Dockerfile DOCKERBUILD_CONTEXT=prombench/temp/prometheus-builder DOCKER_IMAGE_NAME=prometheus-builder DOCKER_IMAGE_TAG=2.0.3
```
