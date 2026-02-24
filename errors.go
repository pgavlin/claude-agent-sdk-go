package agentsdk

import "fmt"

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
