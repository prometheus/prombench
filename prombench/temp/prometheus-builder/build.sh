#!/bin/bash
set -eu

CIRCLECI_BASE_API_URL="https://circleci.com/api/v1.1/project/github"
FILTER="successful"
CIRCLE_JOB="build"
OS="linux"
ARCH="amd64"

DIR="/tmp/prometheus"
rm -rf $DIR

if [[ -z $PR_NUMBER || -z $VOLUME_DIR || -z $GITHUB_ORG || -z $GITHUB_REPO || -z $LAST_COMMIT_SHA ]]; then
    echo "ERROR:: environment variables not set correctly"
    exit 1;
fi

# Clone git repo
echo ">> Cloning repository $GITHUB_ORG/$GITHUB_REPO"
if ! git clone https://github.com/$GITHUB_ORG/$GITHUB_REPO.git $DIR; then
    echo "ERROR:: Cloning of repo $GITHUB_ORG/$GITHUB_REPO failed"
    exit 1;
fi

cd $DIR || exit 1

# Checkout PR
echo ">> Fetching Pull Request $GITHUB_ORG/$GITHUB_REPO/pull/$PR_NUMBER"
if ! git fetch origin pull/$PR_NUMBER/head:pr-branch; then
    echo "ERROR:: Fetching of PR $PR_NUMBER failed"
    exit 1;
fi
git checkout pr-branch

# jq command to get the build.
JQ_ARG='[.[] | select(.build_parameters.CIRCLE_JOB==$JOB)][0] | {build_id: .build_num, v_rev: .vcs_revision}'

# Builds are returned in the order that they were created.
URL="$CIRCLECI_BASE_API_URL/$GITHUB_ORG/$GITHUB_REPO/tree/pull/$PR_NUMBER?filter=$FILTER"
BUILD=$(curl -s "$URL" | jq --arg JOB $CIRCLE_JOB "$JQ_ARG")
BUILD_ID=$(echo $BUILD | jq -r '.build_id' )
BUILD_REV=$(echo $BUILD | jq -r '.v_rev' )

if [[ $BUILD_REV == "null" ]]; then
    # Build prometheus locally.
    echo ">> Creating prometheus binaries"
    if ! make build PROMU_BINARIES="prometheus"; then
        echo "ERROR:: Building of binaries failed"
        exit 1;
    fi
else
    # Use circleCI artifact.
    if [ $LAST_COMMIT_SHA != $BUILD_REV ]; then
        # Check if the build is of the latest commit.
        echo "Last commit hash and the commit hash of the circleci build don't match. exiting"
        exit 1;
    fi

    # Get the artifact url.
    JQ_ARG='.[] | select(.path==$PATH) | .url'
    URL="$CIRCLECI_BASE_API_URL/$GITHUB_ORG/$GITHUB_REPO/$BUILD_ID/artifacts"
    PROMETHEUS_BIN_URL=$(curl -s "$URL" | jq -r --arg PATH "build/$OS-$ARCH/prometheus" "$JQ_ARG")

    # Download the artifact.
    echo ">> Downloading artifact: $PROMETHEUS_BIN_URL"
    curl -O "$PROMETHEUS_BIN_URL"
    chmod u+x prometheus
fi

# Copy files to volume.
echo ">> Copy files to volume"
cp prometheus               $VOLUME_DIR/prometheus
cp -r console_libraries/    $VOLUME_DIR
cp -r consoles/             $VOLUME_DIR
