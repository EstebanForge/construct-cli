# Go Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar && \
    mkdir -p /opt/toolchains/go && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then GO_ARCH="amd64"; else GO_ARCH="arm64"; fi && \
    curl -fsSL "https://go.dev/dl/go1.24.0.linux-${GO_ARCH}.tar.gz" | tar -xz -C /opt/toolchains/go --strip-components=1 && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/go /opt/toolchains/go
