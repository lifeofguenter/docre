FROM golang:1.14-alpine AS builder

WORKDIR /go/src/app
COPY . .
RUN go get -d -v
RUN CGO_ENABLED=0 GOOS=linux go build -a -v

FROM alpine:3

ENTRYPOINT ["/usr/local/bin/docre"]

RUN set -ex &&\
    apk add --no-progress --no-cache \
      bash \
      ca-certificates \
      gettext \
      curl \
      jq \
      zip

COPY --from=builder /go/src/app/docre /usr/local/bin/
