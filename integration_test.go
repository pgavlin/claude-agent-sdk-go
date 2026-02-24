package agentsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain intercepts execution when the test binary is re-invoked as a
// subprocess helper. The AGENTSDK_TEST_HELPER env var selects the helper
// mode. When unset, normal test execution proceeds.
func TestMain(m *testing.M) {
	if mode := os.Getenv("AGENTSDK_TEST_HELPER"); mode != "" {
		runTestHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func runTestHelper(mode string) {
	switch mode {
	case "fake-cli":
		helperFakeCLI()
	case "echo-json":
		helperEchoJSON()
	case "write-stderr":
		helperWriteStderr()
	case "partial-json":
		helperPartialJSON()
	case "exit-error":
		os.Exit(42)
	case "version":
		fmt.Print("2.5.0")
	case "version-old":
		fmt.Print("1.9.0")
	}
}

// helperFakeCLI simulates a minimal Claude CLI that handles the control
// protocol: responds to initialize, then echoes an assistant message and
// result for each user message received.
func helperFakeCLI() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		switch msg["type"] {
		case "control_request":
			reqID, _ := msg["request_id"].(string)
			resp := map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"subtype":    "success",
					"request_id": reqID,
					"response":   map[string]any{"version": "test"},
				},
			}
			data, _ := json.Marshal(resp)
			fmt.Println(string(data))
		case "user":
			// Send assistant message
			assistant := map[string]any{
				"type": "assistant",
				"message": map[string]any{
					"model": "test-model",
					"content": []any{
						map[string]any{"type": "text", "text": "test response"},
					},
				},
			}
			data, _ := json.Marshal(assistant)
			fmt.Println(string(data))

			// Send result
			result := map[string]any{
				"type":            "result",
				"subtype":         "success",
				"duration_ms":     10,
				"duration_api_ms": 5,
				"is_error":        false,
				"num_turns":       1,
				"session_id":      "test-session",
				"result":          "test response",
			}
			data, _ = json.Marshal(result)
			fmt.Println(string(data))
		}
	}
}

// helperEchoJSON reads JSON lines from stdin and writes them to stdout.
func helperEchoJSON() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
}

// helperWriteStderr writes lines to stderr, then waits for stdin to close.
func helperWriteStderr() {
	fmt.Fprintln(os.Stderr, "stderr line 1")
	fmt.Fprintln(os.Stderr, "stderr line 2")
	io.Copy(io.Discard, os.Stdin)
}

// helperPartialJSON writes a JSON object split across two stdout lines,
// then waits for stdin to close.
func helperPartialJSON() {
	// Write a single JSON object split across two lines
	fmt.Print(`{"type":"system",`)
	fmt.Println()
	fmt.Println(`"subtype":"test-partial"}`)
	io.Copy(io.Discard, os.Stdin)
}

// testHelperEnv returns the env map to make the test binary run as a
// specific helper when invoked as a subprocess.
func testHelperEnv(mode string) map[string]string {
	return map[string]string{
		"AGENTSDK_TEST_HELPER": mode,
	}
}

// ---------------------------------------------------------------------------
// SubprocessTransport tests
// ---------------------------------------------------------------------------

func TestSubprocessTransport_ConnectAndIsReady(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("echo-json"),
	}, "sdk-go-test")

	if tr.IsReady() {
		t.Error("expected IsReady() == false before Connect")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if !tr.IsReady() {
		t.Error("expected IsReady() == true after Connect")
	}

	// Start consuming messages so Close doesn't deadlock
	go func() {
		for range tr.ReadMessages(ctx) {
		}
	}()

	if err := tr.EndInput(); err != nil {
		t.Fatalf("EndInput failed: %v", err)
	}

	// Wait a moment for the process to finish
	time.Sleep(50 * time.Millisecond)

	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if tr.IsReady() {
		t.Error("expected IsReady() == false after Close")
	}
}

func TestSubprocessTransport_WriteAndReadMessages(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("echo-json"),
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Write a JSON message
	if err := tr.Write(ctx, `{"type":"system","subtype":"test"}`); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close stdin so the echo process exits
	tr.EndInput()

	// Read messages back
	var messages []map[string]any
	for msg, err := range tr.ReadMessages(ctx) {
		if err != nil {
			// ProcessError is expected when process exits normally after reading
			if _, ok := err.(*ProcessError); ok {
				break
			}
			t.Fatalf("ReadMessages error: %v", err)
		}
		messages = append(messages, msg)
	}

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0]["type"] != "system" {
		t.Errorf("expected type 'system', got %v", messages[0]["type"])
	}
	if messages[0]["subtype"] != "test" {
		t.Errorf("expected subtype 'test', got %v", messages[0]["subtype"])
	}
}

