SRC := $(shell find ../.. -type f -name '*.go')
IPFS_PATH ?= $(HOME)/.ipfs

export CGO_ENABLED := 1

all: build

build: $(SRC)
	go build -buildmode=plugin -trimpath -o go-ds-s3-plugin

install: build
	mkdir -p "$(IPFS_PATH)/plugins"
	install -Dm700 go-ds-s3-plugin "$(IPFS_PATH)/plugins/go-ds-s3-plugin"

dist: build
	mkdir -p dist/go-ds-s3-plugin
	cp go-ds-s3-plugin README.md dist/go-ds-s3-plugin/
	chmod +x dist/go-ds-s3-plugin/go-ds-s3-plugin
	tar -C dist -zcf go-ds-s3-plugin.tar.gz go-ds-s3-plugin
	rm -rf go-ds-s3-plugin
	echo "Packaged as go-ds-s3-plugin.tar.gz"

.PHONY: install
