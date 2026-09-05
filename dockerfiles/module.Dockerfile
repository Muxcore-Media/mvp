# Build any MuxCore module from the workspace root.
#   docker build -f mvp/dockerfiles/module.Dockerfile --build-arg MODULE=auth-local -t muxcore/auth-local .
ARG MODULE
ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS builder
ARG MODULE
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY . .
WORKDIR /src/${MODULE}
RUN test -n "$MODULE" && test -d "/src/${MODULE}"
RUN go mod download
RUN if [ -d ./cmd/module ]; then \
      CGO_ENABLED=0 go build -o /module ./cmd/module; \
    elif [ -f ./main.go ]; then \
      CGO_ENABLED=0 go build -o /module .; \
    else \
      echo "no module entrypoint in ${MODULE}" >&2; exit 1; \
    fi

FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl && adduser -D -h /data app
USER app
WORKDIR /app
COPY --from=builder /module ./module
ENTRYPOINT ["./module"]
