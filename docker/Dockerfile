FROM golang:1.21-bookworm AS builder

WORKDIR /

# Install jq for JSON manipulation in the config file
RUN apt update && apt install -y jq

# Kubo build process
# See details: https://github.com/ipfs/go-ds-s3
ENV GO111MODULE on
ENV GOPROXY direct

# We clone Kubo source code.
RUN git clone https://github.com/ipfs/kubo
ENV SRC_DIR /kubo

# Move to kubo folder
WORKDIR $SRC_DIR

# Install the plugin and build ipfs
RUN go get github.com/ipfs/go-ds-s3@latest
RUN echo "\ns3ds github.com/ipfs/go-ds-s3/plugin 0" >> plugin/loader/preload_list
RUN make build || : #first build will fail
RUN go mod tidy
RUN make build
RUN make install

# The actual IPFS image we will use
FROM ipfs/kubo:v0.23.0
ENV SRC_DIR /kubo

# We copy the new binaries we built in the 'builder' stage (--from=builder)
COPY --from=builder $SRC_DIR/cmd/ipfs/ipfs /usr/local/bin/ipfs
COPY --from=builder $SRC_DIR/bin/container_daemon /usr/local/bin/start_ipfs
COPY --from=builder $SRC_DIR/bin/container_init_run /usr/local/bin/container_init_run

# Fix permissions on start_ipfs
RUN chmod 0755 /usr/local/bin/start_ipfs

# We copy jq so we can manipulate the JSON config file easily in the init.d scripts
COPY --from=builder /usr/bin/jq /usr/local/bin/jq 
COPY --from=builder /usr/lib/*-linux-*/libjq.so.1 /usr/lib/
COPY --from=builder /usr/lib/*-linux-*/libonig.so.5 /usr/lib/

# init.d script IPFS runs before starting the daemon. Used to manipulate the IPFS config file.
COPY 001-config.sh /container-init.d/001-config.sh
