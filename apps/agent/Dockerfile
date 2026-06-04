FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.work go.work.sum ./
COPY apps/agent/go.mod apps/agent/go.sum ./apps/agent/
WORKDIR /src/apps/agent
RUN go mod download

WORKDIR /src
COPY apps/agent ./apps/agent
WORKDIR /src/apps/agent
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mcsm-agent ./cmd/agent

FROM eclipse-temurin:21-jre
RUN useradd --system --create-home --home-dir /nonexistent --shell /usr/sbin/nologin mcsm \
    && mkdir -p /data/servers \
    && chown -R mcsm:mcsm /data
COPY --from=build /out/mcsm-agent /usr/local/bin/mcsm-agent
USER mcsm
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/mcsm-agent"]
