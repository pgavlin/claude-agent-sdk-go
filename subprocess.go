package agentsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const (
	minimumCLIVersion = "2.0.0"
	defaultMaxBuffer  = 1024 * 1024 // 1 MB
	sdkVersion        = "0.1.0"
)

// SubprocessTransport spawns the Claude CLI as a subprocess and communicates
// over stdin/stdout using newline-delimited JSON.
type SubprocessTransport struct {
	opts       *ClaudeAgentOptions
	entrypoint string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	writeMu sync.Mutex
	ready   bool
	done    chan struct{}
}

// NewSubprocessTransport creates a new subprocess transport.
// entrypoint identifies the SDK entry (e.g. "sdk-go" or "sdk-go-client").
func NewSubprocessTransport(opts *ClaudeAgentOptions, entrypoint string) *SubprocessTransport {
	if opts == nil {
		opts = &ClaudeAgentOptions{}
	}
	if entrypoint == "" {
		entrypoint = "sdk-go"
	}
	return &SubprocessTransport{
		opts:       opts,
		entrypoint: entrypoint,
		done:       make(chan struct{}),
	}
}

// Connect starts the CLI subprocess.
func (t *SubprocessTransport) Connect(ctx context.Context) error {
	cliPath, err := t.findCLI()
	if err != nil {
		return err
	}

	if os.Getenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK") == "" {
		if err := t.checkVersion(ctx, cliPath); err != nil {
			if t.opts.Stderr != nil {
				t.opts.Stderr(fmt.Sprintf("warning: CLI version check failed: %v", err))
			}
		}
	}

	args := t.BuildArgs()
	t.cmd = exec.CommandContext(ctx, cliPath, args...)
	t.cmd.Env = t.buildEnv()
	if t.opts.CWD != "" {
		t.cmd.Dir = t.opts.CWD
	}

	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return NewCLIConnectionError("failed to create stdin pipe", err)
	}
	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return NewCLIConnectionError("failed to create stdout pipe", err)
	}
	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return NewCLIConnectionError("failed to create stderr pipe", err)
	}

	if err := t.cmd.Start(); err != nil {
		return NewCLIConnectionError("failed to start CLI process", err)
	}

	go t.readStderr()

	t.ready = true
	return nil
}

// Write sends a JSON line to the CLI's stdin.
func (t *SubprocessTransport) Write(ctx context.Context, data string) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.stdin == nil {
		return NewCLIConnectionError("transport not connected", nil)
	}

	line := data
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}

	_, err := io.WriteString(t.stdin, line)
	if err != nil {
		return NewCLIConnectionError("failed to write to stdin", err)
	}
	return nil
}

// ReadMessages returns an iterator over parsed JSON messages from stdout.
func (t *SubprocessTransport) ReadMessages(ctx context.Context) iter.Seq2[map[string]any, error] {
	return func(yield func(map[string]any, error) bool) {
		maxBuf := t.opts.MaxBufferSize
		if maxBuf <= 0 {
			maxBuf = defaultMaxBuffer
		}

		scanner := bufio.NewScanner(t.stdout)
		scanner.Buffer(make([]byte, 0, maxBuf), maxBuf)

		var buffer strings.Builder
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}

			// Try the line by itself first
			if buffer.Len() == 0 {
				var msg map[string]any
				if err := json.Unmarshal([]byte(line), &msg); err == nil {
					if !yield(msg, nil) {
						return
					}
					continue
				}
			}

			// Accumulate partial JSON
			buffer.WriteString(line)

			var msg map[string]any
			if err := json.Unmarshal([]byte(buffer.String()), &msg); err == nil {
				if !yield(msg, nil) {
					return
				}
				buffer.Reset()
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, NewJSONDecodeError("error reading CLI stdout", "", err))
			return
		}

		if buffer.Len() > 0 {
			var msg map[string]any
			if err := json.Unmarshal([]byte(buffer.String()), &msg); err != nil {
				yield(nil, NewJSONDecodeError("incomplete JSON in buffer", buffer.String(), err))
			} else {
				yield(msg, nil)
			}
		}

		// Wait for process exit after stdout closes
		if t.cmd != nil {
			if err := t.cmd.Wait(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					yield(nil, NewProcessError("CLI process exited with error", exitErr.ExitCode(), ""))
				}
			}
			close(t.done)
		}
	}
}

