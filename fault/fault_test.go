package fault_test

import (
	"errors"
	"testing"

	"github.com/jrgf/go-vial/fault"
)

func TestFaultRetainsApplicationDetailsAndCause(t *testing.T) {
	cause := errors.New("database unavailable")
	err := fault.Wrap(fault.Unavailable, "users_unavailable", "Users are unavailable", cause)
	err.Fields = map[string]string{"user_id": "required"}
	err.Meta = map[string]any{"retry": true}

	if !errors.Is(err, cause) || err.Error() != "Users are unavailable: database unavailable" {
		t.Fatalf("fault did not retain its cause: %v", err)
	}
	if err.Fields["user_id"] != "required" || err.Meta["retry"] != true {
		t.Fatalf("fault did not retain details: %#v", err)
	}
	if (*fault.Error)(nil).Error() != "<nil>" || (*fault.Error)(nil).Unwrap() != nil {
		t.Fatal("nil fault methods returned unexpected values")
	}
}

func TestFaultErrorFallbacks(t *testing.T) {
	if got := fault.New(fault.Conflict, "write_conflict", "").Error(); got != "write_conflict" {
		t.Fatalf("unexpected code fallback %q", got)
	}
	if got := fault.New(fault.Internal, "", "").Error(); got != "application fault" {
		t.Fatalf("unexpected generic fallback %q", got)
	}
}
