# Consumer media-ui: Vite SPA + thin gRPC BFF (mediauiprox).
# Build context: MuxCore workspace root (sibling module clones).
ARG GO_VERSION=1.26
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS ui
WORKDIR /ui
COPY media-ui-app/package.json media-ui-app/package-lock.json* ./
RUN npm install --no-fund --no-audit
COPY media-ui-app/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS bff
ARG MVP_DIR=mvp
WORKDIR /ws
# COPY mvp (or legacy _mvp via --build-arg MVP_DIR) plus every go.mod replace sibling.
COPY ${MVP_DIR} /ws/mvp
COPY core /ws/core
COPY contracts-automation /ws/contracts-automation
COPY contracts-metadata /ws/contracts-metadata
COPY contracts-scanner /ws/contracts-scanner
COPY contracts-media /ws/contracts-media
COPY jellyfin /ws/jellyfin
COPY media-ffprobe /ws/media-ffprobe
COPY media-intro-outro /ws/media-intro-outro
COPY media-list-sync /ws/media-list-sync
COPY media-movies /ws/media-movies
COPY media-subtitles /ws/media-subtitles
COPY media-tvshows /ws/media-tvshows
COPY userdata-local /ws/userdata-local
WORKDIR /ws/mvp
RUN CGO_ENABLED=0 go build -o /mediauiprox ./cmd/mediauiprox

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=ui /ui/dist-app /app/dist-app
COPY --from=bff /mediauiprox /usr/local/bin/mediauiprox
ENV MEDIA_UI_DIST=/app/dist-app \
    MEDIA_UI_LISTEN=:5173 \
    MOVIES_GRPC_CLIENT_ADDR=media-movies:9420 \
    TVSHOWS_GRPC_CLIENT_ADDR=media-tvshows:9440 \
    MOVIES_HTTP_URL=http://media-movies:9430 \
    TVSHOWS_HTTP_URL=http://media-tvshows:9450
EXPOSE 5173
CMD ["mediauiprox", "-listen", ":5173", "-dist", "/app/dist-app"]
