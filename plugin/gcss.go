package plugin

import (
	"fmt"

	"github.com/ipfs/go-ipfs/plugin"
	"github.com/ipfs/go-ipfs/repo"
	"github.com/ipfs/go-ipfs/repo/fsrepo"
	gcss "github.com/luanet/go-ds-gcs"
)

var Plugins = []plugin.Plugin{
	&GcsPlugin{},
}

type GcsPlugin struct{}

func (gsp GcsPlugin) Name() string {
	return "gcs-datastore-plugin"
}

func (gsp GcsPlugin) Version() string {
	return "0.0.1"
}

func (gsp GcsPlugin) Init(env *plugin.Environment) error {
	return nil
}

func (gsp GcsPlugin) DatastoreTypeName() string {
	return "gcs"
}

func (gsp GcsPlugin) DatastoreConfigParser() fsrepo.ConfigFromMap {
	return func(m map[string]interface{}) (fsrepo.DatastoreConfig, error) {
		bucket, ok := m["bucket"].(string)
		if !ok {
			return nil, fmt.Errorf("gcs: no bucket specified")
		}

		credentialsPath, ok := m["credentialsFilePath"].(string)
		if !ok {
			return nil, fmt.Errorf("gcs: no credentials path specified")
		}

		var workers int
		if v, ok := m["workers"]; ok {
			workersf, ok := v.(float64)
			workers = int(workersf)
			switch {
			case !ok:
				return nil, fmt.Errorf("gcs: workers not a number")
			case workers <= 0:
				return nil, fmt.Errorf("gcs: workers <= 0: %f", workersf)
			case float64(workers) != workersf:
				return nil, fmt.Errorf("gcs: workers is not an integer: %f", workersf)
			}
		}

		return &GcsConfig{
			cfg: gcss.Config{
				Bucket:              bucket,
				CredentialsFilePath: credentialsPath,
				Workers:             workers,
			},
		}, nil
	}
}

type GcsConfig struct {
	cfg gcss.Config
}

func (gcsc *GcsConfig) DiskSpec() fsrepo.DiskSpec {
	return fsrepo.DiskSpec{
		"bucket": gcsc.cfg.Bucket,
	}
}

func (gcsc *GcsConfig) Create(path string) (repo.Datastore, error) {
	return gcss.NewGcsDatastore(gcsc.cfg)
}
