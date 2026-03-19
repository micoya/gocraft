// Package cerror 提供统一的业务错误类型和错误码体系。
//
// 设计目标：
//   - 错误码与 HTTP 状态码对齐，降低跨层理解成本
//   - 支持 errors.Is / errors.As / errors.Unwrap 标准链式操作
//   - 可被 chttp 中间件识别并直接映射为 JSON 响应
package cerror

import (
	"errors"
	"fmt"
)

// Code 业务错误码，与 HTTP 状态码语义对齐。
type Code int

// 预定义业务错误码。
const (
	CodeOK             Code = 0   // 成功，通常不用于错误场景
	CodeBadRequest     Code = 400 // 请求参数错误
	CodeUnauthorized   Code = 401 // 未登录 / Token 无效
	CodeForbidden      Code = 403 // 无操作权限
	CodeNotFound       Code = 404 // 资源不存在
	CodeConflict       Code = 409 // 资源冲突（如重复创建）
	CodeTooManyRequests Code = 429 // 请求过于频繁
	CodeInternal       Code = 500 // 服务内部错误
	CodeUnavailable    Code = 503 // 服务暂不可用
)

// 预定义常用错误，可直接返回或通过 Wrap 包装 cause。
var (
	ErrBadRequest      = New(CodeBadRequest, "bad request")
	ErrUnauthorized    = New(CodeUnauthorized, "unauthorized")
	ErrForbidden       = New(CodeForbidden, "forbidden")
	ErrNotFound        = New(CodeNotFound, "not found")
	ErrConflict        = New(CodeConflict, "conflict")
	ErrTooManyRequests = New(CodeTooManyRequests, "too many requests")
	ErrInternal        = New(CodeInternal, "internal server error")
	ErrUnavailable     = New(CodeUnavailable, "service unavailable")
)

// Error 是带业务码的结构化错误。
type Error struct {
	code    Code
	message string
	cause   error
}

// New 创建带业务码和描述的错误。
func New(code Code, message string) *Error {
	return &Error{code: code, message: message}
}

// Newf 创建带格式化描述的错误。
func Newf(code Code, format string, args ...any) *Error {
	return &Error{code: code, message: fmt.Sprintf(format, args...)}
}

// Wrap 包装一个已有错误，赋予业务码和对外描述。
// cause 会被附在错误链中，支持 errors.Unwrap 遍历。
func Wrap(code Code, message string, cause error) *Error {
	return &Error{code: code, message: message, cause: cause}
}

// Wrapf 包装一个已有错误并支持格式化描述。
func Wrapf(code Code, cause error, format string, args ...any) *Error {
	return &Error{code: code, message: fmt.Sprintf(format, args...), cause: cause}
}

// Code 返回业务错误码。
func (e *Error) Code() Code { return e.code }

// Message 返回对外展示的错误描述。
func (e *Error) Message() string { return e.message }

// Error 实现 error 接口，格式为 "[code] message: cause"。
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("[%d] %s: %v", e.code, e.message, e.cause)
	}
	return fmt.Sprintf("[%d] %s", e.code, e.message)
}

// Unwrap 返回内层原始错误，支持 errors.Is / errors.As 的链式遍历。
func (e *Error) Unwrap() error { return e.cause }

// IsCode 检查 err 链中是否存在指定 code 的 *Error。
func IsCode(err error, code Code) bool {
	var ce *Error
	return errors.As(err, &ce) && ce.code == code
}

// FromError 从 err 链中提取第一个 *Error。
// 若不存在则返回 (nil, false)。
func FromError(err error) (*Error, bool) {
	var ce *Error
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// HTTPStatus 将业务码映射为对应的 HTTP 状态码。
// 当 code 本身在 400–599 范围内时直接返回，否则返回 500。
func HTTPStatus(code Code) int {
	switch code {
	case CodeBadRequest:
		return 400
	case CodeUnauthorized:
		return 401
	case CodeForbidden:
		return 403
	case CodeNotFound:
		return 404
	case CodeConflict:
		return 409
	case CodeTooManyRequests:
		return 429
	case CodeInternal:
		return 500
	case CodeUnavailable:
		return 503
	default:
		if int(code) >= 400 && int(code) < 600 {
			return int(code)
		}
		return 500
	}
}
