# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS source
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM source AS core-build
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION}" \
    -o /out/core ./app/core/cmd/core

FROM source AS vision-build
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION}" \
    -o /out/vision ./app/vision/cmd/vision && \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
    -ldflags "-s -w" \
    -o /out/provider-credentials ./app/vision/cmd/provider-credentials

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S tiehu && adduser -S -G tiehu -h /app tiehu
WORKDIR /app

FROM runtime AS core
COPY --from=core-build --chown=tiehu:tiehu /out/core /app/core
COPY --chown=tiehu:tiehu configs/core.yaml /app/configs/core.yaml
USER tiehu
EXPOSE 8000 9000
ENTRYPOINT ["/app/core"]
CMD ["-conf", "/app/configs/core.yaml"]

FROM runtime AS vision
COPY --from=vision-build --chown=tiehu:tiehu /out/vision /app/vision
COPY --from=vision-build --chown=tiehu:tiehu /out/provider-credentials /app/provider-credentials
COPY --chown=tiehu:tiehu configs/vision.yaml /app/configs/vision.yaml
USER tiehu
EXPOSE 8100 9100
ENTRYPOINT ["/app/vision"]
CMD ["-conf", "/app/configs/vision.yaml"]
