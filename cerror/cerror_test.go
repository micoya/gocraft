package cerror_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/micoya/gocraft/cerror"
)

// --- error chain & wrapping ---

func TestWrap_ErrorChain(t *testing.T) {
	cause := errors.New("sql: no rows")
	err := cerror.Wrap(cerror.CodeNotFound, "user not found", cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is should find cause through Unwrap")
	}
	if err.Unwrap() != cause {
		t.Error("Unwrap() should return the direct cause")
	}
}

func TestWrapf_MessageFormat(t *testing.T) {
	cause := errors.New("timeout")
	err := cerror.Wrapf(cerror.CodeInternal, cause, "query user %d failed", 42)
	if err.Message() != "query user 42 failed" {
		t.Errorf("Message() = %q, want %q", err.Message(), "query user 42 failed")
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is should find cause")
	}
}

func TestError_StringContainsCause(t *testing.T) {
	cause := errors.New("conn refused")
	err := cerror.Wrap(cerror.CodeInternal, "db error", cause)
	s := err.Error()
	if s == "" {
		t.Fatal("Error() should not be empty")
	}
	// 含 code、message、cause
	for _, want := range []string{"500", "db error", "conn refused"} {
		if !contains(s, want) {
			t.Errorf("Error() = %q, expected to contain %q", s, want)
		}
	}
}

func TestError_NoCause(t *testing.T) {
	err := cerror.New(cerror.CodeNotFound, "not found")
	s := err.Error()
	if contains(s, ":") && contains(s, "nil") {
		t.Errorf("Error() should not mention nil cause: %q", s)
	}
}

// --- IsCode ---

func TestIsCode_Direct(t *testing.T) {
	err := cerror.New(cerror.CodeNotFound, "not found")
	if !cerror.IsCode(err, cerror.CodeNotFound) {
		t.Error("IsCode should return true for direct *Error")
	}
	if cerror.IsCode(err, cerror.CodeInternal) {
		t.Error("IsCode should return false for wrong code")
	}
}

func TestIsCode_ThroughWrap(t *testing.T) {
	inner := cerror.New(cerror.CodeNotFound, "not found")
	outer := fmt.Errorf("service layer: %w", inner)
	if !cerror.IsCode(outer, cerror.CodeNotFound) {
		t.Error("IsCode should traverse fmt.Errorf %w chain")
	}
}

func TestIsCode_MultiLevel(t *testing.T) {
	inner := cerror.New(cerror.CodeForbidden, "forbidden")
	mid := cerror.Wrap(cerror.CodeInternal, "wrapped", inner)
	outer := fmt.Errorf("top: %w", mid)

	// outer 的直接 *Error 是 CodeInternal，但链中也有 CodeForbidden
	if !cerror.IsCode(outer, cerror.CodeInternal) {
		t.Error("should find CodeInternal in chain")
	}
	// errors.As 找第一个 *Error（CodeInternal），所以 CodeForbidden 不会被 IsCode 找到
	if cerror.IsCode(outer, cerror.CodeForbidden) {
		t.Error("IsCode finds first *Error only, should not find inner CodeForbidden")
	}
}

func TestIsCode_PlainError(t *testing.T) {
	if cerror.IsCode(errors.New("plain"), cerror.CodeNotFound) {
		t.Error("IsCode should return false for plain error")
	}
	if cerror.IsCode(nil, cerror.CodeNotFound) {
		t.Error("IsCode should return false for nil")
	}
}

// --- FromError ---

func TestFromError_Found(t *testing.T) {
	original := cerror.New(cerror.CodeConflict, "duplicate")
	wrapped := fmt.Errorf("repo: %w", original)

	ce, ok := cerror.FromError(wrapped)
	if !ok {
		t.Fatal("FromError should find *Error in chain")
	}
	if ce.Code() != cerror.CodeConflict {
		t.Errorf("Code() = %v, want %v", ce.Code(), cerror.CodeConflict)
	}
}

func TestFromError_NotFound(t *testing.T) {
	_, ok := cerror.FromError(errors.New("plain"))
	if ok {
		t.Error("FromError should return false for plain error")
	}
	_, ok = cerror.FromError(nil)
	if ok {
		t.Error("FromError should return false for nil")
	}
}

// --- HTTPStatus ---

func TestHTTPStatus_KnownCodes(t *testing.T) {
	cases := []struct {
		code cerror.Code
		want int
	}{
		{cerror.CodeBadRequest, http.StatusBadRequest},
		{cerror.CodeUnauthorized, http.StatusUnauthorized},
		{cerror.CodeForbidden, http.StatusForbidden},
		{cerror.CodeNotFound, http.StatusNotFound},
		{cerror.CodeConflict, http.StatusConflict},
		{cerror.CodeTooManyRequests, http.StatusTooManyRequests},
		{cerror.CodeInternal, http.StatusInternalServerError},
		{cerror.CodeUnavailable, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		if got := cerror.HTTPStatus(c.code); got != c.want {
			t.Errorf("HTTPStatus(%d) = %d, want %d", c.code, got, c.want)
		}
	}
}

func TestHTTPStatus_CustomHTTPRangeCode(t *testing.T) {
	// 用户自定义的 422 仍在 4xx 范围内，应直接返回
	custom := cerror.Code(422)
	if got := cerror.HTTPStatus(custom); got != 422 {
		t.Errorf("HTTPStatus(422) = %d, want 422", got)
	}
}

func TestHTTPStatus_UnknownCodeFallsBackTo500(t *testing.T) {
	unknown := cerror.Code(9999)
	if got := cerror.HTTPStatus(unknown); got != 500 {
		t.Errorf("HTTPStatus(9999) = %d, want 500", got)
	}
}

// --- predefined errors usable as sentinel values ---

func TestPredefinedErrors_AsSentinels(t *testing.T) {
	// 预定义错误可以通过 errors.As 检查
	err := fmt.Errorf("handler: %w", cerror.ErrNotFound)
	var ce *cerror.Error
	if !errors.As(err, &ce) {
		t.Fatal("errors.As should find *Error")
	}
	if ce.Code() != cerror.CodeNotFound {
		t.Errorf("Code() = %v, want CodeNotFound", ce.Code())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
