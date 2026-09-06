# Multi-stage build for the ql570 print daemon + embedded web UI.
# Target platform: Raspberry Pi (ARM64), but the image builds on any host:
#
#   docker build --platform linux/arm64 -t ql570 .
#
# USB access requires running the container with --device=/dev/bus/usb
# (see docker-compose.yml) and the usblp kernel module loaded on the host.

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ql570 ./cmd/ql570

FROM debian:12-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ql570 /usr/local/bin/ql570
EXPOSE 9101
ENTRYPOINT ["ql570"]
CMD ["serve"]
