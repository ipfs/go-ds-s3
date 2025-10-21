# S3 Datastore Implementation

> [!WARNING]
> ## 🚧 Looking for Maintainers 🚧
>
> This is an opt-in datastore implementation for those who need S3-compatible storage backends.
> The project is in need of active maintenance from people who are actually using it. If you are interested in maintaining this datastore implementation, please reach out to devrel@ipfs.tech.

This is an implementation of the datastore interface backed by amazon s3.

**NOTE:** Plugins only work on Linux and MacOS at the moment. You can track the progress of this issue here: https://github.com/golang/go/issues/19282

## Quickstart

  1. Grab a plugin release from the [releases](https://github.com/ipfs/go-ds-s3/releases) section matching your Kubo version and install the plugin file in `~/.ipfs/plugins`.
  2. Follow the instructions in the plugin's [README.md](go-ds-s3-plugin/README.md)


## Building and installing


The plugin can be manually built/installed for different versions of Kubo (starting with 0.23.0) with:

```
git checkout go-ds-s3-plugin/v<kubo-version>
make plugin
make install-plugin
```

## Updating to a new version

  1. `go get` the Kubo release you want to build for. Make sure any other
     dependencies are aligned to what Kubo uses.
  2. `make install` and test.


If you are building against dist-released versions of Kubo, you need to build using the same version of go that was used to build the release ([here](https://github.com/ipfs/distributions/blob/master/.tool-versions)).

If you are building against your own build of Kubo you must align your plugin to use it.

If you are updating this repo to produce a new version of the plugin:

  1. Submit a PR so that integration tests run
  2. Make a new tag `go-ds-s3-plugin/v<kubo_version>` and push it. This will build and release the plugin prebuilt binaries.

## Bundling

As go plugins can be finicky to correctly compile and install, you may want to consider bundling this plugin and re-building kubo. If you do it this way, you won't need to install the `.so` file in your local repo, i.e following the above Building and Installing section, and you won't need to worry about getting all the versions to match up.

```bash
# We use go modules for everything.
> export GO111MODULE=on

# Clone kubo.
> git clone https://github.com/ipfs/kubo
> cd kubo

# Pull in the datastore plugin (you can specify a version other than latest if you'd like).
> go get github.com/ipfs/go-ds-s3@latest

# Add the plugin to the preload list.
> echo -en "\ns3ds github.com/ipfs/go-ds-s3/plugin 0" >> plugin/loader/preload_list

# ( this first pass will fail ) Try to build kubo with the plugin
> make build

# Update the deptree
> go mod tidy

# Now rebuild kubo with the plugin
> make build

# (Optionally) install kubo
> make install
```

## Contribute

Feel free to join in. All welcome. Open an [issue](https://github.com/ipfs/go-ipfs-example-plugin/issues)!

This repository falls under the IPFS [Code of Conduct](https://github.com/ipfs/community/blob/master/code-of-conduct.md).

### Want to hack on IPFS?

[![](https://cdn.rawgit.com/jbenet/contribute-ipfs-gif/master/img/contribute.gif)](https://github.com/ipfs/community/blob/master/CONTRIBUTING.md)

## License

MIT
