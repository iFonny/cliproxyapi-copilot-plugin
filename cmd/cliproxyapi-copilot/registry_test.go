package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

type storeRegistry struct {
	SchemaVersion int           `json:"schema_version"`
	Plugins       []storePlugin `json:"plugins"`
}

type storePlugin struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Repository  string `json:"repository"`
	Install     struct {
		Type string `json:"type"`
	} `json:"install"`
}

// CLIProxyAPI derives the plugin ID from the installed library filename and
// looks for a release asset named "<id>_<version>_<goos>_<goarch>.zip", so a
// registry entry that disagrees with the build produces an install that either
// fails to download or loads under the wrong ID.
func TestStoreRegistryMatchesTheBuild(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../registry.json")
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var registry storeRegistry
	if errDecode := json.Unmarshal(raw, &registry); errDecode != nil {
		t.Fatalf("decode registry: %v", errDecode)
	}
	if registry.SchemaVersion != 1 && registry.SchemaVersion != 2 {
		t.Fatalf("schema_version = %d, want 1 or 2", registry.SchemaVersion)
	}
	if len(registry.Plugins) != 1 {
		t.Fatalf("registry declares %d plugins, want 1", len(registry.Plugins))
	}

	plugin := registry.Plugins[0]
	if want := pluginArtifactID(t); plugin.ID != want {
		t.Errorf("registry id = %q, want %q from the Makefile artifact", plugin.ID, want)
	}
	if plugin.Install.Type != "github-release" {
		t.Errorf("install type = %q, want github-release", plugin.Install.Type)
	}
	if want := pluginRegistration().Metadata.GitHubRepository; plugin.Repository != want {
		t.Errorf("registry repository = %q, want %q from the plugin metadata", plugin.Repository, want)
	}
	for name, value := range map[string]string{
		"name":        plugin.Name,
		"description": plugin.Description,
		"author":      plugin.Author,
	} {
		if value == "" {
			t.Errorf("registry entry is missing the required %s field", name)
		}
	}
}

func pluginArtifactID(t *testing.T) string {
	t.Helper()

	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	match := regexp.MustCompile(`(?m)^PLUGIN_SO := \$\(PLUGIN_DIR\)/(.+)\.so$`).FindSubmatch(makefile)
	if match == nil {
		t.Fatal("Makefile does not declare PLUGIN_SO")
	}
	return string(match[1])
}