func TestSubprocessTransport_ReadMessages_PartialJSON(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("partial-json"),
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Close stdin so the helper exits
	tr.EndInput()

	var messages []map[string]any
	for msg, err := range tr.ReadMessages(ctx) {
		if err != nil {
			if _, ok := err.(*ProcessError); ok {
				break
			}
			t.Fatalf("ReadMessages error: %v", err)
		}
		if msg != nil {
			messages = append(messages, msg)
		}
	}

	if len(messages) != 1 {
		t.Fatalf("expected 1 reassembled message, got %d", len(messages))
	}
	if messages[0]["type"] != "system" {
		t.Errorf("expected type 'system', got %v", messages[0]["type"])
	}
	if messages[0]["subtype"] != "test-partial" {
		t.Errorf("expected subtype 'test-partial', got %v", messages[0]["subtype"])
	}
}

func TestSubprocessTransport_ReadMessages_ProcessError(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("exit-error"),
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var gotProcessError bool
	for _, err := range tr.ReadMessages(ctx) {
		if err != nil {
			if pe, ok := err.(*ProcessError); ok {
				gotProcessError = true
				if pe.ExitCode != 42 {
					t.Errorf("expected exit code 42, got %d", pe.ExitCode)
				}
			}
		}
	}

	if !gotProcessError {
		t.Error("expected ProcessError from ReadMessages")
	}
}

func TestSubprocessTransport_StderrCallback(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	var mu sync.Mutex
	var stderrLines []string

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("write-stderr"),
		Stderr: func(line string) {
			mu.Lock()
			stderrLines = append(stderrLines, line)
			mu.Unlock()
		},
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Read messages in background
	go func() {
		for range tr.ReadMessages(ctx) {
		}
	}()

	tr.EndInput()

	// Wait for stderr to be consumed
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	lines := append([]string(nil), stderrLines...)
	mu.Unlock()

	if len(lines) != 2 {
		t.Fatalf("expected 2 stderr lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "stderr line 1" {
		t.Errorf("expected 'stderr line 1', got %q", lines[0])
	}
	if lines[1] != "stderr line 2" {
		t.Errorf("expected 'stderr line 2', got %q", lines[1])
	}

	tr.Close()
}

func TestSubprocessTransport_EndInput(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("echo-json"),
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := tr.EndInput(); err != nil {
		t.Fatalf("EndInput failed: %v", err)
	}

	// Second EndInput on already-closed pipe should not panic
	// (it may return an error, which is fine)
	tr.EndInput()

	// Drain messages
	for range tr.ReadMessages(ctx) {
	}
}

func TestSubprocessTransport_WriteNotConnected(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{}, "sdk-go-test")

	ctx := context.Background()
	err := tr.Write(ctx, `{"test":true}`)
	if err == nil {
		t.Error("expected error writing to unconnected transport")
	}
	if _, ok := err.(*CLIConnectionError); !ok {
		t.Errorf("expected *CLIConnectionError, got %T: %v", err, err)
	}
}

func TestSubprocessTransport_FindCLI_ExplicitPath(t *testing.T) {
	// Valid path — use the test binary itself
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
	}, "sdk-go-test")
	path, err := tr.findCLI()
	if err != nil {
		t.Fatalf("findCLI failed with valid path: %v", err)
	}
	if path != os.Args[0] {
		t.Errorf("expected %s, got %s", os.Args[0], path)
	}
}

func TestSubprocessTransport_FindCLI_ExplicitPathNotFound(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: "/nonexistent/path/to/claude",
	}, "sdk-go-test")
	_, err := tr.findCLI()
	if err == nil {
		t.Fatal("expected error for nonexistent CLI path")
	}
	notFound, ok := err.(*CLINotFoundError)
	if !ok {
		t.Fatalf("expected *CLINotFoundError, got %T: %v", err, err)
	}
	if notFound.CLIPath != "/nonexistent/path/to/claude" {
		t.Errorf("expected CLIPath '/nonexistent/path/to/claude', got %s", notFound.CLIPath)
	}
}

func TestSubprocessTransport_FindCLI_LookPath(t *testing.T) {
	// This test only passes if 'claude' is in PATH
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found in PATH")
	}

	tr := NewSubprocessTransport(&ClaudeAgentOptions{}, "sdk-go-test")
	path, err := tr.findCLI()
	if err != nil {
		t.Fatalf("findCLI failed: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty CLI path")
	}
}

