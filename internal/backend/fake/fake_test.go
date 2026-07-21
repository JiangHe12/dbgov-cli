package fake

import (
	"context"
	"testing"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

func TestExecDDLFailureIsBackendError(t *testing.T) {
	backend := New()
	backend.FailAt = 1
	_, err := backend.ExecDDL(context.Background(), []string{"CREATE TABLE users (id BIGINT)"})
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeBackendError {
		t.Fatalf("error code = %s, want %s; err = %v", got, apperrors.CodeBackendError, err)
	}
}
