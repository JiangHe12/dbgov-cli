package dbgovctx

import (
	"context"
	"os"
	"path/filepath"

	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/credstore"
	corectx "github.com/JiangHe12/opskit-core/ctx"
	"gopkg.in/yaml.v3"
)

const (
	SupportedContextAPIVersion = "dbgov-cli.io/context/v1"
	legacyContextAPIVersion    = "dbgov.io/context/v1"
)

type Context struct {
	corectx.Base `yaml:",inline"`

	Engine   string `yaml:"engine"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database,omitempty"`
}

var (
	configPathOverride string
	store              = corectx.NewStore(func(c *Context) *corectx.Base { return &c.Base })
)

type Config = corectx.Config[Context]

func SetConfigPath(path string) {
	configPathOverride = path
	corectx.SetConfigPath(path)
}

func Load() (*Config, error) {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return nil, err
	}
	return store.Load()
}

func Save(cfg *Config) error {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return err
	}
	return store.Save(cfg)
}

func SetContext(name string, ctx Context) error {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return err
	}
	return store.SetContext(name, ctx)
}

func UseContext(name string) error {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return err
	}
	return store.UseContext(name)
}

func Current() (*Context, string, error) {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return nil, "", err
	}
	return store.Current()
}

func DeleteContext(name string) error {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return err
	}
	return store.DeleteContext(name)
}

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

func migrateLegacyContextAPIVersion() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path) //nolint:gosec // Path is the configured dbgov context file path.
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to read context file", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil //nolint:nilerr // Let the core context loader report malformed config consistently.
	}
	if cfg.APIVersion != legacyContextAPIVersion {
		return nil
	}
	cfg.APIVersion = SupportedContextAPIVersion
	updated, err := yaml.Marshal(&cfg)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to migrate context apiVersion", err)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, updated, mode); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to write migrated context file", err)
	}
	return nil
}

func configPath() (string, error) {
	if configPathOverride != "" {
		return configPathOverride, nil
	}
	dir, err := corectx.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}
