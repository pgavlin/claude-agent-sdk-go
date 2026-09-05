package agentsdk

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func buildArgsFor(opts *ClaudeAgentOptions) []string {
	return NewSubprocessTransport(opts, "sdk-go").BuildArgs()
}

func TestBuildArgs_SystemPromptFile(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{
		SystemPrompt: &SystemPromptFile{Type: "file", Path: "/etc/prompt.md"},
	})
	assertArgPair(t, args, "--system-prompt-file", "/etc/prompt.md")
}

func TestBuildArgs_SystemPromptNilSendsEmpty(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{})
	// An explicit empty prompt suppresses the CLI's default.
	assertArgPair(t, args, "--system-prompt", "")
}

func TestBuildArgs_ToolsEmptyDisablesAll(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{Tools: []string{}})
	assertArgPair(t, args, "--tools", "")
}

func TestBuildArgs_SessionAndBudgetFlags(t *testing.T) {
	dropped := "prompt-uuid"
	args := buildArgsFor(&ClaudeAgentOptions{
		SessionID:         testSessionID,
		TaskBudget:        &TaskBudget{Total: 50000},
		ResumeSessionAt:   testUUIDA,
		ResumeDropsTurn:   &dropped,
		StrictMcpConfig:   true,
		IncludeHookEvents: true,
	})

	assertArgContains(t, args, "--session-id="+testSessionID)
	assertArgPair(t, args, "--task-budget", "50000")
	assertArgContains(t, args, "--resume-session-at="+testUUIDA)
	assertArgContains(t, args, "--resume-drops-turn=prompt-uuid")
	if !containsArg(args, "--strict-mcp-config") {
		t.Error("expected --strict-mcp-config")
	}
	if !containsArg(args, "--include-hook-events") {
		t.Error("expected --include-hook-events")
	}
}

func TestBuildArgs_ResumeDropsTurnEmptyIsForwarded(t *testing.T) {
	empty := ""
	args := buildArgsFor(&ClaudeAgentOptions{ResumeDropsTurn: &empty})
	// An empty value is forwarded so the CLI rejects it, rather than the SDK
	// silently disarming a guard the caller believes is armed.
	assertArgContains(t, args, "--resume-drops-turn=")
}

func TestBuildArgs_SessionMirror(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{SessionStore: NewInMemorySessionStore()})
	if !containsArg(args, "--session-mirror") {
		t.Error("expected --session-mirror when a session store is configured")
	}
}

func TestBuildArgs_SkillsAll(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{Skills: SkillsAll{}})

	assertArgPair(t, args, "--allowedTools", "Skill")
	// Setting sources default to user and project so the CLI discovers
	// installed skills without the caller wiring up both options.
	assertArgContains(t, args, "--setting-sources=user,project")
}

func TestBuildArgs_SkillsList(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{
		AllowedTools: []string{"Read"},
		Skills:       []string{"pdf", "plugin:docx"},
	})

	idx := indexOf(args, "--allowedTools")
	if idx == -1 {
		t.Fatal("expected --allowedTools")
	}
	got := args[idx+1]
	for _, want := range []string{"Read", "Skill(pdf)", "Skill(plugin:docx)"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in the allowed tools %q", want, got)
		}
	}
}

func TestBuildArgs_SkillsRespectsExplicitSettingSources(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{
		Skills:         SkillsAll{},
		SettingSources: []SettingSource{SettingSourceLocal},
	})
	assertArgContains(t, args, "--setting-sources=local")
}

func TestBuildArgs_SkillsNilIsNoOp(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{AllowedTools: []string{"Read"}})

	assertArgPair(t, args, "--allowedTools", "Read")
	for _, arg := range args {
		if strings.HasPrefix(arg, "--setting-sources") {
			t.Errorf("expected no setting-sources default without Skills, got %q", arg)
		}
	}
}

