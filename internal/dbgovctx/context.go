package dbgovctx

import (
	"context"
	"os"

	"github.com/JiangHe12/opskit-core/credstore"
	corectx "github.com/JiangHe12/opskit-core/ctx"
)

type Context struct {
	corectx.Base `yaml:",inline"`

	Engine   string `yaml:"engine"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database,omitempty"`
}

var store = corectx.NewStore(func(c *Context) *corectx.Base { return &c.Base })

type Config = corectx.Config[Context]

func SetConfigPath(path string) { corectx.SetConfigPath(path) }

func Load() (*Config, error) { return store.Load() }

func Save(cfg *Config) error { return store.Save(cfg) }

func SetContext(name string, ctx Context) error { return store.SetContext(name, ctx) }

func UseContext(name string) error { return store.UseContext(name) }

func Current() (*Context, string, error) { return store.Current() }

func DeleteContext(name string) error { return store.DeleteContext(name) }

func (c Context) ResolvePasswordContext(ctx context.Context, contextName string) (string, error) {
	if c.Password == "" {
		if password := os.Getenv("DBGOV_PASSWORD"); password != "" {
			return password, nil
		}
	}
	return c.Base.ResolvePasswordContext(ctx, contextName)
}

func StoreCredential(ctx context.Context, name, backendName, password string, item Context) (Context, error) {
	if password == "" {
		item.CredentialBackend = backendName
		return item, nil
	}
	if backendName == "" || backendName == "plain-yaml" {
		item.Password = password
		item.CredentialBackend = backendName
		return item, nil
	}
	backend, err := credstore.New(backendName)
	if err != nil {
		return item, err
	}
	if err := backend.Available(); err != nil {
		return item, err
	}
	if err := backend.Put(ctx, name, password); err != nil {
		return item, err
	}
	item.Password = credstore.EncodeRef(backendName)
	item.CredentialBackend = backendName
	return item, nil
}
