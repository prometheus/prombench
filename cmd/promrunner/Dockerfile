FROM  quay.io/prometheus/busybox:latest
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"

COPY promrunner /bin/promrunner

RUN mkdir -p /promrunner /etc/prometheus/config /data

WORKDIR    /promrunner
ENTRYPOINT [ "/bin/promrunner" ]
