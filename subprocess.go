package agentsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
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

	// done is closed once the process has been reaped. Only one waiter
	// reaps it: the ReadMessages iterator when one is running, and Close
	// otherwise. reaping guards that choice, and doneOnce keeps the close
	// idempotent.
	done     chan struct{}
	doneOnce sync.Once
	reaping  atomic.Bool
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
	if err := ValidateSkills(t.opts.Skills); err != nil {
		return err
	}
	if err := ValidateSessionStoreOptions(t.opts); err != nil {
		return err
	}

	cliPath, err := t.findCLI()
	if err != nil {
		return err
	}

	if os.Getenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK") == "" {
		if err := t.checkVersion(ctx, cliPath); err != nil {
			warning := fmt.Sprintf("warning: CLI at %s: %v", cliPath, err)
			if t.opts.Stderr != nil {
				t.opts.Stderr(warning)
			} else {
				log.Print("agentsdk: " + warning)
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
	// Pipe stderr only when the caller registered a callback; otherwise let
	// it flow through to the parent's stderr.
	if t.opts.Stderr != nil {
		t.stderr, err = t.cmd.StderrPipe()
		if err != nil {
			return NewCLIConnectionError("failed to create stderr pipe", err)
		}
	}

	if err := t.cmd.Start(); err != nil {
		return NewCLIConnectionError("failed to start CLI process", err)
	}

	if t.stderr != nil {
		go t.readStderr()
	}

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

			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			// Skip non-JSON output such as "[SandboxDebug] ..." that some
			// CLI builds write to stdout. Only do so when not mid-parse,
			// since a continuation line need not start with a brace;
			// otherwise such a line corrupts the buffer.
			if buffer.Len() == 0 && !strings.HasPrefix(line, "{") {
				continue
			}

			// Try the line by itself first.
			if buffer.Len() == 0 {
				var msg map[string]any
				if err := json.Unmarshal([]byte(line), &msg); err == nil {
					if !yield(msg, nil) {
						return
					}
					continue
				}
			}

			// Accumulate partial JSON.
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

		// Wait for process exit after stdout closes. Claim the reap so a
		// concurrent Close does not call Wait on the same process.
		if t.cmd != nil && t.reaping.CompareAndSwap(false, true) {
			if err := t.cmd.Wait(); err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					yield(nil, NewProcessError("CLI process exited with error", exitErr.ExitCode(), ""))
				}
			}
			t.markDone()
		}
	}
}

// closeGracePeriod bounds each stage of subprocess shutdown.
const closeGracePeriod = 5 * time.Second

// Close terminates the CLI subprocess.
//
// It closes stdin first and waits for the process to exit on its own: the
// subprocess needs time to flush its session file after receiving EOF, and
// without that grace period a signal can interrupt the write and lose the
// last assistant message. If it does not exit it is sent SIGTERM, and if a
// blocked SIGTERM handler leaves it running it is finally killed.
func (t *SubprocessTransport) Close() error {
	t.ready = false
	if t.stdin != nil {
		t.stdin.Close()
	}
	if t.cmd == nil || t.cmd.Process == nil {
		return nil
	}

	// With no ReadMessages iterator running there is nobody to reap the
	// process, so Close does it: waiting on done would otherwise stall for
	// every grace period and then return with the process still unreaped.
	if t.reaping.CompareAndSwap(false, true) {
		go func() {
			_ = t.cmd.Wait()
			t.markDone()
		}()
	}

	// Stage 1: wait for graceful exit after stdin EOF.
	if t.waitForExit(closeGracePeriod) {
		return nil
	}

	// Stage 2: ask the process to terminate.
	_ = t.cmd.Process.Signal(syscall.SIGTERM)
	if t.waitForExit(closeGracePeriod) {
		return nil
	}

	// Stage 3: the SIGTERM handler is blocked, so force a kill. Bound this
	// wait too: when no ReadMessages iterator is running there is nobody to
	// reap the process and close done, and Close must not hang.
	_ = t.cmd.Process.Kill()
	t.waitForExit(closeGracePeriod)
	return nil
}

// markDone closes the done channel exactly once.
func (t *SubprocessTransport) markDone() {
	t.doneOnce.Do(func() { close(t.done) })
}

