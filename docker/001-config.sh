#!/bin/sh
set -ex

echo "IPFS PATH: ${IPFS_PATH}"

# We backup old config file
cp ${IPFS_PATH}/config ${IPFS_PATH}/config_bak

# We inject ResourceMgr JSON object in IPFS config
# See: https://github.com/ipfs/kubo/blob/master/docs/config.md#swarmresourcemgr
# We also inject the S3 plugin datastore
# Important: Make sure your fill out the optionnal parameters $CLUSTER_S3_BUCKET, $CLUSTER_AWS_KEY, $CLUSTER_AWS_SECRET in the cloudformation parameters
cat ${IPFS_PATH}/config_bak | \
jq ".Swarm.ResourceMgr.Limits.System = { 
    Memory: 1073741824, 
    FD: 1024, 
    Conns: 1024, 
    ConnsInbound: 256, 
    ConnsOutboun: 1024, 
    Streams: 16384, 
    StreamsInbound: 4096, 
    StreamsOutbound: 16384 
}" | \
jq ".Datastore.Spec = { 
    mounts: [
        {
          child: {
            type: \"s3ds\",
            region: \"${AWS_REGION}\",
            bucket: \"${CLUSTER_S3_BUCKET}\",
            rootDirectory: \"${CLUSTER_PEERNAME}\",
            accessKey: \"${CLUSTER_AWS_KEY}\",
            secretKey: \"${CLUSTER_AWS_SECRET}\"
          },
          mountpoint: \"/blocks\",
          prefix: \"s3.datastore\",
          type: \"measure\"
        },
        {
          child: {
            compression: \"none\",
            path: \"datastore\",
            type: \"levelds\"
          },
          mountpoint: \"/\",
          prefix: \"leveldb.datastore\",
          type: \"measure\"
        }
    ], 
    type: \"mount\"
}" > ${IPFS_PATH}/config

# We override the ${IPFS_PATH}/datastore_spec file
echo "{\"mounts\":[{\"bucket\":\"${CLUSTER_S3_BUCKET}\",\"mountpoint\":\"/blocks\",\"region\":\"${AWS_REGION}\",\"rootDirectory\":\"${CLUSTER_PEERNAME}\"},{\"mountpoint\":\"/\",\"path\":\"datastore\",\"type\":\"levelds\"}],\"type\":\"mount\"}" > ${IPFS_PATH}/datastore_spec
