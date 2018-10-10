package s3ds

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
)

// listMax is the largest amount of objects you can request from S3 in a list call
const listMax = 1000

const defaultConcurrency = 100

type S3Bucket struct {
	Config
	S3      *s3.S3
	limiter chan struct{}
}

type Config struct {
	AccessKey      string
	SecretKey      string
	SessionToken   string
	Bucket         string
	Region         string
	RegionEndpoint string
	RootDirectory  string
	Concurrency    int
}

func NewS3Datastore(conf Config) (*S3Bucket, error) {
	if conf.Concurrency == 0 {
		conf.Concurrency = defaultConcurrency
	}

	awsConfig := aws.NewConfig()
	sess, err := session.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create new session: %s", err)
	}

	creds := credentials.NewChainCredentials([]credentials.Provider{
		&credentials.StaticProvider{Value: credentials.Value{
			AccessKeyID:     conf.AccessKey,
			SecretAccessKey: conf.SecretKey,
			SessionToken:    conf.SessionToken,
		}},
		&credentials.EnvProvider{},
		&credentials.SharedCredentialsProvider{},
		&ec2rolecreds.EC2RoleProvider{Client: ec2metadata.New(sess)},
	})

	if conf.RegionEndpoint != "" {
		awsConfig.WithS3ForcePathStyle(true)
		awsConfig.WithEndpoint(conf.RegionEndpoint)
	}

	awsConfig.WithCredentials(creds)
	awsConfig.WithRegion(conf.Region)

	sess, err = session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create new session with aws config: %s", err)
	}
	s3obj := s3.New(sess)

	return &S3Bucket{
		S3:      s3obj,
		Config:  conf,
		limiter: make(chan struct{}, conf.Concurrency),
	}, nil
}

func (s *S3Bucket) Put(k ds.Key, value []byte) error {
	_, err := s.S3.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
		Body:   bytes.NewReader(value),
	})
	return parseError(err)
}

func (s *S3Bucket) Get(k ds.Key) ([]byte, error) {
	resp, err := s.S3.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
	})
	if err != nil {
		return nil, parseError(err)
	}
	defer resp.Body.Close()

	return ioutil.ReadAll(resp.Body)
}

func (s *S3Bucket) Has(k ds.Key) (exists bool, err error) {
	_, err = s.S3.HeadObject(&s3.HeadObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
	})
	if err != nil {
		if parseError(err) == ds.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *S3Bucket) Delete(k ds.Key) error {
	_, err := s.S3.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
	})
	return parseError(err)
}

func (s *S3Bucket) Query(q dsq.Query) (dsq.Results, error) {
	if q.Orders != nil || q.Filters != nil {
		return nil, fmt.Errorf("s3ds doesn't support filters or orders")
	}

	limit := q.Limit + q.Offset
	if limit == 0 || limit > listMax {
		limit = listMax
	}

	resp, err := s.S3.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket:    aws.String(s.Bucket),
		Prefix:    aws.String(s.s3Path(q.Prefix)),
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int64(int64(limit)),
	})
	if err != nil {
		return nil, err
	}

	index := q.Offset
	nextValue := func() (dsq.Result, bool) {
		for index >= len(resp.Contents) {
			if !*resp.IsTruncated {
				return dsq.Result{}, false
			}

			resp, err = s.S3.ListObjectsV2(&s3.ListObjectsV2Input{
				Bucket:            aws.String(s.Bucket),
				Prefix:            aws.String(s.s3Path(q.Prefix)),
				Delimiter:         aws.String("/"),
				MaxKeys:           aws.Int64(listMax),
				ContinuationToken: resp.NextContinuationToken,
			})
			if err != nil {
				return dsq.Result{Error: err}, false
			}

			index -= len(resp.Contents)
			if index < 0 {
				index = 0
			}
		}

		entry := dsq.Entry{
			Key: *resp.Contents[index].Key,
		}
		if !q.KeysOnly {
			value, err := s.Get(ds.NewKey(entry.Key))
			if err != nil {
				return dsq.Result{Error: err}, false
			}
			entry.Value = value
		}

		index++
		return dsq.Result{Entry: entry}, true
	}

	return dsq.ResultsFromIterator(q, dsq.Iterator{
		Close: func() error {
			return nil
		},
		Next: nextValue,
	}), nil
}

func (s *S3Bucket) Batch() (ds.Batch, error) {
	return &s3Batch{
		s:   s,
		ops: make(map[string]batchOp),
	}, nil
}

func (s *S3Bucket) Close() error {
	close(s.limiter)
	return nil
}

func (s *S3Bucket) s3Path(p string) string {
	return path.Join(s.RootDirectory, p)
}

func parseError(err error) error {
	if s3Err, ok := err.(awserr.Error); ok && s3Err.Code() == s3.ErrCodeNoSuchKey {
		return ds.ErrNotFound
	}
	return err
}

type s3Batch struct {
	s   *S3Bucket
	ops map[string]batchOp
}

type batchOp struct {
	val    []byte
	delete bool
}

func (b *s3Batch) Put(k ds.Key, val []byte) error {
	b.ops[k.String()] = batchOp{
		val:    val,
		delete: false,
	}
	return nil
}

func (b *s3Batch) Delete(k ds.Key) error {
	b.ops[k.String()] = batchOp{
		val:    nil,
		delete: true,
	}
	return nil
}

func (b *s3Batch) Commit() error {
	var (
		deleteObjs []*s3.ObjectIdentifier
		putKeys    []ds.Key
	)
	for k, op := range b.ops {
		if op.delete {
			deleteObjs = append(deleteObjs, &s3.ObjectIdentifier{
				Key: aws.String(k),
			})
		} else {
			putKeys = append(putKeys, ds.NewKey(k))
		}
	}

	errChanSize := len(putKeys)
	if len(deleteObjs) > 0 {
		errChanSize++
	}
	errChan := make(chan error, errChanSize)
	defer close(errChan)

	for _, k := range putKeys {
		go func(k ds.Key, op batchOp) {
			b.s.limiter <- struct{}{}

			err := b.s.Put(k, op.val)
			errChan <- err

			<-b.s.limiter
		}(k, b.ops[k.String()])
	}

	if len(deleteObjs) > 0 {
		go func() {
			resp, err := b.s.S3.DeleteObjects(&s3.DeleteObjectsInput{
				Bucket: aws.String(b.s.Bucket),
				Delete: &s3.Delete{
					Objects: deleteObjs,
				},
			})
			if err != nil {
				errChan <- err
				return
			}

			var errs []string
			for _, err := range resp.Errors {
				errs = append(errs, err.String())
			}

			if len(errs) > 0 {
				err = fmt.Errorf("s3ds: failed to delete objects:\n%s", strings.Join(errs, "\n"))
			}
			errChan <- err
		}()
	}

	for i := 0; i < errChanSize; i++ {
		err := <-errChan
		if err != nil {
			return err
		}
	}

	return nil
}

var _ ds.Batching = (*S3Bucket)(nil)