// waitForExit reports whether the process exited within the timeout,
// signaled by whichever of the ReadMessages iterator or Close reaped it.
func (t *SubprocessTransport) waitForExit(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
		return true
	case <-timer.C:
		return false
	}
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
		return fmt.Errorf(
			"Claude Code version %s is unsupported in the Agent SDK; minimum required version is %s, so some features may not work correctly",
			version, minimumCLIVersion)
	}
	return nil
}

// BuildArgs constructs CLI arguments from options.
func (t *SubprocessTransport) BuildArgs() []string {
	opts := t.opts
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json"}

	// System prompt
	switch sp := opts.SystemPrompt.(type) {
	case nil:
		// An explicit empty prompt suppresses the CLI's default.
		args = append(args, "--system-prompt", "")
	case string:
		args = append(args, "--system-prompt", sp)
	case *SystemPromptFile:
		args = append(args, "--system-prompt-file", sp.Path)
	case *SystemPromptPreset:
		if sp.Append != "" {
			args = append(args, "--append-system-prompt", sp.Append)
		}
	}

	// Tools
	switch tools := opts.Tools.(type) {
	case []string:
		// An empty non-nil slice disables all built-in tools.
		args = append(args, "--tools", strings.Join(tools, ","))
	case *ToolsPreset:
		// The claude_code preset maps to the CLI's "default".
		args = append(args, "--tools", "default")
	}

	effectiveAllowedTools, effectiveSettingSources := t.applySkillsDefaults()

	if len(effectiveAllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(effectiveAllowedTools, ","))
	}

	if len(opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(opts.DisallowedTools, ","))
	}

	if opts.TaskBudget != nil {
		args = append(args, "--task-budget", strconv.Itoa(opts.TaskBudget.Total))
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

	// Pass these as --flag=value rather than as two argv tokens. The CLI
	// declares --resume with an optional value, so in the two-token form a
	// dash-leading value is not bound to the flag and is parsed as a
	// separate flag instead, letting an untrusted value inject arbitrary
	// flags. The equals form always binds the value.
	if opts.Resume != "" {
		args = append(args, "--resume="+opts.Resume)
	}

	if opts.SessionID != "" {
		args = append(args, "--session-id="+opts.SessionID)
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

	if opts.IncludeHookEvents {
		args = append(args, "--include-hook-events")
	}

	if opts.StrictMcpConfig {
		args = append(args, "--strict-mcp-config")
	}

	if opts.ForkSession {
		args = append(args, "--fork-session")
	}

	// Equals form so the value can never be parsed as a separate flag, even
	// if the CLI's declaration of these options changes.
	if opts.ResumeSessionAt != "" {
		args = append(args, "--resume-session-at="+opts.ResumeSessionAt)
	}

	// Non-nil rather than non-empty: an empty string is forwarded so the CLI
	// rejects it as a malformed declaration, instead of the SDK silently
	// disarming a guard the caller believes is armed.
	if opts.ResumeDropsTurn != nil {
		args = append(args, "--resume-drops-turn="+*opts.ResumeDropsTurn)
	}

	if opts.SessionStore != nil {
		args = append(args, "--session-mirror")
	}

	// Agents always ride on the initialize request, matching the TypeScript
	// SDK, so there is no --agents flag here.

	// Omit the flag entirely when unset so the CLI applies its own defaults;
	// an empty non-nil slice still disables filesystem settings.
	if effectiveSettingSources != nil {
		args = append(args, "--setting-sources="+strings.Join(effectiveSettingSources, ","))
	}

	for _, p := range opts.Plugins {
		args = append(args, "--plugin-dir", p.Path)
	}

	args = append(args, buildThinkingArgs(opts)...)

	if opts.Effort != "" {
		args = append(args, "--effort", string(opts.Effort))
	}

	// Extract the schema from the output_format structure, which the CLI
	// expects as {"type": "json_schema", "schema": {...}}.
	if opts.OutputFormat != nil && opts.OutputFormat.Type == "json_schema" && opts.OutputFormat.Schema != nil {
		if schemaJSON, err := json.Marshal(opts.OutputFormat.Schema); err == nil {
			args = append(args, "--json-schema", string(schemaJSON))
		}
	}

	for _, k := range sortedKeys(opts.ExtraArgs) {
		v := opts.ExtraArgs[k]
		switch {
		case v == "":
			// Boolean flag without a value.
			args = append(args, "--"+k)
		case strings.HasPrefix(v, "-"):
			// In the two-token form a dash-leading value is not bound to
			// its flag when the CLI declares the option with an optional
			// value: it parses as a separate flag instead, the same
			// injection the --resume change above closes. The equals form
			// always binds.
			args = append(args, "--"+k+"="+v)
		default:
			args = append(args, "--"+k, v)
		}
	}

	return args
}

// buildEnv merges the environment for the CLI subprocess.
//
// CLAUDE_CODE_ENTRYPOINT defaults to the transport's entrypoint regardless
// of the inherited process environment, and options.Env can override it.
// CLAUDE_AGENT_SDK_VERSION is always set by the SDK. CLAUDECODE is filtered
// out so SDK-spawned subprocesses do not think they are running inside a
// Claude Code parent.
//
// Unlike the Python SDK there is no OpenTelemetry span injection here, since
// that would require a dependency outside the standard library: TRACEPARENT
// and TRACESTATE are inherited from the process environment if set, and can
// be overridden through options.Env.
func (t *SubprocessTransport) buildEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && name == "CLAUDECODE" {
			continue
		}
		env = append(env, kv)
	}

	env = append(env, "CLAUDE_CODE_ENTRYPOINT="+t.entrypoint)

	for k, v := range t.opts.Env {
		env = append(env, k+"="+v)
	}

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