// Close terminates the CLI subprocess.
func (t *SubprocessTransport) Close() error {
	t.ready = false
	if t.stdin != nil {
		t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
		<-t.done
	}
	return nil
}

// EndInput closes the stdin pipe, signaling no more input.
func (t *SubprocessTransport) EndInput() error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin != nil {
		return t.stdin.Close()
	}
	return nil
}

// IsReady reports whether the transport is connected.
func (t *SubprocessTransport) IsReady() bool {
	return t.ready
}

func (t *SubprocessTransport) readStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if t.opts.Stderr != nil {
			t.opts.Stderr(line)
		}
	}
}

func (t *SubprocessTransport) findCLI() (string, error) {
	if t.opts.CLIPath != "" {
		if _, err := os.Stat(t.opts.CLIPath); err == nil {
			return t.opts.CLIPath, nil
		}
		return "", NewCLINotFoundError(
			fmt.Sprintf("CLI not found at specified path: %s", t.opts.CLIPath),
			t.opts.CLIPath,
		)
	}

	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".npm-global", "bin", "claude"),
		"/usr/local/bin/claude",
	}
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData != "" {
			candidates = append(candidates, filepath.Join(appData, "npm", "claude.cmd"))
		}
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", NewCLINotFoundError("claude CLI not found in PATH or common locations", "")
}

func (t *SubprocessTransport) checkVersion(ctx context.Context, cliPath string) error {
	cmd := exec.CommandContext(ctx, cliPath, "-v")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check CLI version: %w", err)
	}

	version := strings.TrimSpace(string(out))
	if !isVersionAtLeast(version, minimumCLIVersion) {
		return fmt.Errorf("CLI version %s is below minimum %s", version, minimumCLIVersion)
	}
	return nil
}

// BuildArgs constructs CLI arguments from options.
func (t *SubprocessTransport) BuildArgs() []string {
	opts := t.opts
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json"}

	// System prompt
	switch sp := opts.SystemPrompt.(type) {
	case string:
		args = append(args, "--system-prompt", sp)
	case *SystemPromptPreset:
		if sp.Append != "" {
			args = append(args, "--append-system-prompt", sp.Append)
		}
	case nil:
		// No system prompt override
	}

	// Tools
	switch tools := opts.Tools.(type) {
	case []string:
		args = append(args, "--tools", strings.Join(tools, ","))
	case *ToolsPreset:
		args = append(args, "--tools", "default")
	}

	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}

	if len(opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(opts.DisallowedTools, ","))
	}

	if opts.MaxTurns != nil {
		args = append(args, "--max-turns", strconv.Itoa(*opts.MaxTurns))
	}

	if opts.MaxBudgetUSD != nil {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(*opts.MaxBudgetUSD, 'f', -1, 64))
	}

	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}

	if opts.FallbackModel != "" {
		args = append(args, "--fallback-model", opts.FallbackModel)
	}

	if len(opts.Betas) > 0 {
		betaStrs := make([]string, len(opts.Betas))
		for i, b := range opts.Betas {
			betaStrs[i] = string(b)
		}
		args = append(args, "--betas", strings.Join(betaStrs, ","))
	}

	if opts.PermissionPromptToolName != "" {
		args = append(args, "--permission-prompt-tool", opts.PermissionPromptToolName)
	}

	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", string(opts.PermissionMode))
	}

	if opts.ContinueConversation {
		args = append(args, "--continue")
	}

	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	}

	settings := t.buildSettings()
	if settings != "" {
		args = append(args, "--settings", settings)
	}

	for _, dir := range opts.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	switch mcp := opts.McpServers.(type) {
	case map[string]McpServerConfig:
		if len(mcp) > 0 {
			mcpJSON := buildMCPConfig(mcp)
			args = append(args, "--mcp-config", mcpJSON)
		}
	case string:
		if mcp != "" {
			args = append(args, "--mcp-config", mcp)
		}
	}

	if opts.IncludePartialMessages {
		args = append(args, "--include-partial-messages")
	}

	if opts.ForkSession {
		args = append(args, "--fork-session")
	}

	if opts.SettingSources != nil {
		sources := make([]string, len(opts.SettingSources))
		for i, s := range opts.SettingSources {
			sources[i] = string(s)
		}
		args = append(args, "--setting-sources", strings.Join(sources, ","))
	} else {
		args = append(args, "--setting-sources", "")
	}

	for _, p := range opts.Plugins {
		args = append(args, "--plugin-dir", p.Path)
	}

	thinkingTokens := resolveThinkingTokens(opts)
	if thinkingTokens != nil {
		args = append(args, "--max-thinking-tokens", strconv.Itoa(*thinkingTokens))
	}

	if opts.Effort != "" {
		args = append(args, "--effort", string(opts.Effort))
	}

	if opts.OutputFormat != nil && opts.OutputFormat.JSONSchema != nil {
		schemaJSON, err := json.Marshal(opts.OutputFormat.JSONSchema)
		if err == nil {
			args = append(args, "--json-schema", string(schemaJSON))
		}
	}

	for k, v := range opts.ExtraArgs {
		if v != "" {
			args = append(args, "--"+k, v)
		} else {
			args = append(args, "--"+k)
		}
	}

	return args
}

