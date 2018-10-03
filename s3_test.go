package s3ds

import (
	"testing"

	dstest "github.com/ipfs/go-datastore/test"
)

func TestSuite(t *testing.T) {
	s3ds, err := NewS3Datastore(Config{})
	if err != nil {
		t.Error(err)
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
}
