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
# Runtime files the module reads from its working dir (policies.yaml, …).
RUN mkdir -p /assets && for f in policies*.yaml; do [ -f "$f" ] && cp "$f" /assets/; done; true

FROM alpine:3.21
# Compose mounts named volumes at these paths (docker-compose.registry.yml). A
# missing mount point is created root-owned, and an empty named volume inherits
# the image directory's owner on first mount, so pre-create them owned by `app`
# (uid 1000, shared with media-ui.Dockerfile). scripts/check-compose-mountpoints_test.sh
# keeps this list in sync with the compose file.
# Extra runtime packages per module (publish-module-images.sh module_apk_extra).
ARG APK_EXTRA=""
RUN apk add --no-cache ca-certificates curl ${APK_EXTRA} \
  && adduser -D -u 1000 -h /data app \
  && mkdir -p /data/downloads /data/movies /data/shows /data/media-ui \
     /data/backups /data/restore /data/dlna /data/tagging /data/intro-outro \
     /data/playback-guard /data/playback-monitor /data/transcoder-pool \
  && chown -R app:app /data
USER app
WORKDIR /app
COPY --from=builder /module ./module
COPY --from=builder /assets/ ./
ENTRYPOINT ["./module"]
