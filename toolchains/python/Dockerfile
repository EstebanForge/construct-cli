# Python Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar && \
    mkdir -p /opt/toolchains/python && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then PY_ARCH="x86_64-unknown-linux-gnu"; else PY_ARCH="aarch64-unknown-linux-gnu"; fi && \
    curl -fsSL "https://github.com/astral-sh/python-build-standalone/releases/download/20250212/cpython-3.12.9+20250212-${PY_ARCH}-install_only.tar.gz" | tar -xz -C /opt/toolchains/python --strip-components=1 && \
    rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/python /opt/toolchains/python
