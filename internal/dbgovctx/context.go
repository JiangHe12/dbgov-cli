package dbgovctx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/credstore"
	corectx "github.com/JiangHe12/opskit-core/v2/ctx"
	"github.com/JiangHe12/opskit-core/v2/lockfile"
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

// ConfigPath returns the effective context configuration path.
func ConfigPath() (string, error) {
	return configPath()
}

func Load() (*Config, error) {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return nil, err
	}
	return store.Load()
}

// LoadReadOnly loads context policy without rewriting a legacy apiVersion.
// Control-plane previews and pre-change authorization use this path so merely
// inspecting the governing policy cannot mutate the config file.
func LoadReadOnly() (*Config, error) {
	cfg, err := store.Load()
	if err == nil {
		return cfg, nil
	}
	if apperrors.AsAppError(err).Code != apperrors.CodeUnsupportedProtocol {
		return nil, err
	}
	path, pathErr := configPath()
	if pathErr != nil {
		return nil, pathErr
	}
	data, readErr := os.ReadFile(path) //nolint:gosec // Path is the configured dbgov context file path.
	if readErr != nil {
		return nil, apperrors.New(apperrors.CodeLocalIOError, "failed to read context file", readErr)
	}
	var legacy Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if decodeErr := decoder.Decode(&legacy); decodeErr != nil {
		return nil, apperrors.New(apperrors.CodeLocalIOError, "failed to parse context file", decodeErr)
	}
	if legacy.APIVersion != legacyContextAPIVersion {
		return nil, err
	}
	if legacy.Contexts == nil {
		legacy.Contexts = make(map[string]Context)
	}
	for name, item := range legacy.Contexts {
		ref := credstore.ParseRef(item.Password)
		if ref.IsRef && ref.BackendName == "" {
			return nil, apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q has empty credential store reference", name), nil)
		}
		if item.OTLPEndpointSource == "" {
			item.OTLPEndpointSource = "auto"
		}
		if item.OTLPMetricsSource == "" {
			item.OTLPMetricsSource = "auto"
		}
		legacy.Contexts[name] = item
	}
	legacy.APIVersion = SupportedContextAPIVersion
	return &legacy, nil
}

// WithLockedRead reloads the context config while holding the same lock used
// by context updates, and keeps that policy immutable for the callback.
func WithLockedRead(fn func(*Config) error) error {
	if fn == nil {
		return apperrors.New(apperrors.CodeValidationFailed, "locked context callback is required", nil)
	}
	dir, err := corectx.ConfigDir()
	if err != nil {
		return err
	}
	lock := lockfile.New(filepath.Join(dir, "config"))
	if err := lock.Acquire(); err != nil {
		return err
	}
	cfg, err := LoadReadOnly()
	if err != nil {
		_ = lock.Release()
		return err
	}
	callbackErr := fn(cfg)
	releaseErr := lock.Release()
	if callbackErr != nil {
		return callbackErr
	}
	if releaseErr != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to release context policy lock", releaseErr)
	}
	return nil
}

func Save(cfg *Config) error {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return err
	}
	return store.Save(cfg)
}

// Update applies a locked read-modify-write operation to the context config.
func Update(fn func(cfg *Config) error) error {
	if err := migrateLegacyContextAPIVersion(); err != nil {
		return err
	}
	return store.Update(fn)
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
