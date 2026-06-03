FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine3.23 AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/docre

FROM alpine:3.23

COPY --from=build /out/docre /usr/local/bin/docre

USER 65534:65534

ENTRYPOINT ["/usr/local/bin/docre"]
