# Package a module binary built on the host (scripts/build-module-binaries.sh,
# ADR-0014) — no Go toolchain or private-module access inside the image build.
#   docker build -f mvp/dockerfiles/module-prebuilt.Dockerfile -t muxcore/auth-local <PREBUILT_DIR>/auth-local
# Build context: the per-module directory <PREBUILT_DIR>/<module> holding `module`
# and any runtime files next to it
# (small context; publish-module-images.sh PREBUILT_DIR=… passes it).
# Runtime layout matches module.Dockerfile (uid 1000 `app`, same pre-created
# mount points; scripts/check-compose-mountpoints_test.sh keeps them in sync).
FROM alpine:3.21
# Extra runtime packages per module (publish-module-images.sh module_apk_extra,
# e.g. ffmpeg for media-ffprobe).
ARG APK_EXTRA=""
RUN apk add --no-cache ca-certificates curl ${APK_EXTRA} \
  && adduser -D -u 1000 -h /data app \
  && mkdir -p /data/downloads /data/movies /data/shows /data/media-ui \
     /data/backups /data/restore /data/dlna /data/tagging /data/intro-outro \
     /data/playback-guard /data/playback-monitor /data/transcoder-pool \
     /data/mesh-id /data/mesh-ca \
  && chown -R app:app /data && chmod 700 /data/mesh-id
USER app
WORKDIR /app
# The binary plus its runtime files (policies*.yaml), owned by root (read-only).
COPY . ./
ENTRYPOINT ["./module"]
