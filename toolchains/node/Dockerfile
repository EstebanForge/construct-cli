# Node.js LTS Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates xz-utils && \
    mkdir -p /opt/toolchains/node && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then NODE_ARCH="x64"; else NODE_ARCH="arm64"; fi && \
    curl -fsSL "https://nodejs.org/dist/v22.14.0/node-v22.14.0-linux-${NODE_ARCH}.tar.xz" | tar -xJ -C /opt/toolchains/node --strip-components=1 && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/node /opt/toolchains/node
