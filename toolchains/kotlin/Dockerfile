# Kotlin Compiler Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates unzip && \
    mkdir -p /opt/toolchains/kotlin && \
    curl -fsSL "https://github.com/JetBrains/kotlin/releases/download/v2.1.10/kotlin-compiler-2.1.10.zip" -o /tmp/kotlin.zip && \
    unzip -q /tmp/kotlin.zip -d /tmp/kotlin-extract && \
    mv /tmp/kotlin-extract/kotlinc/* /opt/toolchains/kotlin/ && \
    rm -rf /tmp/kotlin.zip /tmp/kotlin-extract /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/kotlin /opt/toolchains/kotlin
