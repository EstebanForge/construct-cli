# Swift 6.0 Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar && \
    mkdir -p /opt/toolchains/swift && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then \
        SWIFT_URL="https://download.swift.org/swift-6.0.3-release/ubuntu2404/swift-6.0.3-RELEASE/swift-6.0.3-RELEASE-ubuntu24.04.tar.gz"; \
    else \
        SWIFT_URL="https://download.swift.org/swift-6.0.3-release/ubuntu2404-aarch64/swift-6.0.3-RELEASE/swift-6.0.3-RELEASE-ubuntu24.04-aarch64.tar.gz"; \
    fi && \
    curl -fsSL "$SWIFT_URL" | tar -xz -C /opt/toolchains/swift --strip-components=2 && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/swift /opt/toolchains/swift