// buildThinkingArgs resolves the thinking configuration into CLI flags.
// Thinking takes precedence over the deprecated MaxThinkingTokens.
func buildThinkingArgs(opts *ClaudeAgentOptions) []string {
	if opts.Thinking == nil {
		if opts.MaxThinkingTokens != nil {
			return []string{"--max-thinking-tokens", strconv.Itoa(*opts.MaxThinkingTokens)}
		}
		return nil
	}

	var args []string
	var display ThinkingDisplay
	switch tc := opts.Thinking.(type) {
	case ThinkingConfigAdaptive:
		args = append(args, "--thinking", "adaptive")
		display = tc.Display
	case ThinkingConfigEnabled:
		args = append(args, "--max-thinking-tokens", strconv.Itoa(tc.BudgetTokens))
		display = tc.Display
	case ThinkingConfigDisabled:
		args = append(args, "--thinking", "disabled")
	}
	if display != "" {
		args = append(args, "--thinking-display", string(display))
	}
	return args
}

// sortedKeys returns a map's keys in sorted order, so generated argument
// lists are deterministic.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// skillNameInvalidChars rejects delimiters to the --allowedTools tokenizer
// and control characters, which never appear in a skill directory name.
// U+FEFF is here rather than with the whitespace check below because the CLI
// trims it as whitespace and Go's strings.TrimSpace does not.
var skillNameInvalidChars = regexp.MustCompile("[(),\u0000-\u001f\u007f-\u009f\ufeff]")

