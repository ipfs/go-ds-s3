package s3ds

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	dstest "github.com/ipfs/go-datastore/test"
)

func TestSuiteLocalS3(t *testing.T) {
	// Only run tests when LOCAL_S3 is set, since the tests are only set up for a local S3 endpoint.
	// To run tests locally, run `docker-compose up` in this repo in order to get a local S3 running
	// on port 9000. Then run `LOCAL_S3=true go test -v ./...` to execute tests.
	if _, localS3 := os.LookupEnv("LOCAL_S3"); !localS3 {
		t.Skipf("skipping test suit; LOCAL_S3 is not set.")
	}

  localBucketName, localBucketNameSet := os.LookupEnv("LOCAL_BUCKET_NAME")
  if !localBucketNameSet {
    localBucketName = fmt.Sprintf("localbucketname%d", time.Now().UnixNano())
	}

	config := Config{
		RegionEndpoint: "http://localhost:9000",
		Bucket:         localBucketName,
		Region:         "local",
		AccessKey:      "test",
		SecretKey:      "testdslocal",
		KeyTransform:   "default",
	}

	s3ds, err := NewS3Datastore(config)
	if err != nil {
		t.Fatal(err)
	}

	if err = devMakeBucket(s3ds.S3, localBucketName); err != nil {
		t.Fatal(err)
	}

	t.Run("basic operations", func(t *testing.T) {
		dstest.SubtestBasicPutGet(t, s3ds)
	})
	t.Run("not found operations", func(t *testing.T) {
		dstest.SubtestNotFounds(t, s3ds)
	})
	t.Run("many puts and gets, query", func(t *testing.T) {
		dstest.SubtestManyKeysAndQuery(t, s3ds)
	})
	t.Run("return sizes", func(t *testing.T) {
		dstest.SubtestReturnSizes(t, s3ds)
	})
}

func devMakeBucket(s3obj *s3.S3, bucketName string) error {
	s3obj.DeleteBucket(&s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	_, err := s3obj.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})

	return err
}
