package s3ds

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path"
	"strings"
	"sync"

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

const (
	// listMax is the largest amount of objects you can request from S3 in a list
	// call.
	listMax = 1000

	// deleteMax is the largest amount of objects you can delete from S3 in a
	// delete objects call.
	deleteMax = 1000

	defaultWorkers = 100
)

type S3Bucket struct {
	Config
	S3 *s3.S3
}

type Config struct {
	AccessKey      string
	SecretKey      string
	SessionToken   string
	Bucket         string
	Region         string
	RegionEndpoint string
	RootDirectory  string
	Workers        int
}

func NewS3Datastore(conf Config) (*S3Bucket, error) {
	if conf.Workers == 0 {
		conf.Workers = defaultWorkers
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
		S3:     s3obj,
		Config: conf,
	}, nil
}

func (s *S3Bucket) Put(k ds.Key, value []byte) error {
	_, err := s.S3.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
		Body:   bytes.NewReader(value),
	})
	return err
}

func (s *S3Bucket) Sync(prefix ds.Key) error {
	return nil
}

func (s *S3Bucket) Get(k ds.Key) ([]byte, error) {
	resp, err := s.S3.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ds.ErrNotFound
		}
		return nil, err
	}
	defer resp.Body.Close()

	return ioutil.ReadAll(resp.Body)
}

func (s *S3Bucket) Has(k ds.Key) (exists bool, err error) {
	_, err = s.GetSize(k)
	if err != nil {
		if err == ds.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *S3Bucket) GetSize(k ds.Key) (size int, err error) {
	resp, err := s.S3.HeadObject(&s3.HeadObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
	})
	if err != nil {
		if s3Err, ok := err.(awserr.Error); ok && s3Err.Code() == "NotFound" {
			return -1, ds.ErrNotFound
		}
		return -1, err
	}
	return int(*resp.ContentLength), nil
}

func (s *S3Bucket) Delete(k ds.Key) error {
	_, err := s.S3.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
	})
	if isNotFound(err) {
		// delete is idempotent
		err = nil
	}
	return err
}

func querySupported(q dsq.Query) bool {
	if len(q.Orders) > 0 {
		switch q.Orders[0].(type) {
		case dsq.OrderByKey, *dsq.OrderByKey:
			// We order by key by default.
		default:
			return false
		}
	}
	return len(q.Filters) == 0
}

func (s *S3Bucket) Query(q dsq.Query) (dsq.Results, error) {
	// Handle ordering
	if !querySupported(q) {
		// OK, time to do this the naive way.

		// Skip the stuff we can't apply.
		baseQuery := q
		baseQuery.Filters = nil
		baseQuery.Orders = nil
		baseQuery.Limit = 0  // needs to apply after we order
		baseQuery.Offset = 0 // ditto.

		// perform the base query.
		res, err := s.Query(baseQuery)
		if err != nil {
			return nil, err
		}

		// fix the query
		res = dsq.ResultsReplaceQuery(res, q)

		// Remove the prefix, S3 has already handled it.
		naiveQuery := q
		naiveQuery.Prefix = ""

		// Apply the rest of the query
		return dsq.NaiveQueryApply(naiveQuery, res), nil
	}

	// Normalize the path and strip the leading / as S3 stores values
	// without the leading /.
	prefix := ds.NewKey(q.Prefix).String()[1:]

	sent := 0
	queryLimit := func() int64 {
		if max := q.Limit - sent; q.Limit <= 0 && max < listMax {
			return int64(max)
		}
		return listMax
	}

	resp, err := s.S3.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket:    aws.String(s.Bucket),
		Prefix:    aws.String(s.s3Path(prefix)),
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int64(queryLimit()),
	})
	if err != nil {
		return nil, err
	}

	index := q.Offset
	nextValue := func() (dsq.Result, bool) {
	tryAgain:
		if q.Limit > 0 && sent >= q.Limit {
			return dsq.Result{}, false
		}

		for index >= len(resp.Contents) {
			if !*resp.IsTruncated {
				return dsq.Result{}, false
			}

			index -= len(resp.Contents)

			resp, err = s.S3.ListObjectsV2(&s3.ListObjectsV2Input{
				Bucket:            aws.String(s.Bucket),
				Prefix:            aws.String(s.s3Path(prefix)),
				Delimiter:         aws.String("/"),
				MaxKeys:           aws.Int64(queryLimit()),
				ContinuationToken: resp.NextContinuationToken,
			})
			if err != nil {
				return dsq.Result{Error: err}, false
			}
		}

		entry := dsq.Entry{
			Key:  ds.NewKey(*resp.Contents[index].Key).String(),
			Size: int(*resp.Contents[index].Size),
		}
		if !q.KeysOnly {
			value, err := s.Get(ds.NewKey(entry.Key))
			switch err {
			case nil:
			case ds.ErrNotFound:
				// This just means the value got deleted in the
				// mean-time. That's not an error.
				//
				// We could use a loop instead of a goto, but
				// this is one of those rare cases where a goto
				// is easier to understand.
				goto tryAgain
			default:
				return dsq.Result{Entry: entry, Error: err}, false
			}
			entry.Value = value
		}

		index++
		sent++
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
		s:          s,
		ops:        make(map[string]batchOp),
		numWorkers: s.Workers,
	}, nil
}

