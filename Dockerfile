FROM golang:1.9 AS builder

WORKDIR /go/src/github.com/prometheus/mysqld_exporter

COPY config/collector ./collector
COPY config/vendor ./vendor
COPY config/mysqld_exporter.go ./

RUN env GO15VENDOREXPERIMENT=1 \
        CGO_ENABLED=0 \
        GOOS=linux \
        GOARCH=amd64 \
    go build -o mysqld_exporter mysqld_exporter.go \
    && cp ./mysqld_exporter /mysqld_exporter

FROM scratch

# MySQL Exporter image for OpenShift Origin

LABEL io.k8s.description="MySQL Prometheus Exporter." \
      io.k8s.display-name="MySQL Exporter" \
      io.openshift.expose-services="9113:http" \
      io.openshift.tags="mysql,exporter,prometheus" \
      io.openshift.non-scalable="true" \
      help="For more information visit https://github.com/Worteks/docker-mysqlexporter" \
      maintainer="Samuel MARTIN MORO <faust64@gmail.com>" \
      version="1.0"

COPY --from=builder /mysqld_exporter /mysqld_exporter

ENTRYPOINT ["/mysqld_exporter"]
