# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.23.12-alpine AS build

ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/heatguard ./cmd/server

FROM alpine:3.22
RUN addgroup -S heatguard \
    && adduser -S -G heatguard heatguard \
    && mkdir -p /data \
    && chown heatguard:heatguard /data
WORKDIR /app
COPY --from=build /out/heatguard /app/heatguard
USER heatguard
ENV HTTP_ADDR=:8080 DB_PATH=/data/heatguard.db
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=10 CMD wget -q -O - http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/heatguard"]
