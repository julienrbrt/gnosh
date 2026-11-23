package core

import (
	"fmt"
	"time"
)

// ErrorKind sorts the ways a provider can fail, so that a caller can decide
// what to do next without reading messages.
type ErrorKind string

const (
	// ErrRateLimit: the provider wants fewer requests. Wait, then retry.
	ErrRateLimit ErrorKind = "rate_limit"
	// ErrOverloaded: the provider is too busy. Wait, then retry.
	ErrOverloaded ErrorKind = "overloaded"
	// ErrContextTooLong: the prompt does not fit the model. Retrying the
	// same request will fail the same way.
	ErrContextTooLong ErrorKind = "context_too_long"
	// ErrAuth: the key is missing, wrong, or not allowed to do this.
	ErrAuth ErrorKind = "auth"
	// ErrInvalidRequest: the provider refused the request as malformed.
	ErrInvalidRequest ErrorKind = "invalid_request"
	// ErrServer: the provider failed on its own side.
	ErrServer ErrorKind = "server"
	// ErrNetwork: the provider could not be reached, or the connection
	// broke.
	ErrNetwork ErrorKind = "network"
	// ErrTimeout: one of the provider's time limits ran out. Timeout says
	// which.
	ErrTimeout ErrorKind = "timeout"
	// ErrBadResponse: the provider answered with something that could not
	// be read.
	ErrBadResponse ErrorKind = "bad_response"
)

// ProviderError is a failure of a model service.
type ProviderError struct {
	// Provider is the configured name of the provider that failed.
	Provider string
	Kind     ErrorKind
	// Status is the HTTP status, or zero when there was none.
	Status int
	// Message says what went wrong, in the provider's words where it gave
	// any.
	Message string
	// RetryAfter is how long the provider asked the caller to wait. Zero
	// means it did not say.
	RetryAfter time.Duration
	// Timeout names the limit that ran out when Kind is ErrTimeout:
	// "connect", "first_token", "idle" or "total".
	Timeout string
	// Body is the start of the raw response, kept for the log.
	Body string
	// Tokens and Window are, for ErrContextTooLong, what the provider
	// said of the refused request when it said: how many tokens it
	// counted in it, and how many its model holds (llama.cpp's
	// n_prompt_tokens and n_ctx; the numbers in vLLM's, OpenAI's and
	// Anthropic's messages). Zero when it did not say.
	Tokens, Window int
	// Err is the underlying error, if there was one.
	Err error
}

func (e *ProviderError) Error() string {
	s := e.Provider
	if s == "" {
		s = "provider"
	}
	s += ": " + string(e.Kind)
	if e.Kind == ErrTimeout && e.Timeout != "" {
		s += " (" + e.Timeout + ")"
	}
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Message != "" {
		s += ": " + e.Message
	} else if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Retryable reports whether sending the same request again could work.
func (e *ProviderError) Retryable() bool {
	switch e.Kind {
	case ErrRateLimit, ErrOverloaded, ErrServer, ErrNetwork, ErrTimeout, ErrBadResponse:
		return true
	}
	return false
}