func (s *S3Bucket) Close() error {
	return nil
}

func (s *S3Bucket) s3Path(p string) string {
	return path.Join(s.RootDirectory, p)
}

func isNotFound(err error) bool {
	s3Err, ok := err.(awserr.Error)
	return ok && s3Err.Code() == s3.ErrCodeNoSuchKey
}

type s3Batch struct {
	s          *S3Bucket
	ops        map[string]batchOp
	numWorkers int
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

	numJobs := len(putKeys) + (len(deleteObjs) / deleteMax)
	jobs := make(chan func() error, numJobs)
	results := make(chan error, numJobs)

	numWorkers := b.numWorkers
	if numJobs < numWorkers {
		numWorkers = numJobs
	}

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	defer wg.Wait()

	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			worker(jobs, results)
		}()
	}

	for _, k := range putKeys {
		jobs <- b.newPutJob(k, b.ops[k.String()].val)
	}

	if len(deleteObjs) > 0 {
		for i := 0; i < len(deleteObjs); i += deleteMax {
			limit := deleteMax
			if len(deleteObjs[i:]) < limit {
				limit = len(deleteObjs[i:])
			}

			jobs <- b.newDeleteJob(deleteObjs[i : i+limit])
		}
	}
	close(jobs)

	var errs []string
	for i := 0; i < numJobs; i++ {
		err := <-results
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("s3ds: failed batch operation:\n%s", strings.Join(errs, "\n"))
	}

	return nil
}

func (b *s3Batch) newPutJob(k ds.Key, value []byte) func() error {
	return func() error {
		return b.s.Put(k, value)
	}
}

func (b *s3Batch) newDeleteJob(objs []*s3.ObjectIdentifier) func() error {
	return func() error {
		resp, err := b.s.S3.DeleteObjects(&s3.DeleteObjectsInput{
			Bucket: aws.String(b.s.Bucket),
			Delete: &s3.Delete{
				Objects: objs,
			},
		})
		if err != nil && !isNotFound(err) {
			return err
		}

		var errs []string
		for _, err := range resp.Errors {
			if err.Code != nil && *err.Code == s3.ErrCodeNoSuchKey {
				// idempotent
				continue
			}
			errs = append(errs, err.String())
		}

		if len(errs) > 0 {
			return fmt.Errorf("failed to delete objects: %s", errs)
		}

		return nil
	}
}

func worker(jobs <-chan func() error, results chan<- error) {
	for j := range jobs {
		results <- j()
	}
}

var _ ds.Batching = (*S3Bucket)(nil)