func TestSubprocessTransport_CheckVersion(t *testing.T) {
	// checkVersion spawns its own subprocess (cliPath -v), so the helper
	// env var must be in the parent process env to be inherited.
	t.Setenv("AGENTSDK_TEST_HELPER", "version")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := tr.checkVersion(ctx, os.Args[0])
	if err != nil {
		t.Fatalf("checkVersion failed for 2.5.0: %v", err)
	}
}

func TestSubprocessTransport_CheckVersion_TooOld(t *testing.T) {
	t.Setenv("AGENTSDK_TEST_HELPER", "version-old")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := tr.checkVersion(ctx, os.Args[0])
	if err == nil {
		t.Fatal("expected error for version 1.9.0")
	}
	if !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("expected 'below minimum' in error, got: %v", err)
	}
}

func TestSubprocessTransport_BuildEnv(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Env: map[string]string{
			"MY_VAR": "my_value",
		},
		CWD:                     "/tmp/test",
		EnableFileCheckpointing: true,
	}, "sdk-go-test")

	env := tr.buildEnv()

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["MY_VAR"] != "my_value" {
		t.Errorf("expected MY_VAR=my_value, got %s", envMap["MY_VAR"])
	}
	if envMap["CLAUDE_CODE_ENTRYPOINT"] != "sdk-go-test" {
		t.Errorf("expected CLAUDE_CODE_ENTRYPOINT=sdk-go-test, got %s", envMap["CLAUDE_CODE_ENTRYPOINT"])
	}
	if envMap["CLAUDE_AGENT_SDK_VERSION"] != sdkVersion {
		t.Errorf("expected CLAUDE_AGENT_SDK_VERSION=%s, got %s", sdkVersion, envMap["CLAUDE_AGENT_SDK_VERSION"])
	}
	if envMap["PWD"] != "/tmp/test" {
		t.Errorf("expected PWD=/tmp/test, got %s", envMap["PWD"])
	}
	if envMap["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] != "true" {
		t.Errorf("expected CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true, got %s", envMap["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"])
	}
}

func TestSubprocessTransport_FakeCLI_FullProtocol(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("fake-cli"),
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Send an initialize control request
	initReq := map[string]any{
		"type":       "control_request",
		"request_id": "req_1_test",
		"request":    map[string]any{"subtype": "initialize"},
	}
	initData, _ := json.Marshal(initReq)
	if err := tr.Write(ctx, string(initData)); err != nil {
		t.Fatalf("Write init failed: %v", err)
	}

	// Send a user message
	userMsg := map[string]any{
		"type":               "user",
		"session_id":         "",
		"message":            map[string]any{"role": "user", "content": "hello"},
		"parent_tool_use_id": nil,
	}
	userData, _ := json.Marshal(userMsg)
	if err := tr.Write(ctx, string(userData)); err != nil {
		t.Fatalf("Write user msg failed: %v", err)
	}

	// Close stdin
	tr.EndInput()

	// Collect messages
	var messages []map[string]any
	for msg, err := range tr.ReadMessages(ctx) {
		if err != nil {
			break
		}
		if msg != nil {
			messages = append(messages, msg)
		}
	}

	// Should get: control_response, assistant, result
	if len(messages) < 3 {
		t.Fatalf("expected at least 3 messages, got %d: %+v", len(messages), messages)
	}

	// First: control_response
	if messages[0]["type"] != "control_response" {
		t.Errorf("expected control_response, got %v", messages[0]["type"])
	}

	// Second: assistant
	if messages[1]["type"] != "assistant" {
		t.Errorf("expected assistant, got %v", messages[1]["type"])
	}

	// Third: result
	if messages[2]["type"] != "result" {
		t.Errorf("expected result, got %v", messages[2]["type"])
	}
}

// ---------------------------------------------------------------------------
// Query() tests
// ---------------------------------------------------------------------------

