package s3ds

import (
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
	s3 "github.com/rlmcpherson/s3gof3r"
)

type S3Bucket struct {
	s3c  *s3.S3
	buck *s3.Bucket

	opcfg *s3.Config
}

type Config struct {
	Domain    string
	AccessKey string
	SecretKey string
	Bucket    string
}

func NewS3Datastore(cfg *Config) *S3Bucket {
	keys := s3.Keys{
		AccessKey: cfg.AccessKey,
		SecretKey: cfg.SecretKey,
	}
	c := s3.New(cfg.Domain, keys)
	buck := c.Bucket(cfg.Bucket)
	return &S3Bucket{
		s3c:  c,
		buck: buck,
		opcfg: &s3.Config{
			Concurrency: 1,
			PartSize:    s3.DefaultConfig.PartSize,
			NTry:        10,
			Md5Check:    true,
			Scheme:      "https",
			Client:      s3.ClientWithTimeout(5 * time.Second),
		},
	}
}

func (s *S3Bucket) Get(k ds.Key) (interface{}, error) {
	r, _, err := s.buck.GetReader(k.String(), s.opcfg)
	switch err := err.(type) {
	case *s3.RespError:
		if err.StatusCode == 404 {
			return nil, ds.ErrNotFound
		}
		return nil, err
	default:
		return nil, err
	case nil:
		// continue
	}
	defer r.Close()

	out, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func (s *S3Bucket) Delete(k ds.Key) error {
	return s.buck.Delete(k.String())
}

func (s *S3Bucket) Put(k ds.Key, val interface{}) error {
	valb, ok := val.([]byte)
	if !ok {
		return fmt.Errorf("value being put was not a []byte")
	}

	w, err := s.buck.PutWriter(k.String(), nil, s.opcfg)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = w.Write(valb)
	if err != nil {
		return err
	}

	return nil
}

func (s *S3Bucket) Has(k ds.Key) (bool, error) {
	url := fmt.Sprintf("https://%s.%s%s", s.buck.Name, s.buck.Domain, k.String())
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("If-Modified-Since", time.Now().Format(time.RFC1123))

	s.buck.Sign(req)
	resp, err := s.buck.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	io.Copy(ioutil.Discard, resp.Body)

	switch resp.StatusCode {
	case 200, 304:
		return true, nil
	case 404:
		return false, nil
	default:
		return false, fmt.Errorf(resp.Status)
	}
}

func (s *S3Bucket) Query(q dsq.Query) (dsq.Results, error) {
	if !q.KeysOnly {
		return nil, fmt.Errorf("s3 datastore doesnt support returning values in query, only keys")
	}

	if q.Prefix != "" {
		return nil, fmt.Errorf("s3ds doesnt support prefixes")
	}

	if q.Orders != nil || q.Filters != nil {
		return nil, fmt.Errorf("s3ds doesnt support filters or orders")
	}

	req, err := http.NewRequest("GET", "https://"+s.buck.Name+"."+s.buck.Domain, nil)
	if err != nil {
		return nil, err
	}

	s.buck.Sign(req)

	resp, err := s.buck.Do(req)
	if err != nil {
		return nil, err
	}

	dec := xml.NewDecoder(resp.Body)
	var nextiskey bool

	nextValue := func() (dsq.Result, bool) {
		for {
			tok, err := dec.Token()
			if err != nil {
				if err == io.EOF {
					return dsq.Result{}, false
				}
				return dsq.Result{Error: err}, false
			}

			switch tok := tok.(type) {
			case xml.StartElement:
				if tok.Name.Local == "Key" {
					nextiskey = true
				}
			case xml.CharData:
				if nextiskey {
					kval := string(tok)
					nextiskey = false
					dec.Skip()

					if strings.HasPrefix(kval, ".md5/") {
						continue
					}
					res := dsq.Result{Entry: dsq.Entry{Key: kval}}
					return res, true
				}
			}
		}
	}

	return dsq.ResultsFromIterator(q, dsq.Iterator{
		Close: func() error {
			return resp.Body.Close()
		},
		Next: nextValue,
	}), nil
}

type s3Batch struct {
	s *S3Bucket
}

func (s *S3Bucket) Batch() (ds.Batch, error) {
	return &s3Batch{s}, nil
}

func (b *s3Batch) Put(k ds.Key, val interface{}) error {
	return b.s.Put(k, val)
}

func (b *s3Batch) Delete(k ds.Key) error {
	return b.s.Delete(k)
}

func (b *s3Batch) Commit() error {
	return nil
}

var _ ds.Batching = (*S3Bucket)(nil)
