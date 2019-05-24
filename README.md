# S3 Datastore Implementation

This is an implementation of the datastore interface backed by amazon s3.

**NOTE:** Plugins only work on Linux and MacOS at the moment. You can track the progress of this issue here: https://github.com/golang/go/issues/19282

## Building and Installing

You can this plugin by running `make build`. You can then install it into your local IPFS repo by running `make install`.

Plugins need to be built against the correct version of go-ipfs. This package generally tracks the latest go-ipfs release but if you need to build against a different version, please set the `IPFS_VERSION` environment variable.

You can set `IPFS_VERSION` to:

* `vX.Y.Z` to build against that version of IPFS.
* `$commit` or `$branch` to build against a specific go-ipfs commit or branch.
* `/absolute/path/to/source` to build against a specific go-ipfs checkout.

To update the go-ipfs, run:

```bash
> make go.mod IPFS_VERSION=version
```

## Contribute

Feel free to join in. All welcome. Open an [issue](https://github.com/ipfs/go-ipfs-example-plugin/issues)!

This repository falls under the IPFS [Code of Conduct](https://github.com/ipfs/community/blob/master/code-of-conduct.md).

### Want to hack on IPFS?

[![](https://cdn.rawgit.com/jbenet/contribute-ipfs-gif/master/img/contribute.gif)](https://github.com/ipfs/community/blob/master/contributing.md)

## License

MIT
