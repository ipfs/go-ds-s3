package s3ds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/datastore/dshelp"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ds-s3/pkg/filecache"
	disk "github.com/ipfs/go-ds-s3/pkg/minio-disk"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/credentials/endpointcreds"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/defaults"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/aws/aws-sdk-go/service/sts"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log/v2"
)

const (
	// listMax is the largest amount of objects you can request from S3 in a list
	// call.
	listMax = 1000

	// deleteMax is the largest amount of objects you can delete from S3 in a
	// delete objects call.
	deleteMax = 1000

	defaultWorkers = 100

	// credsRefreshWindow, subtracted from the endpointcred's expiration time, is the
	// earliest time the endpoint creds can be refreshed.
	credsRefreshWindow = 2 * time.Minute
)

var (
	_   ds.Datastore = (*S3Bucket)(nil)
	log              = logging.Logger("godss3")
)

type S3Bucket struct {
	Config
	S3    s3iface.S3API
	Cache filecache.FileCache
}

type Config struct {
	AccessKey           string
	SecretKey           string
	SessionToken        string
	Bucket              string
	Region              string
	RegionEndpoint      string
	RootDirectory       string
	Workers             int
	CredentialsEndpoint string
	KeyTransform        string
	CacheDirectory      string
	CacheCapacity       int64
}

var KeyTransforms = map[string]func(ds.Key) string{
	"default": func(k ds.Key) string {
		return k.String()
	},
	"suffix": func(k ds.Key) string {
		return k.String() + "/data"
	},
	"next-to-last/2": func(k ds.Key) string {
		s := k.String()
		s, _ = strings.CutPrefix(s, "/")
		offset := 1
		start := len(s) - 2 - offset
		return s[start:start+2] + "/" + s
	},
}

func printCid(k ds.Key) string {
	h, _ := dshelp.DsKeyToMultihash(k)
	return fmt.Sprintf("key: %s (DagProtobuf: %s | Raw: %s)", k.String(), cid.NewCidV1(cid.DagProtobuf, h), cid.NewCidV1(cid.Raw, h))
}

func NewS3Datastore(conf Config) (*S3Bucket, error) {
	logConfig := conf
	if logConfig.AccessKey != "" {
		logConfig.AccessKey = ""
	}
	if logConfig.SecretKey != "" {
		logConfig.SecretKey = ""
	}
	if logConfig.SessionToken != "" {
		logConfig.SessionToken = ""
	}
	log.Infof("creating new S3 datastore with config: %+v", logConfig)

	if conf.Workers == 0 {
		conf.Workers = defaultWorkers
	}

	awsConfig := aws.NewConfig()
	sess, err := session.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create new session: %s", err)
	}

	d := defaults.Get()
	providers := []credentials.Provider{
		&credentials.StaticProvider{Value: credentials.Value{
			AccessKeyID:     conf.AccessKey,
			SecretAccessKey: conf.SecretKey,
			SessionToken:    conf.SessionToken,
		}},
		&credentials.EnvProvider{},
		&credentials.SharedCredentialsProvider{},
		&ec2rolecreds.EC2RoleProvider{Client: ec2metadata.New(sess)},
		endpointcreds.NewProviderClient(*d.Config, d.Handlers, conf.CredentialsEndpoint,
			func(p *endpointcreds.Provider) { p.ExpiryWindow = credsRefreshWindow },
		),
	}

	if len(os.Getenv("AWS_ROLE_ARN")) > 0 && len(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")) > 0 {
		stsClient := sts.New(sess)
		stsProvider := stscreds.NewWebIdentityRoleProviderWithOptions(stsClient, os.Getenv("AWS_ROLE_ARN"), "", stscreds.FetchTokenPath(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")))
		// prepend sts provider to list of providers
		providers = append([]credentials.Provider{stsProvider}, providers...)
	}

	creds := credentials.NewChainCredentials(providers)

	if conf.RegionEndpoint != "" {
		awsConfig.WithS3ForcePathStyle(true)
		awsConfig.WithEndpoint(conf.RegionEndpoint)
	}

	awsConfig.WithCredentials(creds)
	awsConfig.CredentialsChainVerboseErrors = aws.Bool(true)
	awsConfig.WithRegion(conf.Region)

	sess, err = session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create new session with aws config: %s", err)
	}
	s3obj := s3.New(sess)

	var cache filecache.FileCache
	if conf.CacheDirectory != "" {
		cacheImpl := filecache.NewDefaultCache(conf.CacheDirectory, nil, filecache.DefaultCacheComparer)
		cacheImpl.MaxItems = 262144
		cacheImpl.MaxSize = conf.CacheCapacity
		if cacheImpl.MaxSize <= 0 {
			info, err := disk.GetInfo(conf.CacheDirectory, false)
			if err == nil {
				cacheImpl.MaxSize = int64(float64(info.Total) * 0.8)
				log.Infof("[go-ds-s3] cache capacity is automatically set to %.2f GB", float64(cacheImpl.MaxSize)/filecache.Gigabyte)
			} else {
				cacheImpl.MaxSize = filecache.Gigabyte
				log.Infof("[go-ds-s3] could not get disk info for cache directory(%s): %+v", conf.CacheDirectory, err)
			}
		}
		cache = cacheImpl
	} else {
		cache = filecache.NewNoop()
	}
	if err = cache.Start(); err != nil {
		log.Infof("[go-ds-s3] cache(%s) failed to start: %+v", conf.CacheDirectory, err)
		cache = filecache.NewNoop()
	}

	return &S3Bucket{
		S3:     s3obj,
		Config: conf,
		Cache:  cache,
	}, nil
}

