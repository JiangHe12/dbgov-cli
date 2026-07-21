package cmd

import (
	"fmt"
	"strconv"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

func errNoContext() error {
	return apperrors.New(apperrors.CodeUsageError, "no database context configured", nil)
}

func errUnsupportedEngine(engine string) error {
	return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("unsupported engine %q", engine), nil)
}

func itoa(v int) string { return strconv.Itoa(v) }
