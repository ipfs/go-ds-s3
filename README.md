# Google Cloud Storage Datastore Implementation

This is an implementation of the datastore interface backed by google cloud storage.

**NOTE:** Plugins only work on Linux and MacOS at the moment. You can track the progress of this issue here: https://github.com/golang/go/issues/19282

## Building and Installing

You must build the plugin with the *exact* version of go used to build the go-ipfs binary you will use it with. You can find the go version for go-ipfs builds from dist.ipfs.io in the build-info file, e.g. https://dist.ipfs.io/go-ipfs/v0.4.22/build-info or by running `ipfs version --all`.

In addition to needing the exact version of go, you need to build the correct version of this plugin.

* To build against a released version of go-ipfs, checkout the `release/v$VERSION` branch and build.
* To build against a custom (local) build of go-ipfs, run `make IPFS_VERSION=/path/to/go-ipfs/source`.

You can then install it into your local IPFS repo by running `make install`.

## Bundling

As go plugins can be finicky to correctly compile and install, you may want to consider bundling this plugin and re-building go-ipfs. If you do it this way, you won't need to install the `.so` file in your local repo and you won't need to worry about getting all the versions to match up.

```bash
# We use go modules for everything.
> export GO111MODULE=on

# Clone go-ipfs.
> git clone https://github.com/ipfs/go-ipfs
> cd go-ipfs

# Pull in the datastore plugin (you can specify a version other than latest if you'd like).
> go get github.com/luanet/go-ds-gcs/plugin@latest

# Add the plugin to the preload list.
> echo "s3ds github.com/luanet/go-ds-gcs/plugin 0" >> plugin/loader/preload_list

# ( this first pass will fail ) Try to build go-ipfs with the plugin
> make build

# Update the deptree
> go mod tidy

# Now rebuild go-ipfs with the plugin
> make build

# (Optionally) install go-ipfs
> make install
```

## Detailed Installation

For a brand new ipfs instance (no data stored yet):

1. Copy gcsplugin.so $IPFS_DIR/plugins/go-ds-gcs.so (or run `make install` if you are installing locally).
2. Run `ipfs init`.
3. Edit $IPFS_DIR/config to include gcs details (see Configuration below).
4. Overwrite `$IPFS_DIR/datastore_spec` as specified below (*Don't do this on an instance with existing data - it will be lost*).

### Configuration

The config file should include the following:
```json
{
  "Datastore": {
  ...

    "Spec": {
      "mounts": [
        {
          "child": {
            "type": "gcs",
            "bucket": "$bucketname",
            "workers": 1
          },
          "mountpoint": "/blocks",
          "prefix": "gcs.datastore",
          "type": "measure"
        },
```

If you are configuring a brand new ipfs instance without any data, you can overwrite the datastore_spec file with:

```
{"mounts":[{"bucket":"$bucketname","mountpoint":"/blocks"},{"mountpoint":"/","path":"datastore","type":"levelds"}],"type":"mount"}
```

Otherwise, you need to do a datastore migration.

## Contribute

Feel free to join in. All welcome. Open an [issue](https://github.com/ipfs/go-ipfs-example-plugin/issues)!

This repository falls under the IPFS [Code of Conduct](https://github.com/ipfs/community/blob/master/code-of-conduct.md).

### Want to hack on IPFS?

[![](https://cdn.rawgit.com/jbenet/contribute-ipfs-gif/master/img/contribute.gif)](https://github.com/ipfs/community/blob/master/CONTRIBUTING.md)

## License

MIT
