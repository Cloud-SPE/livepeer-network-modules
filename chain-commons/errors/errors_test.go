package errors

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorClass_String(t *testing.T) {
	tests := []struct {
		class ErrorClass
		want  string
	}{
		{ClassUnknown, "unknown"},
		{ClassTransient, "transient"},
		{ClassPermanent, "permanent"},
		{ClassReverted, "reverted"},
		{ClassNoncePast, "nonce_past"},
		{ClassInsufficientFunds, "insufficient_funds"},
		{ClassReorged, "reorged"},
		{ClassCircuitOpen, "circuit_open"},
		{ErrorClass(99), "class(99)"},
	}
	for _, tt := range tests {
		if got := tt.class.String(); got != tt.want {
			t.Errorf("ErrorClass(%d).String() = %q, want %q", tt.class, got, tt.want)
		}
	}
}

func TestError_ErrorAndUnwrap(t *testing.T) {
	cause := fmt.Errorf("underlying boom")
	e := Wrap(ClassTransient, "rpc.boom", "thing went boom", cause)
	if got := e.Error(); got != "rpc.boom: thing went boom: underlying boom" {
		t.Errorf("Error() = %q", got)
	}
	if got := errors.Unwrap(e); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestError_NoCause(t *testing.T) {
	e := New(ClassPermanent, "tx.bad", "bad call")
	if got := e.Error(); got != "tx.bad: bad call" {
		t.Errorf("Error() = %q", got)
	}
	if got := errors.Unwrap(e); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}

func TestError_Is(t *testing.T) {
	e1 := New(ClassTransient, "rpc.timeout", "timed out")
	e2 := New(ClassPermanent, "rpc.timeout", "different msg") // same Code
	e3 := New(ClassTransient, "rpc.other", "different code")

	if !errors.Is(e1, e2) {
		t.Errorf("errors.Is should match by Code")
	}
	if errors.Is(e1, e3) {
		t.Errorf("errors.Is should not match different Codes")
	}
	if errors.Is(e1, fmt.Errorf("plain")) {
		t.Errorf("errors.Is should not match non-*Error targets")
	}
}

func TestClassify_NilReturnsNil(t *testing.T) {
	if got := Classify(nil); got != nil {
		t.Errorf("Classify(nil) = %v, want nil", got)
	}
}

func TestClassify_AlreadyClassified(t *testing.T) {
	original := New(ClassReverted, "tx.reverted", "reverted")
	classified := Classify(original)
	if classified != original {
		t.Errorf("Classify of *Error should return same pointer")
	}
}

func TestClassify_Patterns(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantClass ErrorClass
		wantCode  string
	}{
		{"nonce too low", fmt.Errorf("nonce too low"), ClassNoncePast, "tx.nonce_past"},
		{"insufficient funds", fmt.Errorf("insufficient funds for gas * price + value"), ClassInsufficientFunds, "tx.insufficient_funds"},
		{"reverted", fmt.Errorf("execution reverted: not eligible"), ClassReverted, "tx.reverted"},
		{"invalid argument", fmt.Errorf("invalid argument 0: bad calldata"), ClassPermanent, "rpc.invalid_argument"},
		{"EOF", fmt.Errorf("EOF"), ClassTransient, "rpc.connection_error"},
		{"connection reset", fmt.Errorf("read tcp: connection reset by peer"), ClassTransient, "rpc.connection_error"},
		{"deadline exceeded", fmt.Errorf("context deadline exceeded"), ClassTransient, "rpc.timeout"},
		{"rate limit", fmt.Errorf("HTTP 429: too many requests"), ClassTransient, "rpc.rate_limited"},
		{"block not synced", fmt.Errorf("unsupported block number 1234567"), ClassTransient, "rpc.block_not_synced"},
		{"unknown", fmt.Errorf("something weird"), ClassTransient, "rpc.unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Classify(tt.err)
			if c == nil {
				t.Fatalf("Classify returned nil for non-nil err")
			}
			if c.Class != tt.wantClass {
				t.Errorf("Class = %v, want %v", c.Class, tt.wantClass)
			}
			if c.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", c.Code, tt.wantCode)
			}
			if c.Cause != tt.err {
				t.Errorf("Cause should preserve original error")
			}
		})
	}
}

func TestIsTransient(t *testing.T) {
	if IsTransient(nil) {
		t.Errorf("IsTransient(nil) should be false")
	}
	if !IsTransient(fmt.Errorf("EOF")) {
		t.Errorf("EOF should be transient")
	}
	if IsTransient(fmt.Errorf("execution reverted")) {
		t.Errorf("revert should not be transient")
	}
}

func TestIsPermanent(t *testing.T) {
	if IsPermanent(nil) {
		t.Errorf("IsPermanent(nil) should be false")
	}
	if !IsPermanent(fmt.Errorf("execution reverted")) {
		t.Errorf("revert should be permanent")
	}
	if !IsPermanent(fmt.Errorf("nonce too low")) {
		t.Errorf("nonce-past should be permanent")
	}
	if !IsPermanent(fmt.Errorf("insufficient funds")) {
		t.Errorf("insufficient-funds should be permanent")
	}
	if !IsPermanent(fmt.Errorf("invalid argument")) {
		t.Errorf("invalid-argument should be permanent")
	}
	if IsPermanent(fmt.Errorf("EOF")) {
		t.Errorf("EOF should not be permanent")
	}
}
