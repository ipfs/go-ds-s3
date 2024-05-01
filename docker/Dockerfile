FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.22 AS builder

ARG TARGETOS TARGETARCH

ENV SRC_DIR /kubo

WORKDIR $SRC_DIR

RUN git clone --branch v0.28.0 https://github.com/ipfs/kubo.git .
RUN go mod download

RUN go get github.com/ipfs/go-ds-s3@latest
RUN echo "\ns3ds github.com/ipfs/go-ds-s3/plugin 0" >> plugin/loader/preload_list

# Preload an in-tree but disabled-by-default plugin by adding it to the IPFS_PLUGINS variable
# e.g. docker build --build-arg IPFS_PLUGINS="foo bar baz"
ARG IPFS_PLUGINS

# Allow for other targets to be built, e.g.: docker build --build-arg MAKE_TARGET="nofuse"
ARG MAKE_TARGET=build

# Build the thing.
# Also: fix getting HEAD commit hash via git rev-parse.
RUN mkdir -p .git/objects \
    && GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOFLAGS=-buildvcs=false make ${MAKE_TARGET} IPFS_PLUGINS="$IPFS_PLUGINS"

FROM debian:stable-slim AS utilities

# Install jq for JSON manipulation in the config file
RUN apt-get update && apt-get install -y \
    jq \
    && apt-get clean

# The actual IPFS image we will use
FROM ipfs/kubo:v0.28.0

ENV SRC_DIR /kubo

# We copy the new binaries we built in the 'builder' stage (--from=builder)
COPY --from=builder $SRC_DIR/cmd/ipfs/ipfs /usr/local/bin/ipfs
COPY --from=builder $SRC_DIR/bin/container_daemon /usr/local/bin/start_ipfs
COPY --from=builder $SRC_DIR/bin/container_init_run /usr/local/bin/container_init_run

# Fix permissions on start_ipfs
RUN chmod 0755 /usr/local/bin/start_ipfs

# We copy jq so we can manipulate the JSON config file easily in the init.d scripts
COPY --from=utilities /usr/bin/jq /usr/local/bin/jq 
COPY --from=utilities /usr/lib/*-linux-*/libjq.so.1 /usr/lib/
COPY --from=utilities /usr/lib/*-linux-*/libonig.so.5 /usr/lib/

# init.d script IPFS runs before starting the daemon. Used to manipulate the IPFS config file.
COPY 001-config.sh /container-init.d/001-config.sh