func (s *S3Bucket) Put(ctx context.Context, k ds.Key, value []byte) error {
	log.Debugf("put: %s", k)

	key := prepareKey(s.Config, k)

	_, err := s.S3.PutObjectWithContext(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.s3Path(key)),
		Body:   bytes.NewReader(value),
	})
	if err == nil {
		s.writeToCache(key, value)
	}
	if err != nil {
		log.Errorf("put error on key %s: %v", k, err)
	}
	return err
}

func (s *S3Bucket) Sync(ctx context.Context, prefix ds.Key) error {
	return nil
}

func (s *S3Bucket) Get(ctx context.Context, k ds.Key) ([]byte, error) {
	var err error
	var resp *s3.GetObjectOutput
	keys := prepareKeyWithFallback(s.Config, k)

	log.Debugf("get: %s", k)

	cachedFile, cerr := s.Cache.Open(keys[0])
	if cerr == nil {
		body, cerr := io.ReadAll(cachedFile)
		if cerr == nil {
			return body, nil
		}
	}

	var body []byte
	for index, key := range keys {
		resp, err = s.S3.GetObjectWithContext(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.Bucket),
			Key:    aws.String(s.s3Path(key)),
		})

		if err == nil {
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		if index == 1 && err == nil {
			_, _ = s.S3.PutObjectWithContext(ctx, &s3.PutObjectInput{
				Bucket: aws.String(s.Bucket),
				Key:    aws.String(s.s3Path(prepareKey(s.Config, k))),
				Body:   bytes.NewReader(body),
			})
		}

		if err == nil || !isNotFound(err) {
			break
		}
	}
	if err != nil {
		if isNotFound(err) {
			log.Debugf("get: %s not found", k)
			return nil, ds.ErrNotFound
		}
		log.Errorf("get error on key %s: %v", k, err)
		return nil, err
	}

	s.writeToCache(keys[0], body)

	return body, nil
}

func (s *S3Bucket) Has(ctx context.Context, k ds.Key) (exists bool, err error) {
	log.Debugf("has: %s", k)
	_, err = s.GetSize(ctx, k)
	if err != nil {
		if errors.Is(err, ds.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *S3Bucket) GetSize(ctx context.Context, k ds.Key) (size int, err error) {
	log.Debugf("get size: %s", k)

	var resp *s3.HeadObjectOutput
	keys := prepareKeyWithFallback(s.Config, k)

	for _, key := range keys {
		resp, err = s.S3.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.Bucket),
			Key:    aws.String(s.s3Path(key)),
		})
		if err == nil || !isNotFound(err) {
			break
		}
	}
	if err != nil {
		if isNotFound(err) {
			return -1, ds.ErrNotFound
		}
		log.Errorf("get size error on key %s: %v", k, err)
		return -1, err
	}
	return int(aws.Int64Value(resp.ContentLength)), nil
}

