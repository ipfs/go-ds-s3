IPFS_PATH ?= ${HOME}/.ipfs

gx:
	go get github.com/whyrusleeping/gx
	go get github.com/whyrusleeping/gx-go

deps: gx
	gx --verbose install --global
	gx-go rewrite

build: deps
	go build -buildmode=plugin -o=s3plugin.so ./plugin

install: build
	mkdir -p ${IPFS_PATH}/plugins
	chmod +x s3plugin.so
	rm -f ${IPFS_PATH}/plugins/s3plugin.so
	cp s3plugin.so ${IPFS_PATH}/plugins/
