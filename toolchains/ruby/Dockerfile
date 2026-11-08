# Ruby Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends ruby ruby-dev rubygems bundler ca-certificates && \
    mkdir -p /opt/toolchains/ruby/bin && \
    ln -s /usr/bin/ruby /opt/toolchains/ruby/bin/ruby && \
    ln -s /usr/bin/gem /opt/toolchains/ruby/bin/gem && \
    ln -s /usr/bin/bundle /opt/toolchains/ruby/bin/bundle && \
    ln -s /usr/bin/bundler /opt/toolchains/ruby/bin/bundler && \
    rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ruby ruby-dev rubygems bundler ca-certificates git && \
    mkdir -p /opt/toolchains/ruby/bin && \
    ln -s /usr/bin/ruby /opt/toolchains/ruby/bin/ruby && \
    ln -s /usr/bin/gem /opt/toolchains/ruby/bin/gem && \
    ln -s /usr/bin/bundle /opt/toolchains/ruby/bin/bundle && \
    ln -s /usr/bin/bundler /opt/toolchains/ruby/bin/bundler && \
    rm -rf /var/lib/apt/lists/*