// validateSkillName rejects skill names that cannot ride safely in a
// Skill(name) rule.
//
// Names from options.Skills are formatted into the --allowedTools value,
// which the CLI splits into rules on commas and spaces outside parentheses.
// That tokenizer does not honor escape sequences — escaping exists only in
// the per-rule grammar, applied after splitting — so a name carrying a
// delimiter cannot be passed through reliably: what it tokenizes into
// depends on what surrounds it.
//
// Names that tokenize cleanly but can never match the listed skill are
// rejected too, so a dead rule fails loudly here instead of silently
// granting nothing.
func validateSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return NewValidationError("skill names must be non-empty strings")
	}
	if !utf8.ValidString(name) {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: contains invalid UTF-8, which can never match a skill the CLI discovered", name))
	}
	if name != strings.TrimSpace(name) {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: leading or trailing whitespace can never match, since the Skill tool trims the invoked name", name))
	}
	if skillNameInvalidChars.MatchString(name) {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: parentheses, commas, control characters, and byte-order marks are not allowed. "+
				"Names match the skill's directory name, or 'plugin:skill' for plugin-qualified skills", name))
	}
	if name == "*" {
		return NewValidationError(`invalid skill name "*": use Skills: SkillsAll{} to enable every skill`)
	}
	if strings.HasSuffix(name, ":*") || strings.HasSuffix(name, " *") {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: wildcard-suffix names are not allowed; list each skill by its exact name", name))
	}
	if strings.HasPrefix(name, "/") {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: skill names may not start with '/'. The Skills option takes the canonical name, not the slash-command form", name))
	}
	if strings.Contains(name, `\\`) {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: consecutive backslashes are not allowed, since the per-rule parser collapses them and the rule would name a different skill", name))
	}
	if strings.HasSuffix(name, `\`) {
		return NewValidationError(fmt.Sprintf(
			"invalid skill name %q: names may not end with an unpaired backslash", name))
	}
	return nil
}

// ValidateSkills checks the Skills option, returning an error for a value
// that is neither nil, SkillsAll, nor a []string of valid skill names. It is
// called before subprocess spawn so misconfiguration fails fast.
func ValidateSkills(skills any) error {
	switch v := skills.(type) {
	case nil, SkillsAll:
		return nil
	case []string:
		for _, name := range v {
			if err := validateSkillName(name); err != nil {
				return err
			}
		}
		return nil
	case string:
		return NewValidationError(fmt.Sprintf(
			"ClaudeAgentOptions.Skills must be []string or SkillsAll{}, got string %q. Did you mean []string{%q}?", v, v))
	default:
		return NewValidationError(fmt.Sprintf(
			"ClaudeAgentOptions.Skills must be []string or SkillsAll{}, got %T", skills))
	}
}

// applySkillsDefaults computes the effective allowed tools and setting
// sources for the Skills option.
//
// SkillsAll injects the bare Skill tool; a []string injects Skill(name) for
// each entry. In either case setting sources default to user and project
// when unset, so the CLI discovers installed skills without the caller
// wiring up both options. A nil Skills value is a no-op.
//
// It does not mutate the options. Invalid skill names are dropped here;
// ValidateSkills reports them before spawn.
func (t *SubprocessTransport) applySkillsDefaults() (allowedTools []string, settingSources []string) {
	opts := t.opts

	allowedTools = append(allowedTools, opts.AllowedTools...)
	if opts.SettingSources != nil {
		settingSources = make([]string, len(opts.SettingSources))
		for i, src := range opts.SettingSources {
			settingSources[i] = string(src)
		}
	}

	if opts.Skills == nil {
		return allowedTools, settingSources
	}

	switch skills := opts.Skills.(type) {
	case SkillsAll:
		if !slices.Contains(allowedTools, "Skill") {
			allowedTools = append(allowedTools, "Skill")
		}
	case []string:
		for _, name := range skills {
			if validateSkillName(name) != nil {
				continue
			}
			pattern := "Skill(" + name + ")"
			if !slices.Contains(allowedTools, pattern) {
				allowedTools = append(allowedTools, pattern)
			}
		}
	default:
		return allowedTools, settingSources
	}

	if settingSources == nil {
		settingSources = []string{"user", "project"}
	}
	return allowedTools, settingSources
}

// ValidateSessionStoreOptions rejects invalid SessionStore option
// combinations. It is called before subprocess spawn so misconfiguration
// fails fast instead of surfacing as a confusing mid-session error.
func ValidateSessionStoreOptions(opts *ClaudeAgentOptions) error {
	if opts == nil || opts.SessionStore == nil {
		return nil
	}

	if opts.ContinueConversation && opts.Resume == "" {
		// When Resume is set, ListSessions is provably never called, since
		// resume wins over continue, so a minimal store is fine there.
		if _, ok := opts.SessionStore.(SessionLister); !ok {
			return NewValidationError(
				"ContinueConversation with SessionStore requires the store to implement SessionLister")
		}
	}

	if opts.EnableFileCheckpointing {
		return NewValidationError(
			"SessionStore cannot be combined with EnableFileCheckpointing, since checkpoints are local-disk only " +
				"and would diverge from the mirrored transcript")
	}

	return nil
}