func TestQuery_WithFakeCLI(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	opts := &ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("fake-cli"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	messages, errFn := Query(ctx, "hello", opts)

	var msgs []Message
	for msg := range messages {
		msgs = append(msgs, msg)
	}

	if err := errFn(); err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	// Should have at least an AssistantMessage and a ResultMessage
	var gotAssistant, gotResult bool
	for _, msg := range msgs {
		switch m := msg.(type) {
		case *AssistantMessage:
			gotAssistant = true
			if len(m.Content) == 0 {
				t.Error("expected non-empty content in AssistantMessage")
			} else if tb, ok := m.Content[0].(TextBlock); ok {
				if tb.Text != "test response" {
					t.Errorf("expected 'test response', got %q", tb.Text)
				}
			}
		case *ResultMessage:
			gotResult = true
			if m.Result != "test response" {
				t.Errorf("expected result 'test response', got %q", m.Result)
			}
			if m.SessionID != "test-session" {
				t.Errorf("expected session_id 'test-session', got %q", m.SessionID)
			}
		}
	}

	if !gotAssistant {
		t.Error("expected an AssistantMessage")
	}
	if !gotResult {
		t.Error("expected a ResultMessage")
	}
}

func TestQuery_NilOpts(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	// Query with nil opts should not panic. It will fail to find the CLI,
	// and that error should be returned by the error function.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	messages, errFn := Query(ctx, "hello", nil)
	for range messages {
	}

	err := errFn()
	if err == nil {
		// If claude happens to be installed, this test might actually succeed.
		// That's fine too.
		if _, lookErr := exec.LookPath("claude"); lookErr != nil {
			t.Error("expected error when CLI not found with nil opts")
		}
	}
}

func TestQuery_ContextCancellation(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	cancel()

	opts := &ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("fake-cli"),
	}

	messages, errFn := Query(ctx, "hello", opts)
	for range messages {
	}

	// Should get a context error
	err := errFn()
	if err == nil {
		// Context was canceled — expect an error
		t.Error("expected error from cancelled context")
	}
}

// ---------------------------------------------------------------------------
// NewClient tests
// ---------------------------------------------------------------------------

func TestNewClient_DefaultTransport(t *testing.T) {
	client := NewClient(&ClaudeAgentOptions{})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	// Verify the transport is a *SubprocessTransport
	if _, ok := client.transport.(*SubprocessTransport); !ok {
		t.Errorf("expected *SubprocessTransport, got %T", client.transport)
	}
}

func TestNewClient_NilOpts(t *testing.T) {
	client := NewClient(nil)
	if client == nil {
		t.Fatal("expected non-nil client with nil opts")
	}
	if client.opts == nil {
		t.Fatal("expected non-nil opts after construction")
	}
}

func TestNewClient_CanUseToolSetsPermissionPrompt(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(ctx context.Context, toolName string, input map[string]any, permCtx ToolPermissionContext) (PermissionResult, error) {
			return PermissionAllow{}, nil
		},
	}

	_ = NewClient(opts)

	if opts.PermissionPromptToolName != "stdio" {
		t.Errorf("expected PermissionPromptToolName 'stdio', got %q", opts.PermissionPromptToolName)
	}
}

func TestNewClient_ExplicitPermissionPromptNotOverridden(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(ctx context.Context, toolName string, input map[string]any, permCtx ToolPermissionContext) (PermissionResult, error) {
			return PermissionAllow{}, nil
		},
		PermissionPromptToolName: "custom-tool",
	}

	_ = NewClient(opts)

	if opts.PermissionPromptToolName != "custom-tool" {
		t.Errorf("expected PermissionPromptToolName 'custom-tool', got %q", opts.PermissionPromptToolName)
	}
}

func TestNewClient_ConnectWithFakeCLI(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	client := NewClient(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("fake-cli"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	info := client.ServerInfo()
	if info == nil {
		t.Fatal("expected non-nil ServerInfo after Connect")
	}
	if info["version"] != "test" {
		t.Errorf("expected server version 'test', got %v", info["version"])
	}
}

func TestNewClient_SendQueryReceiveResponse(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")

	client := NewClient(&ClaudeAgentOptions{
		CLIPath: os.Args[0],
		Env:     testHelperEnv("fake-cli"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if err := client.SendQuery(ctx, "test prompt", "default"); err != nil {
		t.Fatalf("SendQuery failed: %v", err)
	}

	var msgs []Message
	for msg := range client.ReceiveResponse() {
		msgs = append(msgs, msg)
	}

	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}

	// Last message should be a ResultMessage
	last := msgs[len(msgs)-1]
	if _, ok := last.(*ResultMessage); !ok {
		t.Errorf("expected last message to be *ResultMessage, got %T", last)
	}
}

// ---------------------------------------------------------------------------
// Error type tests
// ---------------------------------------------------------------------------

func TestErrors_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{
			"CLIConnectionError",
			NewCLIConnectionError("connection failed", fmt.Errorf("dial timeout")),
			"connection failed: dial timeout",
		},
		{
			"CLIConnectionError_noCause",
			NewCLIConnectionError("not connected", nil),
			"not connected",
		},
		{
			"CLINotFoundError",
			NewCLINotFoundError("not found", "/usr/bin/claude"),
			"not found",
		},
		{
			"ProcessError",
			NewProcessError("exited", 1, "panic"),
			"exited",
		},
		{
			"JSONDecodeError",
			NewJSONDecodeError("bad json", "{invalid", fmt.Errorf("unexpected token")),
			"bad json",
		},
		{
			"MessageParseError",
			NewMessageParseError("missing field", map[string]any{"type": "test"}),
			"missing field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if !strings.Contains(msg, tt.contains) {
				t.Errorf("expected error to contain %q, got %q", tt.contains, msg)
			}
		})
	}
}

