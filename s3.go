package s3ds

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log"
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

var (
	log      = logging.Logger("ds/s3")
	cacheLog = logging.Logger("ds/s3/cache")
)

type S3Bucket struct {
	Config
	S3        *s3.S3
	keys      map[ds.Key]int
	keysMutex sync.RWMutex
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
	CacheKeys      bool
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

	bucket := &S3Bucket{
		S3:     s3obj,
		Config: conf,
	}

	if conf.CacheKeys {
		bucket.keys = make(map[ds.Key]int)

		if err := bucket.fetchKeyCache(); err != nil {
			return nil, err
		}

		go func() {
			for {
				time.Sleep(5 * time.Minute)

				err := bucket.fetchKeyCache()
				if err != nil {
					fmt.Println(err)
				}
			}
		}()
	}

	return bucket, nil
}

func (s *S3Bucket) Put(k ds.Key, value []byte) error {
	_, err := s.S3.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(k.String())),
		Body:   bytes.NewReader(value),
	})
	if s.CacheKeys {
		s.cachePut(k, -1)
	}
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
	if s.CacheKeys {
		return s.cacheHas(k), nil
	} else {
		_, err = s.GetSize(k)
		if err != nil {
			if err == ds.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
}

func (s *S3Bucket) GetSize(k ds.Key) (size int, err error) {
	if s.CacheKeys {
		i, exists := s.cacheGet(k)
		if exists && i != -1 {
			return i, nil
		} else {
			if !exists {
				return -1, ds.ErrNotFound
			} else {
				cacheLog.Warn("GetSize: Key ", k, " existed in cache, but was -1")
			}
		}
	}
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
	size = int(*resp.ContentLength)

	if s.CacheKeys {
		s.cachePut(k, size)
		cacheLog.Debug("GetSize: Put key ", k, " in cache")
	}

	return
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
	if s.CacheKeys {
		s.cacheDel(k)
	}
	return err
}

func (s *S3Bucket) Query(q dsq.Query) (dsq.Results, error) {
	if q.Orders != nil || q.Filters != nil {
		return nil, fmt.Errorf("s3ds: filters or orders are not supported")
	}

	// S3 store a "/foo" key as "foo" so we need to trim the leading "/"
	q.Prefix = strings.TrimPrefix(q.Prefix, "/")

	limit := q.Limit + q.Offset
	if limit == 0 || limit > listMax {
		limit = listMax
	}

	resp, err := s.S3.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket:  aws.String(s.Bucket),
		Prefix:  aws.String(s.s3Path(q.Prefix)),
		MaxKeys: aws.Int64(int64(limit)),
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

			index -= len(resp.Contents)

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
		}

		entry := dsq.Entry{
			Key:  ds.NewKey(*resp.Contents[index].Key).String(),
			Size: int(*resp.Contents[index].Size),
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

func (s *S3Bucket) cacheGet(key ds.Key) (int, bool) {
	cacheLog.Debug("cacheGet: RLock for   ", key)
	s.keysMutex.RLock()
	i, exists := s.keys[key]
	s.keysMutex.RUnlock()
	cacheLog.Debug("cacheGet: RUnlock for ", key)
	return i, exists
}

func (s *S3Bucket) cacheHas(key ds.Key) (exists bool) {
	_, exists = s.cacheGet(key)
	return
}

func (s *S3Bucket) cachePut(key ds.Key, size int) {
	log.Debug("cachePut: WRITE Lock for   ", key)
	s.keysMutex.Lock()
	s.keys[key] = size
	s.keysMutex.Unlock()
	log.Debug("cachePut: WRITE Unlock for ", key)
}

func (s *S3Bucket) cacheDel(key ds.Key) {
	log.Debug("cacheDel: WRITE Lock for   ", key)
	s.keysMutex.Lock()
	delete(s.keys, key)
	s.keysMutex.Unlock()
	log.Debug("cacheDel: WRITE Unlock for ", key)
}

func (s *S3Bucket) fetchKeyCache() error {
	log.Debug("fetchKeyCache: Fetching...")
	results, err := s.Query(dsq.Query{
		KeysOnly: true,
	})
	if err != nil {
		log.Warn("fetchKeyCache: Fetching key cache failed with ", err)
		return err
	}

	local := map[ds.Key]int{}
	localOnly := map[ds.Key]int{}
	notLocal := map[ds.Key]bool{}

	for {
		result, notfinished := results.NextSync()
		if !notfinished {
			break
		}
		local[ds.NewKey(result.Key)] = result.Size
	}

	// Sweep
	s.keysMutex.RLock()
	for cacheKey := range s.keys {
		if _, ok := local[cacheKey]; !ok {
			notLocal[cacheKey] = true
		}
	}

	for localKey := range local {
		if i, ok := s.keys[localKey]; !ok {
			localOnly[localKey] = i
		}
	}
	s.keysMutex.RUnlock()

	// Apply
	for notLocalKey := range notLocal {
		log.Debug("fetchKeyCache: Removing key ", notLocalKey)
		s.cacheDel(notLocalKey)
	}

	for localOnlyKey, i := range localOnly {
		log.Debug("fetchKeyCache: Adding key ", localOnlyKey)
		s.cachePut(localOnlyKey, i)
	}

	s.keysMutex.RLock()
	size := len(s.keys)
	s.keysMutex.RUnlock()

	log.Debug("fetchKeyCache: Key cache now contains ", size, " keys")

	return nil
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
