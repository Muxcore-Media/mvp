# Consumer media-ui: Vite SPA + thin gRPC BFF (mediauiprox).
# Build context: MuxCore workspace root.
ARG GO_VERSION=1.26
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS ui
WORKDIR /ui
COPY media-ui-app/package.json media-ui-app/package-lock.json* ./
RUN npm install --no-fund --no-audit
COPY media-ui-app/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS bff
WORKDIR /ws
COPY _mvp /ws/_mvp
COPY media-movies /ws/media-movies
COPY media-tvshows /ws/media-tvshows
WORKDIR /ws/_mvp
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
