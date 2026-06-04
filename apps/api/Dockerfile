FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.work go.work.sum ./
COPY apps/api/go.mod apps/api/go.sum ./apps/api/
WORKDIR /src/apps/api
RUN go mod download

WORKDIR /src
COPY apps/api ./apps/api
WORKDIR /src/apps/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mcsm-api ./cmd/server

FROM debian:bookworm-slim
RUN useradd --system --create-home --home-dir /nonexistent --shell /usr/sbin/nologin mcsm \
    && mkdir -p /data/api /data/servers \
    && chown -R mcsm:mcsm /data
COPY --from=build /out/mcsm-api /usr/local/bin/mcsm-api
USER mcsm
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/mcsm-api"]
