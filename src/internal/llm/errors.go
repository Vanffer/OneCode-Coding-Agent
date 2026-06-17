package llm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
)

// LLMError 通用 LLM 错误
type LLMError struct {
	Message string
}

func (e *LLMError) Error() string { return e.Message }

// AuthenticationError 认证失败
type AuthenticationError struct {
	Message string
}

func (e *AuthenticationError) Error() string { return e.Message }

// RateLimitError 限流错误
type RateLimitError struct {
	Message    string
	RetryAfter string
}

func (e *RateLimitError) Error() string { return e.Message }

// NetworkError 网络错误
type NetworkError struct {
	Message string
}

func (e *NetworkError) Error() string { return e.Message }

// ContextTooLongError 上下文过长
type ContextTooLongError struct {
	Message string
}

func (e *ContextTooLongError) Error() string { return e.Message }

// classifyAnthropicError 将 Anthropic API 错误映射为业务语义
func classifyAnthropicError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		// 413 或 prompt is too long → ContextTooLongError
		if apiErr.StatusCode == 413 || strings.Contains(apiErr.Error(), "prompt is too long") {
			return &ContextTooLongError{Message: fmt.Sprintf("上下文过长: %s", apiErr.Error())}
		}
		switch apiErr.Type() {
		case anthropic.ErrorTypeAuthenticationError:
			return &AuthenticationError{Message: fmt.Sprintf("认证失败: %s", apiErr.Error())}
		case anthropic.ErrorTypeRateLimitError:
			retry := ""
			if apiErr.Response != nil {
				retry = apiErr.Response.Header.Get("Retry-After")
			}
			msg := "请求被限流"
			if retry != "" {
				msg += fmt.Sprintf("，请 %ss 后重试", retry)
			}
			return &RateLimitError{Message: msg, RetryAfter: retry}
		default:
			return &LLMError{Message: fmt.Sprintf("API 错误 (%d): %s", apiErr.StatusCode, apiErr.Error())}
		}
	}
	return &NetworkError{Message: fmt.Sprintf("网络错误: %s", err.Error())}
}

// classifyOpenAIError 将 OpenAI API 错误映射为业务语义
func classifyOpenAIError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 413 || (apiErr.StatusCode == 400 && containsContextLengthError(apiErr.Error())) {
			return &ContextTooLongError{Message: fmt.Sprintf("上下文过长: %s", apiErr.Error())}
		}
		switch apiErr.StatusCode {
		case 401:
			return &AuthenticationError{Message: fmt.Sprintf("认证失败: %s", apiErr.Error())}
		case 429:
			retry := ""
			if apiErr.Response != nil {
				retry = apiErr.Response.Header.Get("Retry-After")
			}
			msg := "请求被限流"
			if retry != "" {
				msg += fmt.Sprintf("，请 %ss 后重试", retry)
			}
			return &RateLimitError{Message: msg, RetryAfter: retry}
		default:
			return &LLMError{Message: fmt.Sprintf("API 错误 (%d): %s", apiErr.StatusCode, apiErr.Error())}
		}
	}
	return &NetworkError{Message: fmt.Sprintf("网络错误: %s", err.Error())}
}

// containsContextLengthError 检查错误消息是否包含上下文过长的关键词
func containsContextLengthError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "prompt is too long")
}