func (t *SubprocessTransport) buildEnv() []string {
	env := os.Environ()

	for k, v := range t.opts.Env {
		env = append(env, k+"="+v)
	}

	env = append(env, "CLAUDE_CODE_ENTRYPOINT="+t.entrypoint)
	env = append(env, "CLAUDE_AGENT_SDK_VERSION="+sdkVersion)

	if t.opts.CWD != "" {
		env = append(env, "PWD="+t.opts.CWD)
	}

	if t.opts.EnableFileCheckpointing {
		env = append(env, "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true")
	}

	return env
}

func (t *SubprocessTransport) buildSettings() string {
	opts := t.opts
	if opts.Settings == "" && opts.Sandbox == nil {
		return ""
	}

	if opts.Settings != "" && opts.Sandbox == nil {
		return opts.Settings
	}

	if opts.Sandbox != nil && opts.Settings == "" {
		settingsMap := map[string]any{"sandbox": opts.Sandbox}
		data, err := json.Marshal(settingsMap)
		if err != nil {
			return ""
		}
		return string(data)
	}

	if strings.HasPrefix(strings.TrimSpace(opts.Settings), "{") {
		var settingsMap map[string]any
		if err := json.Unmarshal([]byte(opts.Settings), &settingsMap); err == nil {
			settingsMap["sandbox"] = opts.Sandbox
			data, err := json.Marshal(settingsMap)
			if err == nil {
				return string(data)
			}
		}
	}

	return opts.Settings
}

func buildMCPConfig(servers map[string]McpServerConfig) string {
	mcpServers := make(map[string]any)
	for name, cfg := range servers {
		mcpServers[name] = cfg
	}
	wrapper := map[string]any{"mcpServers": mcpServers}
	data, err := json.Marshal(wrapper)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func resolveThinkingTokens(opts *ClaudeAgentOptions) *int {
	if opts.Thinking != nil {
		switch tc := opts.Thinking.(type) {
		case ThinkingConfigAdaptive:
			_ = tc
			tokens := 32000
			return &tokens
		case ThinkingConfigEnabled:
			return &tc.BudgetTokens
		case ThinkingConfigDisabled:
			_ = tc
			tokens := 0
			return &tokens
		}
	}
	return opts.MaxThinkingTokens
}

func isVersionAtLeast(version, minimum string) bool {
	vParts := parseSemver(version)
	mParts := parseSemver(minimum)

	for i := 0; i < 3; i++ {
		v, m := 0, 0
		if i < len(vParts) {
			v = vParts[i]
		}
		if i < len(mParts) {
			m = mParts[i]
		}
		if v > m {
			return true
		}
		if v < m {
			return false
		}
	}
	return true
}

func parseSemver(s string) []int {
	parts := strings.SplitN(s, ".", 3)
	result := make([]int, 0, 3)
	for _, p := range parts {
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		result = append(result, n)
	}
	return result
}
