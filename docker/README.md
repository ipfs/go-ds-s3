# Dockerfile and script to integrate the go-ds-s3 plugin

The Dockerfile builds IPFS and the go-ds-s3 plugin together using the same golang version.
It copies the relevant files to the final Docker image.

We also copy the `001-config.sh` shell script to manipulate the IPFS config file before startup.

## Config changes

The script injects the correct config in the `Datastore.Spec` object to setup the plugin and
update the `datastore_spec` file to reflect the new datastore configuration.

Edit the `001-config.sh` to fit your use case.

## Building the image

```
cd docker
docker build -t my-ipfs-image .
```

## Running a container

```
export ipfs_staging=/local/data/ipfs_staging
export ipfs_data=/local/data/ipfs_data
docker run -d -v $ipfs_staging:/export -v $ipfs_data:/data/ipfs -p 4001:4001 -p 4001:4001/udp -p 127.0.0.1:8080:8080 -p 127.0.0.1:5001:5001 --env-file .env my-ipfs-image`
```

Note that we pass a `.env` file that contains the following environment variables:

```
AWS_REGION=<my_region>
CLUSTER_S3_BUCKET=<my_bucket>
CLUSTER_PEERNAME=<node_name>
CLUSTER_AWS_KEY=<aws_key>
CLUSTER_AWS_SECRET=<aws_secret>
```

