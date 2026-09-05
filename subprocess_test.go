package agentsdk

import (
	"strings"
	"testing"
)

func TestBuildArgs_BaseFlags(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{}, "sdk-go")
	args := tr.BuildArgs()

	expected := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json"}
	for _, e := range expected {
		if !containsArg(args, e) {
			t.Errorf("expected args to contain %q", e)
		}
	}
}

func TestBuildArgs_SystemPromptString(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		SystemPrompt: "You are a helpful assistant",
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--system-prompt", "You are a helpful assistant")
}

func TestBuildArgs_SystemPromptPreset(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		SystemPrompt: &SystemPromptPreset{
			Type:   "preset",
			Preset: "claude_code",
			Append: "Be concise.",
		},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--append-system-prompt", "Be concise.")
}

func TestBuildArgs_SystemPromptPresetNoAppend(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		SystemPrompt: &SystemPromptPreset{
			Type:   "preset",
			Preset: "claude_code",
		},
	}, "sdk-go")
	args := tr.BuildArgs()

	if containsArg(args, "--append-system-prompt") {
		t.Error("expected no --append-system-prompt for a preset without an append")
	}
	if containsArg(args, "--system-prompt") {
		t.Error("expected no --system-prompt for a preset")
	}
}

func TestBuildArgs_ToolsList(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Tools: []string{"Read", "Write", "Bash"},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--tools", "Read,Write,Bash")
}

func TestBuildArgs_ToolsPreset(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Tools: &ToolsPreset{Type: "preset", Preset: "claude_code"},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--tools", "default")
}

func TestBuildArgs_Model(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Model: "claude-sonnet-4-5-20250514",
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--model", "claude-sonnet-4-5-20250514")
}

func TestBuildArgs_MaxTurns(t *testing.T) {
	turns := 5
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		MaxTurns: &turns,
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--max-turns", "5")
}

func TestBuildArgs_PermissionMode(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		PermissionMode: PermissionModeBypassPermissions,
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--permission-mode", "bypassPermissions")
}

func TestBuildArgs_Continue(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		ContinueConversation: true,
	}, "sdk-go")
	args := tr.BuildArgs()

	if !containsArg(args, "--continue") {
		t.Error("expected --continue flag")
	}
}

func TestBuildArgs_Resume(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Resume: "session-123",
	}, "sdk-go")
	args := tr.BuildArgs()

	// The equals form binds the value so it can never be parsed as a
	// separate flag.
	assertArgContains(t, args, "--resume=session-123")
}

func TestBuildArgs_AllowedTools(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		AllowedTools: []string{"Read", "Bash"},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--allowedTools", "Read,Bash")
}

func TestBuildArgs_DisallowedTools(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		DisallowedTools: []string{"Write"},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--disallowedTools", "Write")
}

func TestBuildArgs_Betas(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Betas: []SdkBeta{SdkBetaContext1M},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--betas", "context-1m-2025-08-07")
}

func TestBuildArgs_Effort(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Effort: EffortHigh,
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--effort", "high")
}

func TestBuildArgs_IncludePartialMessages(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		IncludePartialMessages: true,
	}, "sdk-go")
	args := tr.BuildArgs()

	if !containsArg(args, "--include-partial-messages") {
		t.Error("expected --include-partial-messages flag")
	}
}

func TestBuildArgs_ForkSession(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		ForkSession: true,
	}, "sdk-go")
	args := tr.BuildArgs()

	if !containsArg(args, "--fork-session") {
		t.Error("expected --fork-session flag")
	}
}

func TestBuildArgs_AddDirs(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		AddDirs: []string{"/path/one", "/path/two"},
	}, "sdk-go")
	args := tr.BuildArgs()

	// Should have two --add-dir flags
	count := 0
	for _, a := range args {
		if a == "--add-dir" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 --add-dir flags, got %d", count)
	}
}

func TestBuildArgs_Plugins(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Plugins: []PluginConfig{
			{Type: "local", Path: "/plugins/my-plugin"},
		},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--plugin-dir", "/plugins/my-plugin")
}

func TestBuildArgs_SettingSources(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		SettingSources: []SettingSource{SettingSourceUser, SettingSourceProject},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgContains(t, args, "--setting-sources=user,project")
}

func TestBuildArgs_SettingSourcesNil(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{}, "sdk-go")
	args := tr.BuildArgs()

	// An unset SettingSources omits the flag so the CLI applies its own
	// defaults, rather than sending an empty value that disables them.
	for _, arg := range args {
		if strings.HasPrefix(arg, "--setting-sources") {
			t.Errorf("expected no --setting-sources flag, got %q", arg)
		}
	}
}

func TestBuildArgs_SettingSourcesEmpty(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		SettingSources: []SettingSource{},
	}, "sdk-go")
	args := tr.BuildArgs()

	// An empty non-nil slice disables filesystem settings.
	assertArgContains(t, args, "--setting-sources=")
}

func TestBuildArgs_ExtraArgs(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		ExtraArgs: map[string]string{
			"debug-to-stderr": "",
			"custom-flag":     "value",
		},
	}, "sdk-go")
	args := tr.BuildArgs()

	if !containsArg(args, "--debug-to-stderr") {
		t.Error("expected --debug-to-stderr flag")
	}
	assertArgPair(t, args, "--custom-flag", "value")
}

