package main

import (
	"slices"
	"testing"
)

// A format the plugin does not declare makes the host fall back to any other
// declared format it can transform into, which silently yields an empty
// response body. Chat Completions clients therefore have to be declared.
func TestRegistrationDeclaresEveryServedFormat(t *testing.T) {
	t.Parallel()

	capabilities := pluginRegistration().Capabilities
	for _, format := range []string{"openai", "openai-response", "claude"} {
		if !slices.Contains(capabilities.ExecutorInputFormats, format) {
			t.Errorf("executor input formats omit %q: %v", format, capabilities.ExecutorInputFormats)
		}
		if !slices.Contains(capabilities.ExecutorOutputFormats, format) {
			t.Errorf("executor output formats omit %q: %v", format, capabilities.ExecutorOutputFormats)
		}
	}
}
