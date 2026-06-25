package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearBlitzEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"BLITZ_PROVIDER", "BLITZ_MODEL", "BLITZ_BASE_URL", "BLITZ_PROMPT",
		"BLITZ_REASONING_EFFORT", "BLITZ_HOME",
		"BLITZ_SKILLS_DIR", "CODEX_HOME",
	} {
		t.Setenv(key, "")
	}
}

func TestResolveCodexURL(t *testing.T) {
	tests := map[string]string{
		"":                                      "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api":       "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api/codex": "https://chatgpt.com/backend-api/codex/responses",
		"https://example.test/codex/responses":  "https://example.test/codex/responses",
		"https://example.test/base///":          "https://example.test/base/codex/responses",
	}
	for input, want := range tests {
		if got := resolveCodexURL(input); got != want {
			t.Fatalf("resolveCodexURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCodexRequestBodyUsesMessageListInput(t *testing.T) {
	body := codexRequestBody(config{
		model:           "gpt-5.4-mini",
		prompt:          "Improve it",
		input:           "Tell me a joke",
		stream:          true,
		reasoningEffort: "low",
	})

	input, ok := body["input"].([]map[string]any)
	if !ok {
		t.Fatalf("input has type %T, want []map[string]any", body["input"])
	}
	if len(input) != 1 || input[0]["role"] != "user" {
		t.Fatalf("input = %#v, want one user message", input)
	}
	content, ok := input[0]["content"].([]map[string]string)
	if !ok {
		t.Fatalf("content has type %T, want []map[string]string", input[0]["content"])
	}
	if len(content) != 1 || content[0]["type"] != "input_text" || content[0]["text"] != "Tell me a joke" {
		t.Fatalf("content = %#v, want input_text input", content)
	}
}

func TestDefaultSettingsUseGPT55WithoutReasoning(t *testing.T) {
	clearBlitzEnv(t)
	cfg, err := parseRunFlags([]string{"--blitz-home", t.TempDir(), "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.model != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", cfg.model)
	}
	if cfg.reasoningEffort != "" {
		t.Fatalf("reasoning = %q, want disabled", cfg.reasoningEffort)
	}
	body := codexRequestBody(cfg)
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("body included reasoning: %#v", body["reasoning"])
	}
	if cfg.stream {
		t.Fatalf("stream = true, want default false")
	}
}

func TestConfigFileOverridesDefaults(t *testing.T) {
	clearBlitzEnv(t)
	blitzHome := t.TempDir()
	model := "gpt-5.4-mini"
	reasoning := "low"
	if err := saveSettings(settingsPath(blitzHome), userSettings{Model: &model, ReasoningEffort: &reasoning}); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseRunFlags([]string{"--blitz-home", blitzHome, "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.model != model || cfg.reasoningEffort != reasoning {
		t.Fatalf("model/reasoning = %q/%q, want %q/%q", cfg.model, cfg.reasoningEffort, model, reasoning)
	}
}

func TestFastDefaultsToPriorityForCodex(t *testing.T) {
	clearBlitzEnv(t)
	cfg, err := parseRunFlags([]string{"--blitz-home", t.TempDir(), "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	body := codexRequestBody(cfg)
	if got := body["service_tier"]; got != "priority" {
		t.Fatalf("service_tier = %#v, want priority", got)
	}
}

func TestPrintStatusShowsDefaults(t *testing.T) {
	clearBlitzEnv(t)
	cfg, skills, err := statusConfig([]string{"--blitz-home", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printStatus(&out, cfg, skills)
	got := out.String()
	if !strings.Contains(got, "model: gpt-5.5") || !strings.Contains(got, "reasoning: off") || !strings.Contains(got, "stream: false") {
		t.Fatalf("status output missing defaults:\n%s", got)
	}
}

func TestParseRunFlagsLoadsSkillPromptByMarkdownFilename(t *testing.T) {
	clearBlitzEnv(t)
	blitzHome := t.TempDir()
	skillsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillsDir, "transcript.md"), []byte("Clean up this transcript."), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := parseRunFlags([]string{"--blitz-home", blitzHome, "--skills-dir", skillsDir, "--transcript", "Hello world ähm"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.skill != "transcript" {
		t.Fatalf("skill = %q, want transcript", cfg.skill)
	}
	if cfg.prompt != "Clean up this transcript." {
		t.Fatalf("prompt = %q", cfg.prompt)
	}
	if cfg.input != "Hello world ähm" {
		t.Fatalf("input = %q", cfg.input)
	}
}

func TestParseRunFlagsRejectsSkillAndPromptTogether(t *testing.T) {
	clearBlitzEnv(t)
	blitzHome := t.TempDir()
	skillsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillsDir, "transcript.md"), []byte("Clean up this transcript."), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := parseRunFlags([]string{"--blitz-home", blitzHome, "--skills-dir", skillsDir, "--transcript", "--prompt", "Custom prompt", "Hello"})
	if err == nil || !strings.Contains(err.Error(), "either a skill flag or -prompt") {
		t.Fatalf("err = %v, want skill/prompt conflict", err)
	}
}

func TestReadSSEExtractsResponsesAndChatDeltas(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		``,
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	var out bytes.Buffer
	if err := readSSE(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Hello world\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestExtractFinalText(t *testing.T) {
	got := extractFinalText(map[string]any{
		"output": []any{
			map[string]any{
				"content": []any{
					map[string]any{"text": "clean transcript"},
				},
			},
		},
	})
	if got != "clean transcript" {
		t.Fatalf("got %q", got)
	}
}
