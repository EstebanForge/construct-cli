# Rust Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar bash && \
    mkdir -p /tmp/rust /opt/toolchains/rust && \
    ARCH=$(dpkg --print-architecture) && \
    if [ "$ARCH" = "amd64" ]; then RUST_TARGET="x86_64-unknown-linux-gnu"; else RUST_TARGET="aarch64-unknown-linux-gnu"; fi && \
    curl -fsSL "https://static.rust-lang.org/dist/rust-1.85.0-${RUST_TARGET}.tar.gz" | tar -xz -C /tmp/rust --strip-components=1 && \
    /tmp/rust/install.sh --prefix=/opt/toolchains/rust --components=rustc,cargo,rust-std-${RUST_TARGET} && \
    rm -rf /tmp/rust /var/lib/apt/lists/*

FROM scratch
COPY --from=build /opt/toolchains/rust /opt/toolchains/rust
