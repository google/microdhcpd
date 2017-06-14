FROM alpine:latest

RUN set -ex; \
  apk add --no-cache --no-progress --virtual .build-deps git gcc musl-dev bash go; \
  env GOPATH=/go go get -v github.com/google/microdhcpd; \
  install -t /bin /go/bin/microdhcpd; \
  rm -rf /go; \
  apk --no-progress del .build-deps