func TestErrors_Unwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := NewCLIConnectionError("wrapper", cause)

	unwrapped := err.Unwrap()
	if unwrapped != cause {
		t.Errorf("expected unwrapped error to be the cause, got %v", unwrapped)
	}
}

func TestErrors_Fields(t *testing.T) {
	t.Run("CLINotFoundError_CLIPath", func(t *testing.T) {
		err := NewCLINotFoundError("not found", "/custom/path")
		if err.CLIPath != "/custom/path" {
			t.Errorf("expected CLIPath '/custom/path', got %s", err.CLIPath)
		}
	})

	t.Run("ProcessError_ExitCode", func(t *testing.T) {
		err := NewProcessError("failed", 137, "killed")
		if err.ExitCode != 137 {
			t.Errorf("expected ExitCode 137, got %d", err.ExitCode)
		}
		if err.Stderr != "killed" {
			t.Errorf("expected Stderr 'killed', got %s", err.Stderr)
		}
	})

	t.Run("JSONDecodeError_Line", func(t *testing.T) {
		orig := fmt.Errorf("parse error")
		err := NewJSONDecodeError("bad", "{bad", orig)
		if err.Line != "{bad" {
			t.Errorf("expected Line '{bad', got %s", err.Line)
		}
		if err.OriginalError != orig {
			t.Errorf("expected OriginalError to match")
		}
	})

	t.Run("MessageParseError_Data", func(t *testing.T) {
		data := map[string]any{"foo": "bar"}
		err := NewMessageParseError("parse failed", data)
		if err.Data["foo"] != "bar" {
			t.Errorf("expected Data[foo] = bar")
		}
	})
}

// ---------------------------------------------------------------------------
// Real CLI tests (skipped if claude is not installed)
// ---------------------------------------------------------------------------

func TestSubprocessTransport_RealCLI_Version(t *testing.T) {
	cliPath, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude CLI not found in PATH")
	}

	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		CLIPath: cliPath,
	}, "sdk-go-test")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := tr.checkVersion(ctx, cliPath); err != nil {
		t.Fatalf("checkVersion with real CLI failed: %v", err)
	}
}

func TestQuery_RealCLI(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found in PATH")
	}
	if os.Getenv("AGENTSDK_TEST_REAL_CLI") == "" {
		t.Skip("set AGENTSDK_TEST_REAL_CLI=1 to run real CLI tests")
	}

	maxTurns := 1
	opts := &ClaudeAgentOptions{
		MaxTurns:       &maxTurns,
		PermissionMode: PermissionModePlan,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages, errFn := Query(ctx, "Respond with exactly: HELLO_FROM_TEST", opts)

	var gotResult bool
	for msg := range messages {
		if _, ok := msg.(*ResultMessage); ok {
			gotResult = true
		}
	}

	if err := errFn(); err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if !gotResult {
		t.Error("expected a ResultMessage from real CLI")
	}
}

func TestNewClient_RealCLI(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found in PATH")
	}
	if os.Getenv("AGENTSDK_TEST_REAL_CLI") == "" {
		t.Skip("set AGENTSDK_TEST_REAL_CLI=1 to run real CLI tests")
	}

	maxTurns := 1
	client := NewClient(&ClaudeAgentOptions{
		MaxTurns:       &maxTurns,
		PermissionMode: PermissionModePlan,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect with real CLI failed: %v", err)
	}
	defer client.Close()

	info := client.ServerInfo()
	if info == nil {
		t.Error("expected non-nil ServerInfo from real CLI")
	}

	if err := client.SendQuery(ctx, "Respond with exactly: HELLO_FROM_TEST", "default"); err != nil {
		t.Fatalf("SendQuery failed: %v", err)
	}

	var gotResult bool
	for msg := range client.ReceiveResponse() {
		if _, ok := msg.(*ResultMessage); ok {
			gotResult = true
		}
	}

	if !gotResult {
		t.Error("expected a ResultMessage from real CLI")
	}
}
