FROM ubuntu:20.04
# (glibc 2.29)
# TODO: Change base image to to debian:bullseye-slim on 2025-05-29 (glibc 2.31)
# Then to ubuntu:22.04 on 2026-06-30 (glibc 2.35)
# Then to debian:bookworm-slim on 2027-06-01 (glibc 2.36)
# Then to ubuntu:24:04 on 2028-06-30 (glibc 2.39)

LABEL org.opencontainers.image.source=https://github.com/NordSecurity/nordvpn-linux

# Based on: https://github.com/docker-library/golang/blob/master/1.22/bookworm/Dockerfile
ENV GOLANG_VERSION 1.23.3
ENV GOLANG_SHA256 a0afb9744c00648bafb1b90b4aba5bdb86f424f02f9275399ce0c20b93a2c3a8

RUN apt-get update && \
    apt-get -y --no-install-recommends upgrade && \
    apt-get -y --no-install-recommends install \
        iptables \
        iproute2 \
        wget \
        curl \
        ca-certificates \
        git \
        unzip && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install Go from the official binaries
RUN wget --progress=dot:giga "https://go.dev/dl/go${GOLANG_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz && \
    echo "$GOLANG_SHA256 /tmp/go.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/go.tar.gz -C /usr/local && rm /tmp/go.tar.gz

RUN echo "deb [arch=i386,amd64] http://archive.ubuntu.com/ubuntu/ focal main universe" > /etc/apt/sources.list && \
    echo "deb [arch=i386,amd64] http://archive.ubuntu.com/ubuntu/ focal-updates main universe" >> /etc/apt/sources.list && \
    echo "deb [arch=i386,amd64] http://security.ubuntu.com/ubuntu/ focal-security main universe" >> /etc/apt/sources.list && \
    echo "deb [arch=armhf,arm64] http://ports.ubuntu.com/ubuntu-ports/ focal main universe" >> /etc/apt/sources.list && \
    echo "deb [arch=armhf,arm64] http://ports.ubuntu.com/ubuntu-ports/ focal-updates main universe" >> /etc/apt/sources.list && \
    echo "deb [arch=armhf,arm64] http://ports.ubuntu.com/ubuntu-ports/ focal-security main universe" >> /etc/apt/sources.list

# Don't auto-upgrade the gotoolchain
# https://github.com/docker-library/golang/issues/472
ENV GOTOOLCHAIN=local
ENV GOBIN=/usr/bin
ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH
RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" "$GOPATH/pkg/mod" && chmod -R 1777 "$GOPATH" && rm /etc/environment