func (s *S3Bucket) Delete(ctx context.Context, k ds.Key) error {
	log.Debugf("delete: %s", k)

	var err error
	keys := prepareKeyWithFallback(s.Config, k)

	s.Cache.Remove(keys[0])

	for _, key := range keys {
		_, err = s.S3.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.Bucket),
			Key:    aws.String(s.s3Path(key)),
		})
		if err == nil || !isNotFound(err) {
			break
		}
	}
	if isNotFound(err) {
		// delete is idempotent
		log.Debugf("delete: %s not found, idempotent", k)
		err = nil
	} else if err != nil {
		log.Errorf("delete error on key %s: %v", k, err)
	}
	return err
}

func (s *S3Bucket) writeToCache(k string, data []byte) {
	writer, err := s.Cache.Create(k)
	if err != nil {
		log.Infof("cache create failed: %+v", err)
		return
	}
	_, err = writer.Write(data)
	if err != nil {
		log.Infof("cache write failed: %+v", err)
		writer.Cancel()
	} else {
		writer.Close()
	}
}

func prepareKey(cfg Config, k ds.Key) string {
	return KeyTransforms[cfg.KeyTransform](k)
}

func prepareKeyWithFallback(cfg Config, k ds.Key) []string {
	keys := []string{KeyTransforms[cfg.KeyTransform](k)}
	if cfg.KeyTransform != "default" {
		keys = append(keys, KeyTransforms["default"](k))
	}
	return keys
}

func (s *S3Bucket) Query(ctx context.Context, q dsq.Query) (dsq.Results, error) {
	log.Debugf("query: %+v", q)
	if q.Orders != nil || q.Filters != nil {
		err := fmt.Errorf("s3ds: filters or orders are not supported")
		log.Error(err)
		return nil, err
	}

	// S3 store a "/foo" key as "foo" so we need to trim the leading "/"
	prefix := strings.TrimPrefix(q.Prefix, "/")

	listInput := &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.Bucket),
		Prefix:  aws.String(s.s3Path(prefix)),
		MaxKeys: aws.Int64(listMax),
	}

	// The iterator needs to be stateful across Next() calls.
	// The closure will capture these state variables.
	var (
		resp    *s3.ListObjectsV2Output
		err     error
		index   = 0
		started = false
		skipped = 0
		yielded = 0
	)

	nextValue := func() (dsq.Result, bool) {
		// Initial fetch on first call
		if !started {
			log.Debugf("query: initial list call for prefix %s", q.Prefix)
			resp, err = s.S3.ListObjectsV2WithContext(ctx, listInput)
			if err != nil {
				log.Errorf("query: list objects error: %v", err)
				return dsq.Result{Error: err}, false
			}
			started = true
		}

		for {
			// Have we yielded enough results according to limit?
			if q.Limit > 0 && yielded >= q.Limit {
				return dsq.Result{}, false
			}

			// Do we need to fetch the next page of results?
			for index >= len(resp.Contents) {
				if !aws.BoolValue(resp.IsTruncated) {
					log.Debug("query: end of results")
					return dsq.Result{}, false
				}

				index = 0
				listInput.ContinuationToken = resp.NextContinuationToken
				log.Debugf("query: fetching next page with token %s", *resp.NextContinuationToken)
				resp, err = s.S3.ListObjectsV2WithContext(ctx, listInput)
				if err != nil {
					log.Errorf("query: list objects error on next page: %v", err)
					return dsq.Result{Error: err}, false
				}
			}

			// Have we skipped enough results according to offset?
			if skipped < q.Offset {
				skipped++
				index++
				continue
			}

			// If we are here, we have an entry to return.
			keyFromS3 := aws.StringValue(resp.Contents[index].Key)
			dsKeyPath := strings.TrimPrefix(keyFromS3, s.RootDirectory)
			dsKeyPath = strings.TrimPrefix(dsKeyPath, "/")

			entry := dsq.Entry{
				Key:  ds.NewKey(dsKeyPath).String(),
				Size: int(aws.Int64Value(resp.Contents[index].Size)),
			}
			if !q.KeysOnly {
				value, getErr := s.Get(ctx, ds.NewKey(entry.Key))
				if getErr != nil {
					return dsq.Result{Error: getErr}, false
				}
				entry.Value = value
			}

			index++
			yielded++
			return dsq.Result{Entry: entry}, true
		}
	}

	// We are handling offset and limit in our iterator.
	// We create a new query object for ResultsFromIterator to avoid it also
	// trying to apply them.
	cleanQuery := q
	cleanQuery.Offset = 0
	cleanQuery.Limit = 0

	return dsq.ResultsFromIterator(cleanQuery, dsq.Iterator{
		Close: func() error { return nil },
		Next:  nextValue,
	}), nil
}

