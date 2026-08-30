package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// APIError is a structured error returned by a provider API. It captures the
// HTTP status code and the provider's error payload so callers can branch on
// specific failure modes (rate limits, auth, context overflow, ...).
type APIError struct {
	// Provider is the name of the provider that produced the error,
	// e.g. "openai", "openai-responses" or "anthropic".
	Provider string
	// StatusCode is the HTTP status code. 0 for transport-level failures.
	StatusCode int
	// Type is the provider-specific error type/code, when available.
	Type string
	// Message is the human-readable error message.
	Message string
	// Body is the raw response body, kept for diagnostics.
	Body string
}

func (e *APIError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("callable: %s request failed: %s", e.Provider, e.Message)
	}
	return fmt.Sprintf("callable: %s API error (status %d, type %q): %s",
		e.Provider, e.StatusCode, e.Type, e.Message)
}

// IsRetryable reports whether the error is transient (transport failure,
// 429, or a 5xx) and may succeed on retry.
func (e *APIError) IsRetryable() bool {
	if e.StatusCode == 0 {
		return true // transport-level failure
	}
	return e.StatusCode == 429 || e.StatusCode >= 500
}

// newAPIError builds an APIError from a non-2xx response body, best-effort
// extracting type/message from the common error payload shapes of the three
// supported providers.
func newAPIError(provider string, statusCode int, body []byte) *APIError {
	err := &APIError{
		Provider:   provider,
		StatusCode: statusCode,
		Body:       string(body),
	}

	var payload struct {
		Error *struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		// OpenAI Responses streams errors as {type, code, message, param} at
		// the top level.
		Type    string `json:"type"`
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		switch {
		case payload.Error != nil:
			err.Type = payload.Error.Type
			if code := codeString(payload.Error.Code); code != "" && err.Type == "" {
				err.Type = code
			}
			err.Message = payload.Error.Message
		default:
			err.Type = payload.Type
			if code := codeString(payload.Code); code != "" && err.Type == "" {
				err.Type = code
			}
			err.Message = payload.Message
		}
	}
	if err.Message == "" {
		err.Message = strings.TrimSpace(string(body))
	}
	if err.Message == "" {
		err.Message = fmt.Sprintf("unexpected status code %d", statusCode)
	}
	return err
}

func codeString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", c)
	}
}
