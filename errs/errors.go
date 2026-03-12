package errs

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是 lumin-proxy 的错误码类型。
type Code int

const (
	// CodeOK 成功
	CodeOK Code = 0
	// CodeUnknown 未知错误
	CodeUnknown Code = 1000
	// CodeInvalidRequest 无效请求
	CodeInvalidRequest Code = 1001
	// CodeUnauthorized 未授权
	CodeUnauthorized Code = 1002
	// CodeForbidden 禁止访问
	CodeForbidden Code = 1003
	// CodeNotFound 资源未找到
	CodeNotFound Code = 1004
	// CodeRateLimited 请求被限流
	CodeRateLimited Code = 1005
	// CodeNoAvailableAccount 无可用账号
	CodeNoAvailableAccount Code = 1006
	// CodeUpstreamError 上游服务错误
	CodeUpstreamError Code = 1007
	// CodeTimeout 请求超时
	CodeTimeout Code = 1008
	// CodeInternal 内部错误
	CodeInternal Code = 1009
	// CodePluginError 插件错误
	CodePluginError Code = 1010
	// CodeProtocolError 协议解析错误
	CodeProtocolError Code = 1011
	// CodeModelNotSupported 模型不支持
	CodeModelNotSupported Code = 1012
)

// HTTPStatus 返回错误码对应的 HTTP 状态码。
func (c Code) HTTPStatus() int {
	switch c {
	case CodeOK:
		return http.StatusOK
	case CodeInvalidRequest, CodeProtocolError:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound, CodeModelNotSupported:
		return http.StatusNotFound
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeNoAvailableAccount:
		return http.StatusServiceUnavailable
	case CodeUpstreamError:
		return http.StatusBadGateway
	case CodeTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// ProxyError 是 lumin-proxy 的统一错误类型。
type ProxyError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

// Error 实现 error 接口。
func (e *ProxyError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// Unwrap 返回底层错误。
func (e *ProxyError) Unwrap() error {
	return e.Cause
}

// New 创建一个新的 ProxyError。
func New(code Code, message string) *ProxyError {
	return &ProxyError{Code: code, Message: message}
}

// Wrap 创建一个包装了底层错误的 ProxyError。
func Wrap(code Code, message string, cause error) *ProxyError {
	return &ProxyError{Code: code, Message: message, Cause: cause}
}

// IsProxyError 检查 error 是否为 ProxyError 类型。
func IsProxyError(err error) (*ProxyError, bool) {
	var pe *ProxyError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}