func (s *S3Bucket) Batch(_ context.Context) (ds.Batch, error) {
	log.Debug("starting batch")
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
	return path.Join(s.RootDirectory, strings.TrimPrefix(p, "/"))
}

func isNotFound(err error) bool {
	var s3Err awserr.Error
	ok := errors.As(err, &s3Err)
	return ok && (s3Err.Code() == s3.ErrCodeNoSuchKey || s3Err.Code() == "NotFound")
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

func (b *s3Batch) Put(ctx context.Context, k ds.Key, val []byte) error {
	log.Debugf("batch put: %s", k)
	b.ops[k.String()] = batchOp{
		val:    val,
		delete: false,
	}
	return nil
}

func (b *s3Batch) Delete(ctx context.Context, k ds.Key) error {
	log.Debugf("batch delete: %s", k)
	b.ops[k.String()] = batchOp{
		val:    nil,
		delete: true,
	}
	return nil
}

func (b *s3Batch) Commit(ctx context.Context) error {
	log.Debugf("committing batch with %d operations", len(b.ops))
	var (
		deleteObjs []*s3.ObjectIdentifier
		putKeys    []ds.Key
	)
	for k, op := range b.ops {
		if op.delete {
			deleteObjs = append(deleteObjs, &s3.ObjectIdentifier{
				Key: aws.String(b.s.s3Path(k)),
			})
		} else {
			putKeys = append(putKeys, ds.NewKey(k))
		}
	}

	log.Debugf("batch commit: %d puts, %d deletes", len(putKeys), len(deleteObjs))

	numJobs := len(putKeys) + (len(deleteObjs) / deleteMax)
	if len(deleteObjs)%deleteMax > 0 {
		numJobs++
	}
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
		jobs <- b.newPutJob(ctx, k, b.ops[k.String()].val)
	}

	if len(deleteObjs) > 0 {
		for i := 0; i < len(deleteObjs); i += deleteMax {
			limit := deleteMax
			if len(deleteObjs[i:]) < limit {
				limit = len(deleteObjs[i:])
			}

			jobs <- b.newDeleteJob(ctx, deleteObjs[i:i+limit])
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
		err := fmt.Errorf("s3ds: failed batch operation:\n%s", strings.Join(errs, "\n"))
		log.Error(err)
		return err
	}

	log.Debug("batch commit successful")
	return nil
}

func (b *s3Batch) newPutJob(ctx context.Context, k ds.Key, value []byte) func() error {
	return func() error {
		return b.s.Put(ctx, k, value)
	}
}

func (b *s3Batch) newDeleteJob(ctx context.Context, objs []*s3.ObjectIdentifier) func() error {
	return func() error {
		log.Debugf("batch worker: deleting %d objects", len(objs))
		resp, err := b.s.S3.DeleteObjectsWithContext(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(b.s.Bucket),
			Delete: &s3.Delete{
				Objects: objs,
			},
		})
		if err != nil && !isNotFound(err) {
			log.Errorf("batch worker: error deleting objects: %v", err)
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
			err := fmt.Errorf("failed to delete objects: %s", errs)
			log.Error(err)
			return err
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
