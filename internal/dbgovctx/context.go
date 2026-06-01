package dbgovctx

import corectx "github.com/JiangHe12/opskit-core/ctx"

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
