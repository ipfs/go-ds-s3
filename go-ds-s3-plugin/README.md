# go-ds-s3-plugin

## Installation

On a brand new instance:

  1. Copy the binary `go-ds-s3-plugin` to `~/.ipfs/plugins`.
  2. Run `ipfs init`
  3. Update the datastore configuration in `.ipfs/config` as explained below. **This does not happen automatically**.
  4. Start Kubo (`ipfs daemon`). The plugin should be loaded automatically and the S3 backend should be used.

### Configuration

The config file should include the following. This must be edited manually after initializing Kubo:

```json
{
  "Datastore": {
  ...

    "Spec": {
      "mounts": [
        {
          "child": {
            "type": "s3ds",
            "region": "us-east-1",
            "bucket": "$bucketname",
            "rootDirectory": "$bucketsubdirectory",
            "accessKey": "",
            "secretKey": ""
          },
          "mountpoint": "/blocks",
          "prefix": "s3.datastore",
          "type": "measure"
        },
```

If the access and secret key are blank they will be loaded from the usual ~/.aws/.
If you are on another S3 compatible provider, e.g. Linode, then your config should be:

```json
{
  "Datastore": {
  ...

    "Spec": {
      "mounts": [
        {
          "child": {
            "type": "s3ds",
            "region": "us-east-1",
            "bucket": "$bucketname",
            "rootDirectory": "$bucketsubdirectory",
            "regionEndpoint": "us-east-1.linodeobjects.com",
            "accessKey": "",
            "secretKey": ""
          },
          "mountpoint": "/blocks",
          "prefix": "s3.datastore",
          "type": "measure"
        },
```

If you are configuring a brand new ipfs instance without any data, you can overwrite the datastore_spec file with:

```
{"mounts":[{"bucket":"$bucketname","mountpoint":"/blocks","region":"us-east-1","rootDirectory":"$bucketsubdirectory"},{"mountpoint":"/","path":"datastore","type":"levelds"}],"type":"mount"}
```

Otherwise, you need to do a datastore migration.
