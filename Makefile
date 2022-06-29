# Force Go Modules
GO111MODULE = on

GOCC ?= go
GOFLAGS ?=

# If set, override the install location for plugins
IPFS_PATH ?= $(HOME)/.ipfs

# If set, override the IPFS version to build against. This _modifies_ the local
# go.mod/go.sum files and permanently sets this version.
IPFS_VERSION ?= $(lastword $(shell $(GOCC) list -m github.com/ipfs/go-ipfs))

# make reproducible
ifneq ($(findstring /,$(IPFS_VERSION)),)
# Locally built go-ipfs
GOFLAGS += -asmflags=all=-trimpath="$(GOPATH)" -gcflags=all=-trimpath="$(GOPATH)"
else
# Remote version of go-ipfs (e.g. via `go get -trimpath` or official distribution)
GOFLAGS += -trimpath
endif

.PHONY: install build

go.mod: FORCE
	./set-target.sh $(IPFS_VERSION)

FORCE:

gcsplugin.so: plugin/main/main.go go.mod
	CGO_ENABLED=1 $(GOCC) build $(GOFLAGS) -buildmode=plugin -o "$@" "$<"
	chmod +x "$@"

build: gcsplugin.so
	@echo "Built against" $(IPFS_VERSION)

install: build
	install -Dm700 gcsplugin.so "$(IPFS_PATH)/plugins/go-ds-gcs.so"
