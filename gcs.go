package gcss

import (
	"context"
	"fmt"
	"io/ioutil"
	"path"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
	"google.golang.org/api/option"
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

var _ ds.Datastore = (*GcsBucket)(nil)

type GcsBucket struct {
	Config
	Client *storage.Client
}

type Config struct {
	Bucket              string
	RootDirectory       string
	CredentialsFilePath string
	Workers             int
}

func NewGcsDatastore(conf Config) (*GcsBucket, error) {
	if conf.Workers == 0 {
		conf.Workers = defaultWorkers
	}

	client, err := storage.NewClient(context.Background(), option.WithCredentialsFile(conf.CredentialsFilePath))
	if err != nil {
		return nil, fmt.Errorf("Failed to create new gcs client: %s", err)
	}

	return &GcsBucket{
		Client: client,
		Config: conf,
	}, nil
}

func (s *GcsBucket) Put(ctx context.Context, k ds.Key, value []byte) error {
	// Upload an object with storage.Writer.
	wc := s.Client.Bucket(s.Config.Bucket).Object(s.gcsPath(k.String())).NewWriter(ctx)
	wc.ChunkSize = 0 // note retries are not supported for chunk size 0.

	if _, err := wc.Write(value); err != nil {
		return fmt.Errorf("Writer.Write: %v", err)
	}

	// Data can continue to be added to the file until the writer is closed.
	if err := wc.Close(); err != nil {
		return fmt.Errorf("Writer.Close: %v", err)
	}

	return nil
}

func (s *GcsBucket) Sync(ctx context.Context, prefix ds.Key) error {
	return nil
}

func (s *GcsBucket) Get(ctx context.Context, k ds.Key) ([]byte, error) {
	rc, err := s.Client.Bucket(s.Config.Bucket).Object(s.gcsPath(k.String())).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("Object(%q).NewReader: %v", k.String(), err)
	}

	defer rc.Close()

	return ioutil.ReadAll(rc)
}

func (s *GcsBucket) Has(ctx context.Context, k ds.Key) (exists bool, err error) {
	o := s.Client.Bucket(s.Config.Bucket).Object(s.gcsPath(k.String()))
	if _, err := o.Attrs(ctx); err != nil {
		return false, nil
	}

	return true, nil
}

func (s *GcsBucket) GetSize(ctx context.Context, k ds.Key) (size int, err error) {
	o := s.Client.Bucket(s.Config.Bucket).Object(s.gcsPath(k.String()))
	attrs, err := o.Attrs(ctx)
	if err != nil {
		return -1, ds.ErrNotFound
	}

	return int(attrs.Size), nil
}

func (s *GcsBucket) Delete(ctx context.Context, k ds.Key) error {
	o := s.Client.Bucket(s.Config.Bucket).Object(s.gcsPath(k.String()))
	// Optional: set a generation-match precondition to avoid potential race
	// conditions and data corruptions. The request to upload is aborted if the
	// object's generation number does not match your precondition.
	attrs, err := o.Attrs(ctx)
	if err != nil {
		return fmt.Errorf("object.Attrs: %v", err)
	}

	o = o.If(storage.Conditions{GenerationMatch: attrs.Generation})
	if err := o.Delete(ctx); err != nil {
		return fmt.Errorf("Object(%q).Delete: %v", k.String(), err)
	}

	return nil
}

func (s *GcsBucket) Query(ctx context.Context, q dsq.Query) (dsq.Results, error) {
	return nil, fmt.Errorf("Todo: implement query for gcs datastore.")
}

func (s *GcsBucket) Batch(_ context.Context) (ds.Batch, error) {
	return &gcsBatch{
		s:          s,
		ops:        make(map[string]batchOp),
		numWorkers: s.Workers,
	}, nil
}

func (s *GcsBucket) Close() error {
	return nil
}

func (s *GcsBucket) gcsPath(p string) string {
	return path.Join(s.Config.RootDirectory, strings.TrimPrefix(p, "/"))
}

type gcsBatch struct {
	s          *GcsBucket
	ops        map[string]batchOp
	numWorkers int
}

type batchOp struct {
	val    []byte
	delete bool
}

func (b *gcsBatch) Put(ctx context.Context, k ds.Key, val []byte) error {
	b.ops[k.String()] = batchOp{
		val:    val,
		delete: false,
	}
	return nil
}

func (b *gcsBatch) Delete(ctx context.Context, k ds.Key) error {
	b.ops[k.String()] = batchOp{
		val:    nil,
		delete: true,
	}
	return nil
}

func (b *gcsBatch) Commit(ctx context.Context) error {
	var (
		deleteObjs []ds.Key
		putKeys    []ds.Key
	)
	for k, op := range b.ops {
		if op.delete {
			deleteObjs = append(deleteObjs, ds.NewKey(k))
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
		jobs <- b.newPutJob(ctx, k, b.ops[k.String()].val)
	}

	for _, k := range deleteObjs {
		jobs <- b.newDeleteJob(ctx, k)
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
		return fmt.Errorf("gcss: failed batch operation:\n%s", strings.Join(errs, "\n"))
	}

	return nil
}

func (b *gcsBatch) newPutJob(ctx context.Context, k ds.Key, value []byte) func() error {
	return func() error {
		return b.s.Put(ctx, k, value)
	}
}

func (b *gcsBatch) newDeleteJob(ctx context.Context, obj ds.Key) func() error {
	return func() error {
		err := b.s.Delete(ctx, obj)
		if err != nil {
			return fmt.Errorf("Failed to delete objects: %s", err)
		}

		return nil
	}
}

func worker(jobs <-chan func() error, results chan<- error) {
	for j := range jobs {
		results <- j()
	}
}

var _ ds.Batching = (*GcsBucket)(nil)
