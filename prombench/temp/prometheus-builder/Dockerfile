FROM golang:1.13
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"

RUN apt-get update && apt-get install -y --no-install-recommends \
    apt-utils \
    build-essential \
    ca-certificates \
    make \
    git \
    jq \
    curl \
    && curl -sS https://dl.yarnpkg.com/debian/pubkey.gpg | apt-key add - \
    && echo "deb https://dl.yarnpkg.com/debian/ stable main" > /etc/apt/sources.list.d/yarn.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends nodejs yarn \
    && rm -rf /var/lib/apt/lists/*

COPY build.sh /
RUN chmod +x /build.sh

ENTRYPOINT ["/build.sh"]
