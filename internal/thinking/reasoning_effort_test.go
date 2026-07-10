package thinking

import "testing"

func TestExtractReasoningEffortOpenAICompatiblePayloads(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		body     []byte
		want     string
	}{
		{
			name:     "chat completions native effort",
			provider: "openai",
			model:    "gpt-5.4",
			body:     []byte(`{"reasoning_effort":"low"}`),
			want:     "low",
		},
		{
			name:     "responses native effort",
			provider: "openai-response",
			model:    "gpt-5.4",
			body:     []byte(`{"reasoning":{"effort":"medium"}}`),
			want:     "medium",
		},
		{
			name:     "responses accepts top level OpenAI effort",
			provider: "openai-response",
			model:    "gpt-5.4",
			body:     []byte(`{"reasoning_effort":"high"}`),
			want:     "high",
		},
		{
			name:     "model suffix overrides body",
			provider: "openai",
			model:    "alias(max)",
			body:     []byte(`{"reasoning_effort":"low"}`),
			want:     "max",
		},
		{
			name:     "claude style budget for chat completions",
			provider: "openai",
			model:    "gpt-5.4",
			body:     []byte(`{"thinking":{"type":"enabled","budget_tokens":24576}}`),
			want:     "high",
		},
		{
			name:     "claude style budget for responses",
			provider: "openai-response",
			model:    "gpt-5.4",
			body:     []byte(`{"thinking":{"type":"enabled","budget_tokens":24576}}`),
			want:     "high",
		},
		{
			name:     "adaptive output effort for chat completions",
			provider: "openai",
			model:    "gpt-5.4",
			body:     []byte(`{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "max",
		},
		{
			name:     "adaptive output effort for responses",
			provider: "openai-response",
			model:    "gpt-5.4",
			body:     []byte(`{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "max",
		},
		{
			name:     "native effort overrides claude compatibility fallback",
			provider: "openai",
			model:    "gpt-5.4",
			body:     []byte(`{"reasoning_effort":"low","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractReasoningEffort(tt.body, tt.provider, tt.model); got != tt.want {
				t.Fatalf("ExtractReasoningEffort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTranslatedReasoningEffortOpenAICompatibleFallback(t *testing.T) {
	if got := ExtractTranslatedReasoningEffort(
		[]byte(`{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
		"openai-response",
	); got != "max" {
		t.Fatalf("ExtractTranslatedReasoningEffort() = %q, want %q", got, "max")
	}
}

func TestExtractReasoningEffortOpenAICompatiblePriorityAndFallback(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		body     []byte
		want     string
	}{
		{
			name:     "chat blank native effort falls back to claude thinking",
			provider: "openai",
			body:     []byte(`{"reasoning_effort":" \t ","thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`),
			want:     "high",
		},
		{
			name:     "responses blank native effort falls back to claude thinking",
			provider: "openai-response",
			body:     []byte(`{"reasoning":{"effort":" \t "},"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`),
			want:     "high",
		},
		{
			name:     "responses native effort overrides claude thinking",
			provider: "openai-response",
			body:     []byte(`{"reasoning":{"effort":"low"},"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "low",
		},
		{
			name:     "chat native none blocks claude fallback",
			provider: "openai",
			body:     []byte(`{"reasoning_effort":"none","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "none",
		},
		{
			name:     "chat native auto blocks claude fallback",
			provider: "openai",
			body:     []byte(`{"reasoning_effort":"auto","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "auto",
		},
		{
			name:     "responses native none blocks claude fallback",
			provider: "openai-response",
			body:     []byte(`{"reasoning":{"effort":"none"},"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "none",
		},
		{
			name:     "responses native auto blocks claude fallback",
			provider: "openai-response",
			body:     []byte(`{"reasoning":{"effort":"auto"},"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
			want:     "auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractReasoningEffort(tt.body, tt.provider, "gpt-5.4"); got != tt.want {
				t.Fatalf("ExtractReasoningEffort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTranslatedReasoningEffortOpenAICompatibleBlankNativeFallsBack(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		body     []byte
	}{
		{
			name:     "chat completions",
			provider: "openai",
			body:     []byte(`{"reasoning_effort":" ","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
		},
		{
			name:     "responses",
			provider: "openai-response",
			body:     []byte(`{"reasoning":{"effort":" \t "},"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTranslatedReasoningEffort(tt.body, tt.provider); got != "max" {
				t.Fatalf("ExtractTranslatedReasoningEffort() = %q, want %q", got, "max")
			}
		})
	}
}