func TestValidateSkills(t *testing.T) {
	if err := ValidateSkills(nil); err != nil {
		t.Errorf("expected nil Skills to validate, got %v", err)
	}
	if err := ValidateSkills(SkillsAll{}); err != nil {
		t.Errorf("expected SkillsAll to validate, got %v", err)
	}
	if err := ValidateSkills([]string{"pdf", "plugin:docx"}); err != nil {
		t.Errorf("expected valid names to validate, got %v", err)
	}

	// A bare string is the most likely mistake, so it gets a pointed message.
	err := ValidateSkills("pdf")
	if err == nil {
		t.Fatal("expected a bare string to be rejected")
	}
	if !strings.Contains(err.Error(), `[]string{"pdf"}`) {
		t.Errorf("expected a suggestion in the error, got %v", err)
	}

	for _, bad := range []string{
		"",
		"   ",
		" pdf",
		"pdf ",
		"has(paren)",
		"has,comma",
		"*",
		"plugin:*",
		"/slash-command",
		`trailing\`,
		`double\\backslash`,
	} {
		if err := ValidateSkills([]string{bad}); err == nil {
			t.Errorf("expected skill name %q to be rejected", bad)
		}
	}
}

func TestBuildArgs_ExtraArgsDashLeadingValue(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{
		ExtraArgs: map[string]string{"some-flag": "-danger"},
	})
	// The equals form binds the value so it cannot be parsed as a flag.
	assertArgContains(t, args, "--some-flag=-danger")
}

func TestBuildArgs_ExtraArgsAreDeterministic(t *testing.T) {
	opts := &ClaudeAgentOptions{
		ExtraArgs: map[string]string{"zebra": "1", "alpha": "2", "middle": "3"},
	}
	first := buildArgsFor(opts)
	for i := 0; i < 5; i++ {
		if !slices.Equal(first, buildArgsFor(opts)) {
			t.Fatal("expected BuildArgs to be deterministic across calls")
		}
	}
}

func TestBuildArgs_OutputFormatIgnoresOtherTypes(t *testing.T) {
	args := buildArgsFor(&ClaudeAgentOptions{
		OutputFormat: &OutputFormat{Type: "text", Schema: map[string]any{"type": "object"}},
	})
	if containsArg(args, "--json-schema") {
		t.Error("expected no --json-schema for a non-json_schema output format")
	}
}

func TestValidateSessionStoreOptions(t *testing.T) {
	store := NewInMemorySessionStore()

	if err := ValidateSessionStoreOptions(&ClaudeAgentOptions{}); err != nil {
		t.Errorf("expected no error without a store, got %v", err)
	}
	if err := ValidateSessionStoreOptions(&ClaudeAgentOptions{
		SessionStore: store, ContinueConversation: true,
	}); err != nil {
		t.Errorf("expected a full-capability store to satisfy continue, got %v", err)
	}

	// Continue needs listing, unless an explicit resume makes it moot.
	err := ValidateSessionStoreOptions(&ClaudeAgentOptions{
		SessionStore: newMinimalStore(), ContinueConversation: true,
	})
	if err == nil {
		t.Error("expected an error for continue with a store that cannot list")
	}
	if err := ValidateSessionStoreOptions(&ClaudeAgentOptions{
		SessionStore: newMinimalStore(), ContinueConversation: true, Resume: testSessionID,
	}); err != nil {
		t.Errorf("expected an explicit resume to bypass the listing requirement, got %v", err)
	}

	// Checkpointing is local-disk only and would diverge from the mirror.
	err = ValidateSessionStoreOptions(&ClaudeAgentOptions{
		SessionStore: store, EnableFileCheckpointing: true,
	})
	if err == nil {
		t.Error("expected an error combining a session store with file checkpointing")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("expected a ValidationError, got %T", err)
	}
}

// stubCanUseTool is a no-op permission callback for option tests.
func stubCanUseTool(context.Context, string, map[string]any, ToolPermissionContext) (PermissionResult, error) {
	return PermissionAllow{}, nil
}

func TestConfigureCanUseTool(t *testing.T) {
	opts := &ClaudeAgentOptions{CanUseTool: stubCanUseTool}
	if err := configureCanUseTool(opts); err != nil {
		t.Fatalf("configureCanUseTool failed: %v", err)
	}
	if opts.PermissionPromptToolName != "stdio" {
		t.Errorf("expected permission prompts to route over stdio, got %q", opts.PermissionPromptToolName)
	}

	conflicting := &ClaudeAgentOptions{CanUseTool: stubCanUseTool, PermissionPromptToolName: "my_tool"}
	if err := configureCanUseTool(conflicting); err == nil {
		t.Error("expected an error when both CanUseTool and PermissionPromptToolName are set")
	}

	if err := configureCanUseTool(&ClaudeAgentOptions{}); err != nil {
		t.Errorf("expected a no-op without a callback, got %v", err)
	}
}

func TestCanUseToolShadowedWarning(t *testing.T) {
	if got := CanUseToolShadowedWarning(&ClaudeAgentOptions{}); got != "" {
		t.Errorf("expected no warning without a callback, got %q", got)
	}

	bypass := CanUseToolShadowedWarning(&ClaudeAgentOptions{
		CanUseTool: stubCanUseTool, PermissionMode: PermissionModeBypassPermissions,
	})
	if !strings.Contains(bypass, "bypassPermissions") {
		t.Errorf("expected a bypassPermissions warning, got %q", bypass)
	}

	// Entries that allow a whole tool shadow the callback; a real specifier
	// does not.
	shadowed := CanUseToolShadowedWarning(&ClaudeAgentOptions{
		CanUseTool:   stubCanUseTool,
		AllowedTools: []string{"Read", "Read()", "Write(*)", "Bash(ls:*)"},
	})
	for _, want := range []string{"Read", "Write"} {
		if !strings.Contains(shadowed, want) {
			t.Errorf("expected %q in the warning %q", want, shadowed)
		}
	}
	if strings.Contains(shadowed, "Bash") {
		t.Errorf("expected a specifier rule not to shadow, got %q", shadowed)
	}
	// Read appears twice in the input but must be reported once.
	if strings.Count(shadowed, "Read") != 1 {
		t.Errorf("expected Read to be reported once, got %q", shadowed)
	}

	// SkillsAll appends a bare Skill entry, so it shadows too.
	skills := CanUseToolShadowedWarning(&ClaudeAgentOptions{
		CanUseTool: stubCanUseTool, Skills: SkillsAll{},
	})
	if !strings.Contains(skills, "Skill") {
		t.Errorf("expected SkillsAll to shadow the callback, got %q", skills)
	}

	// A named skill list produces Skill(name) specifiers, which do not shadow.
	named := CanUseToolShadowedWarning(&ClaudeAgentOptions{
		CanUseTool: stubCanUseTool, Skills: []string{"pdf"},
	})
	if named != "" {
		t.Errorf("expected named skills not to shadow, got %q", named)
	}
}

func TestWholeToolAllowed(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Read", "Read"},
		{"Read()", "Read"},
		{"Read(*)", "Read"},
		{"Bash(ls:*)", ""},
		{"", ""},
		{"   ", ""},
		{"(malformed)", ""},
		{"Unterminated(", ""},
	} {
		if got := wholeToolAllowed(tc.in); got != tc.want {
			t.Errorf("wholeToolAllowed(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadTimeoutResolution(t *testing.T) {
	if got := (&ClaudeAgentOptions{}).loadTimeoutMS(); got != defaultLoadTimeoutMS {
		t.Errorf("expected the default timeout, got %d", got)
	}
	if got := (&ClaudeAgentOptions{LoadTimeoutMS: 5000}).loadTimeoutMS(); got != 5000 {
		t.Errorf("expected the configured timeout, got %d", got)
	}
	if got := (&ClaudeAgentOptions{LoadTimeoutMS: LoadTimeoutImmediate}).loadTimeoutMS(); got != 0 {
		t.Errorf("expected an immediate timeout, got %d", got)
	}
}
