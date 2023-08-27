FROM golang:1.19.8-bullseye AS builder

WORKDIR /

# Install jq for JSON manipulation in the config file
RUN apt update && apt install -y jq gcc make build-essential

# Kubo build process
# See details: https://github.com/ipfs/go-ds-s3
ENV GO111MODULE on
ENV GOPROXY direct

WORKDIR /app

COPY . /app/

RUN  make build