#!/bin/bash

GOCC="${GOCC:-go}"

set -eo pipefail

GOPATH="$($GOCC env GOPATH)"
VERSION="$1"
PKG=github.com/ipfs/go-ipfs

if [[ "$VERSION" == /* ]]; then
    # Build against a local repo
    PKGDIR="$VERSION"
    $GOCC mod edit -replace "github.com/ipfs/go-ipfs=$VERSION"
else
    $GOCC mod edit -dropreplace=github.com/ipfs/go-ipfs
    # Resolve the exact version/package name
    PKGDIR="$(go list -f '{{.Dir}}' -m "$PKG@$VERSION")"
    resolvedver="$(go list -f '{{.Version}}' -m "$PKG@$VERSION")"

    # Update to that version.
    $GOCC get $PKG@$resolvedver
fi

$GOCC mod edit $(cd "$PKGDIR" && go list -f '-require={{.Path}}@{{.Version}}{{if .Replace}} -replace={{.Path}}@{{.Version}}={{.Replace}}{{end}}' -m all | tail -n+2)
$GOCC mod tidy
