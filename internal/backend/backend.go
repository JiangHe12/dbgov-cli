package backend

import (
	"context"

	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend interface {
	Ping(ctx context.Context) error
	IntrospectSchema(ctx context.Context) (schema.Schema, error)
}
