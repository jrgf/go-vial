package asyncpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

var (
	_ vial.AsyncLifecycleExecutor = (*Executor)(nil)
	_ vial.AsyncWaiter            = (*Executor)(nil)
	_ vial.AsyncMetricsProvider   = (*Executor)(nil)
)

type testResult int64

func (testResult) LastInsertId() (int64, error) { return 0, nil }
func (result testResult) RowsAffected() (int64, error) {
	return int64(result), nil
}

func TestRetryPolicyAndBackoff(t *testing.T) {
	attempts, initial, maximum, err := normalizeRetry(vial.RetryPolicy{MaxAttempts: 4})
	if err != nil || attempts != 4 || initial != time.Second || maximum != time.Minute {
		t.Fatalf("normalized retry = %d %s %s, error %v", attempts, initial, maximum, err)
	}
	if got := retryBackoff(1, time.Second, 10*time.Second); got != time.Second {
		t.Fatalf("first backoff = %s", got)
	}
	if got := retryBackoff(5, time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("capped backoff = %s", got)
	}
	if _, _, _, err := normalizeRetry(vial.RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Minute, MaxBackoff: time.Second}); !errors.Is(err, vial.ErrInvalidOperation) {
		t.Fatalf("invalid retry error = %v", err)
	}
}

func TestSchemaAndOwnedUpdates(t *testing.T) {
	for _, required := range []string{"lease_expires_at", "idempotency_scope", "retrying"} {
		if !strings.Contains(schemaSQL, required) {
			t.Fatalf("schema is missing %s", required)
		}
	}
	if err := ownedUpdate(testResult(0), nil); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("zero-row update error = %v", err)
	}
	if err := ownedUpdate(testResult(1), nil); err != nil {
		t.Fatalf("owned update: %v", err)
	}
}
