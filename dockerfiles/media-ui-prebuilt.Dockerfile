# Consumer media-ui from host-built artefacts (scripts/build-module-binaries.sh
# media-ui, ADR-0014): <PREBUILT_DIR>/media-ui/{mediauiprox,dist-app/}.
#   docker build -f mvp/dockerfiles/media-ui-prebuilt.Dockerfile -t muxcore/media-ui <PREBUILT_DIR>/media-ui
# Runtime layout matches media-ui.Dockerfile (uid 1000 `app` shared with
# admin-ui on media-ui-data; same pre-created mount points).
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
RUN useradd -u 1000 -U -M -d /data app \
  && mkdir -p /data/media-ui /data/restore \
  && chown -R app:app /data
COPY dist-app /app/dist-app
COPY mediauiprox /usr/local/bin/mediauiprox
ENV MEDIA_UI_DIST=/app/dist-app \
    MEDIA_UI_LISTEN=:5173 \
    MOVIES_GRPC_CLIENT_ADDR=media-movies:9420 \
    TVSHOWS_GRPC_CLIENT_ADDR=media-tvshows:9440 \
    MOVIES_HTTP_URL=http://media-movies:9430 \
    TVSHOWS_HTTP_URL=http://media-tvshows:9450
EXPOSE 5173
USER app
CMD ["mediauiprox", "-listen", ":5173", "-dist", "/app/dist-app"]
