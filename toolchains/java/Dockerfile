# Java (Eclipse Temurin OpenJDK 21 LTS) Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar && \
    mkdir -p /opt/toolchains/java && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then JAVA_ARCH="x64"; else JAVA_ARCH="aarch64"; fi && \
    curl -fsSL "https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.6%2B7/OpenJDK21U-jdk_${JAVA_ARCH}_linux_hotspot_21.0.6_7.tar.gz" | tar -xz -C /opt/toolchains/java --strip-components=1 && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/java /opt/toolchains/java
