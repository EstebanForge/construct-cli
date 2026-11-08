# PHP Toolchain for Construct
FROM debian:trixie-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates php-cli php-mbstring php-xml php-curl && \
    mkdir -p /opt/toolchains/php/bin && \
    curl -fsSL https://getcomposer.org/composer-stable.phar -o /opt/toolchains/php/bin/composer && \
    chmod +x /opt/toolchains/php/bin/composer && \
    ln -s /usr/bin/php /opt/toolchains/php/bin/php && \
    rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates php-cli php-mbstring php-xml php-curl git unzip && \
    mkdir -p /opt/toolchains/php/bin && \
    curl -fsSL https://getcomposer.org/composer-stable.phar -o /opt/toolchains/php/bin/composer && \
    chmod +x /opt/toolchains/php/bin/composer && \
    ln -s /usr/bin/php /opt/toolchains/php/bin/php && \
    rm -rf /var/lib/apt/lists/*
