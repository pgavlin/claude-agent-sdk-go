package agentsdk

import (
	"fmt"
	"strings"
)

// sdkError is the base error implementation for all SDK errors.
type sdkError struct {
	message string
	cause   error
}

func (e *sdkError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

func (e *sdkError) Unwrap() error {
	return e.cause
}

func newError(message string, cause error) sdkError {
	return sdkError{message: message, cause: cause}
}

// CLIConnectionError indicates a failure to connect to the CLI subprocess.
type CLIConnectionError struct {
	sdkError
}

// NewCLIConnectionError creates a new CLIConnectionError.
func NewCLIConnectionError(message string, cause error) *CLIConnectionError {
	return &CLIConnectionError{sdkError: newError(message, cause)}
}

// CLINotFoundError indicates the CLI binary could not be found.
type CLINotFoundError struct {
	sdkError
	CLIPath string
}

// NewCLINotFoundError creates a new CLINotFoundError.
func NewCLINotFoundError(message string, cliPath string) *CLINotFoundError {
	return &CLINotFoundError{sdkError: newError(message, nil), CLIPath: cliPath}
}

// ProcessError indicates the CLI subprocess exited with a non-zero exit code.
type ProcessError struct {
	sdkError
	ExitCode int
	Stderr   string
}

// NewProcessError creates a new ProcessError.
func NewProcessError(message string, exitCode int, stderr string) *ProcessError {
	return &ProcessError{sdkError: newError(message, nil), ExitCode: exitCode, Stderr: stderr}
}

// JSONDecodeError indicates a failure to parse a JSON line from the CLI.
type JSONDecodeError struct {
	sdkError
	Line          string
	OriginalError error
}

// NewJSONDecodeError creates a new JSONDecodeError.
func NewJSONDecodeError(message string, line string, original error) *JSONDecodeError {
	return &JSONDecodeError{sdkError: newError(message, original), Line: line, OriginalError: original}
}

// MessageParseError indicates a failure to parse a message from the CLI output.
type MessageParseError struct {
	sdkError
	Data map[string]any
}

// NewMessageParseError creates a new MessageParseError.
func NewMessageParseError(message string, data map[string]any) *MessageParseError {
	return &MessageParseError{sdkError: newError(message, nil), Data: data}
}

// ValidationError indicates an invalid option combination or value,
// detected before the CLI subprocess is spawned.
type ValidationError struct {
	sdkError
}

// NewValidationError creates a new ValidationError.
func NewValidationError(message string) *ValidationError {
	return &ValidationError{sdkError: newError(message, nil)}
}

// ResultError indicates the CLI exited after reporting a terminal error
// result.
//
// The CLI ends a failed run by emitting a result message with is_error true
// — yielded to the caller as a ResultMessage — and then exiting non-zero.
// This error replaces the bare exit-code ProcessError for that case and
// carries the result's payload, so callers can branch on why the run failed
// without matching on strings:
//
//	var resErr *agentsdk.ResultError
//	if errors.As(err, &resErr) {
//		switch {
//		case resErr.TerminalReason == "api_error": // overloaded, timeout
//			retry()
//		case resErr.Subtype == "error_max_turns":
//			// ...
//		}
//	}
//
// It wraps a ProcessError, so errors.As for *ProcessError also matches.
type ResultError struct {
	*ProcessError

	// Subtype is the result subtype, such as "error_max_turns" or
	// "error_during_execution" — or "success" when the agent loop itself
	// completed but the last turn was an API error.
	Subtype string

	// Errors are the error strings the CLI reported. It may be empty.
	Errors []string

	// Result is the result text, if any. For API failures it holds the
	// "API Error: ..." prose.
	Result string

	// APIErrorStatus is the HTTP status of the failing API call, or zero.
	APIErrorStatus int

	// TerminalReason says why the run ended, such as "api_error" or
	// "max_turns", when the CLI reported one.
	TerminalReason string

	// SessionID is the session the result belongs to, when reported.
	SessionID string

	// Data is the raw result message payload as the CLI emitted it.
	Data map[string]any
}

// NewResultError creates a ResultError from a raw result message payload.
func NewResultError(message string, data map[string]any, exitCode int) *ResultError {
	if data == nil {
		data = map[string]any{}
	}
	subtype, _ := data["subtype"].(string)
	result, _ := data["result"].(string)
	terminalReason, _ := data["terminal_reason"].(string)
	sessionID, _ := data["session_id"].(string)

	return &ResultError{
		ProcessError:   NewProcessError(message, exitCode, ""),
		Subtype:        subtype,
		Errors:         normalizeResultErrors(data["errors"]),
		Result:         result,
		APIErrorStatus: intFromAny(data["api_error_status"]),
		TerminalReason: terminalReason,
		SessionID:      sessionID,
		Data:           data,
	}
}

// normalizeResultErrors normalizes a result frame's errors field to clean
// strings. The CLI emits a list of strings; a bare string is tolerated for
// older emitters, and non-string or blank entries are dropped so the
// structured ResultError.Errors and the error text always agree.
func normalizeResultErrors(raw any) []string {
	var items []any
	switch v := raw.(type) {
	case string:
		items = []any{v}
	case []any:
		items = v
	default:
		return nil
	}

	var out []string
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
