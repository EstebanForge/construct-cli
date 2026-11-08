# Dart SDK Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates unzip && \
    mkdir -p /opt/toolchains/dart && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then DART_ARCH="x64"; else DART_ARCH="arm64"; fi && \
    curl -fsSL "https://storage.googleapis.com/dart-archive/channels/stable/release/latest/sdk/dartsdk-linux-${DART_ARCH}-release.zip" -o /tmp/dart.zip && \
    unzip -q /tmp/dart.zip -d /tmp/dart-extract && \
    mv /tmp/dart-extract/dart-sdk/* /opt/toolchains/dart/ && \
    rm -rf /tmp/dart.zip /tmp/dart-extract /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/dart /opt/toolchains/dart
