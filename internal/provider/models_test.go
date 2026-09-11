package provider

import (
	"testing"

	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/translate"
)

func TestSelectEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		model        upstreamModel
		sourceFormat string
		want         string
		wantError    bool
	}{
		{
			name:         "responses preferred",
			model:        upstreamModel{ID: "model-a", SupportedEndpoints: []string{"/chat/completions", "/responses"}},
			sourceFormat: "openai-response",
			want:         translate.EndpointResponses,
		},
		{
			name:         "messages fallback",
			model:        upstreamModel{ID: "model-b", SupportedEndpoints: []string{"messages"}},
			sourceFormat: "openai-response",
			want:         translate.EndpointMessages,
		},
		{
			name:         "chat client prefers chat completions",
			model:        upstreamModel{ID: "model-a", SupportedEndpoints: []string{"/chat/completions", "/responses"}},
			sourceFormat: "openai",
			want:         translate.EndpointChatCompletions,
		},
		{
			name:         "chat client falls back to responses",
			model:        upstreamModel{ID: "model-c", SupportedEndpoints: []string{"/responses"}},
			sourceFormat: "openai",
			want:         translate.EndpointResponses,
		},
		{
			name:         "claude client keeps responses preference",
			model:        upstreamModel{ID: "model-a", SupportedEndpoints: []string{"/chat/completions", "/responses"}},
			sourceFormat: "claude",
			want:         translate.EndpointResponses,
		},
		{
			name:         "sol forced to responses",
			model:        upstreamModel{ID: "gpt-5.6-sol", SupportedEndpoints: []string{"/chat/completions"}},
			sourceFormat: "openai-response",
			want:         translate.EndpointResponses,
		},
		{
			name:         "sol forced to responses for chat client",
			model:        upstreamModel{ID: "gpt-5.6-sol", SupportedEndpoints: []string{"/chat/completions"}},
			sourceFormat: "openai",
			want:         translate.EndpointResponses,
		},
		{
			name:         "terra forced to responses",
			model:        upstreamModel{ID: "GPT-5.6-TERRA"},
			sourceFormat: "openai-response",
			want:         translate.EndpointResponses,
		},
		{
			name:         "unsupported",
			model:        upstreamModel{ID: "embedding-model", SupportedEndpoints: []string{"/embeddings"}},
			sourceFormat: "openai",
			wantError:    true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := selectEndpoint(test.model, test.sourceFormat)
			if test.wantError {
				if err == nil {
					t.Fatal("expected an endpoint selection error")
				}
				return
			}
			if err != nil {
				t.Fatalf("select endpoint: %v", err)
			}
			if got != test.want {
				t.Fatalf("endpoint = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeModelsAddsResponsesMetadata(t *testing.T) {
	t.Parallel()

	models := normalizeModels([]upstreamModel{
		{
			ID:                 "gpt-5.6-sol",
			SupportedEndpoints: []string{"/chat/completions"},
			Capabilities: modelCapabilities{
				Supports: modelSupports{Streaming: true, ToolCalls: true, Vision: true},
				Limits:   modelLimits{MaxPromptTokens: 100, MaxOutputTokens: 20},
			},
		},
	})
	if len(models) != 1 || !contains(models[0].SupportedEndpoints, translate.EndpointResponses) {
		t.Fatalf("responses endpoint was not added: %#v", models)
	}
	info := modelInfos(models)[0]
	if !contains(info.SupportedGenerationMethods, translate.EndpointResponses) {
		t.Fatalf("model metadata omits responses endpoint: %#v", info.SupportedGenerationMethods)
	}
	if !contains(info.SupportedInputModalities, "IMAGE") {
		t.Fatalf("model metadata omits image support: %#v", info.SupportedInputModalities)
	}
}

func TestFilterModelsExcludesConfiguredPrefixes(t *testing.T) {
	t.Parallel()

	models := filterModels([]upstreamModel{
		{ID: "gpt-5.6-sol"},
		{ID: "claude-sonnet-5"},
		{ID: "Claude-Haiku-4.5"},
	}, []string{"claude-"})
	if len(models) != 1 || models[0].ID != "gpt-5.6-sol" {
		t.Fatalf("filtered models = %#v", models)
	}
}

func TestNormalizeModelPrefixes(t *testing.T) {
	t.Parallel()

	got := normalizeModelPrefixes([]string{" Claude- ", "claude-", "", "GPT-"})
	if len(got) != 2 || got[0] != "claude-" || got[1] != "gpt-" {
		t.Fatalf("normalized prefixes = %#v", got)
	}
}