func TestBuildArgs_McpServersString(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		McpServers: "/path/to/mcp-config.json",
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--mcp-config", "/path/to/mcp-config.json")
}

func TestBuildArgs_McpServersMap(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		McpServers: map[string]McpServerConfig{
			"my-server": McpStdioServerConfig{
				Type:    "stdio",
				Command: "node",
				Args:    []string{"server.js"},
			},
		},
	}, "sdk-go")
	args := tr.BuildArgs()

	// Should have --mcp-config with JSON
	idx := indexOf(args, "--mcp-config")
	if idx == -1 || idx+1 >= len(args) {
		t.Fatal("expected --mcp-config flag with value")
	}
	value := args[idx+1]
	if !strings.Contains(value, "mcpServers") {
		t.Errorf("expected mcp config JSON, got %s", value)
	}
}

func TestBuildArgs_ThinkingAdaptive(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Thinking: ThinkingConfigAdaptive{},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--thinking", "adaptive")
}

func TestBuildArgs_ThinkingAdaptiveWithDisplay(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Thinking: ThinkingConfigAdaptive{Display: ThinkingDisplaySummarized},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--thinking", "adaptive")
	assertArgPair(t, args, "--thinking-display", "summarized")
}

func TestBuildArgs_ThinkingEnabled(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Thinking: ThinkingConfigEnabled{BudgetTokens: 16000},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--max-thinking-tokens", "16000")
}

func TestBuildArgs_ThinkingDisabled(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Thinking: ThinkingConfigDisabled{},
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--thinking", "disabled")
}

func TestBuildArgs_MaxThinkingTokensLegacy(t *testing.T) {
	tokens := 8000
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		MaxThinkingTokens: &tokens,
	}, "sdk-go")
	args := tr.BuildArgs()

	assertArgPair(t, args, "--max-thinking-tokens", "8000")
}

func TestBuildArgs_ThinkingOverridesLegacy(t *testing.T) {
	tokens := 8000
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		MaxThinkingTokens: &tokens,
		Thinking:          ThinkingConfigDisabled{},
	}, "sdk-go")
	args := tr.BuildArgs()

	// The thinking config takes precedence over the deprecated budget.
	assertArgPair(t, args, "--thinking", "disabled")
	if containsArg(args, "--max-thinking-tokens") {
		t.Error("expected no --max-thinking-tokens when Thinking is set")
	}
}

func TestBuildArgs_OutputFormat(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		OutputFormat: &OutputFormat{
			Type: "json_schema",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"answer": map[string]any{"type": "string"},
				},
			},
		},
	}, "sdk-go")
	args := tr.BuildArgs()

	idx := indexOf(args, "--json-schema")
	if idx == -1 {
		t.Fatal("expected --json-schema flag")
	}
}

func TestBuildSettings_OnlySettings(t *testing.T) {
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Settings: "/path/to/settings.json",
	}, "sdk-go")
	result := tr.buildSettings()
	if result != "/path/to/settings.json" {
		t.Errorf("expected '/path/to/settings.json', got %s", result)
	}
}

func TestBuildSettings_OnlySandbox(t *testing.T) {
	enabled := true
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Sandbox: &SandboxSettings{Enabled: &enabled},
	}, "sdk-go")
	result := tr.buildSettings()
	if !strings.Contains(result, "sandbox") {
		t.Errorf("expected sandbox in settings, got %s", result)
	}
}

func TestBuildSettings_BothSettingsAndSandbox(t *testing.T) {
	enabled := true
	tr := NewSubprocessTransport(&ClaudeAgentOptions{
		Settings: `{"key": "value"}`,
		Sandbox:  &SandboxSettings{Enabled: &enabled},
	}, "sdk-go")
	result := tr.buildSettings()
	if !strings.Contains(result, "sandbox") || !strings.Contains(result, "key") {
		t.Errorf("expected merged settings, got %s", result)
	}
}

func TestIsVersionAtLeast(t *testing.T) {
	tests := []struct {
		version  string
		minimum  string
		expected bool
	}{
		{"2.0.0", "2.0.0", true},
		{"2.1.0", "2.0.0", true},
		{"3.0.0", "2.0.0", true},
		{"1.9.9", "2.0.0", false},
		{"2.0.0", "2.0.1", false},
		{"2.0.10", "2.0.9", true},
	}

	for _, tt := range tests {
		result := isVersionAtLeast(tt.version, tt.minimum)
		if result != tt.expected {
			t.Errorf("isVersionAtLeast(%q, %q) = %v, want %v", tt.version, tt.minimum, result, tt.expected)
		}
	}
}

// helpers

func containsArg(args []string, arg string) bool {
	for _, a := range args {
		if a == arg {
			return true
		}
	}
	return false
}

func indexOf(args []string, arg string) int {
	for i, a := range args {
		if a == arg {
			return i
		}
	}
	return -1
}

func assertArgPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	idx := indexOf(args, flag)
	if idx == -1 {
		t.Errorf("expected flag %q in args: %v", flag, args)
		return
	}
	if idx+1 >= len(args) {
		t.Errorf("flag %q has no value", flag)
		return
	}
	if args[idx+1] != value {
		t.Errorf("expected %q %q, got %q %q", flag, value, flag, args[idx+1])
	}
}

// assertArgContains fails unless args contains an exact match for want.
func assertArgContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if arg == want {
			return
		}
	}
	t.Errorf("expected argument %q in %v", want, args)
}
