package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestClassifyDeviceToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		status        int
		token         oauthTokenResponse
		wantStatus    pluginapi.AuthLoginStatus
		wantTerminal  bool
		wantInterval  time.Duration
		messageNeedle string
	}{
		{
			name:         "success",
			status:       200,
			token:        oauthTokenResponse{AccessToken: " token-value "},
			wantStatus:   pluginapi.AuthLoginStatusSuccess,
			wantTerminal: true,
		},
		{
			name:         "authorization pending",
			status:       200,
			token:        oauthTokenResponse{Error: "authorization_pending"},
			wantStatus:   pluginapi.AuthLoginStatusPending,
			wantInterval: 5 * time.Second,
		},
		{
			name:         "slow down",
			status:       200,
			token:        oauthTokenResponse{Error: "slow_down", Interval: 12},
			wantStatus:   pluginapi.AuthLoginStatusPending,
			wantInterval: 12 * time.Second,
		},
		{
			name:          "access denied",
			status:        400,
			token:         oauthTokenResponse{Error: "access_denied", ErrorDescription: "the user declined"},
			wantStatus:    pluginapi.AuthLoginStatusError,
			wantTerminal:  true,
			messageNeedle: "user declined",
		},
		{
			name:          "server error remains retryable",
			status:        503,
			token:         oauthTokenResponse{},
			wantStatus:    pluginapi.AuthLoginStatusError,
			wantTerminal:  false,
			messageNeedle: "503",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := classifyDeviceToken(test.status, test.token, 5*time.Second)
			if got.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, test.wantStatus)
			}
			if got.Terminal != test.wantTerminal {
				t.Fatalf("terminal = %v, want %v", got.Terminal, test.wantTerminal)
			}
			if got.NextInterval != test.wantInterval {
				t.Fatalf("next interval = %s, want %s", got.NextInterval, test.wantInterval)
			}
			if test.messageNeedle != "" && !strings.Contains(got.Message, test.messageNeedle) {
				t.Fatalf("message %q does not contain %q", got.Message, test.messageNeedle)
			}
			if test.wantStatus == pluginapi.AuthLoginStatusSuccess {
				if got.Token == nil || got.Token.AccessToken != "token-value" {
					t.Fatalf("success token was not normalized")
				}
			}
		})
	}
}

func TestParseAuthExposesPriorityAttribute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		rawJSON        string
		wantPriority   string
		wantMetaPrio   any
		wantHandled    bool
		wantAuthKind   string
		expectPriority bool
	}{
		{
			name:           "numeric priority",
			rawJSON:        `{"type":"copilot","github_access_token":"gh-token","github_login":"alice","priority":100,"updated_at":"2026-01-01T00:00:00Z"}`,
			wantPriority:   "100",
			wantMetaPrio:   100,
			wantHandled:    true,
			wantAuthKind:   "oauth",
			expectPriority: true,
		},
		{
			name:           "string priority",
			rawJSON:        `{"type":"copilot","github_access_token":"gh-token","github_login":"alice","priority":"42","updated_at":"2026-01-01T00:00:00Z"}`,
			wantPriority:   "42",
			wantMetaPrio:   42,
			wantHandled:    true,
			wantAuthKind:   "oauth",
			expectPriority: true,
		},
		{
			name:           "negative priority",
			rawJSON:        `{"type":"copilot","github_access_token":"gh-token","github_login":"alice","priority":-1,"updated_at":"2026-01-01T00:00:00Z"}`,
			wantPriority:   "-1",
			wantMetaPrio:   -1,
			wantHandled:    true,
			wantAuthKind:   "oauth",
			expectPriority: true,
		},
		{
			name:           "missing priority",
			rawJSON:        `{"type":"copilot","github_access_token":"gh-token","github_login":"alice","updated_at":"2026-01-01T00:00:00Z"}`,
			wantHandled:    true,
			wantAuthKind:   "oauth",
			expectPriority: false,
		},
		{
			name:           "invalid priority ignored",
			rawJSON:        `{"type":"copilot","github_access_token":"gh-token","github_login":"alice","priority":"not-a-number","updated_at":"2026-01-01T00:00:00Z"}`,
			wantHandled:    true,
			wantAuthKind:   "oauth",
			expectPriority: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			svc := New(nil)
			resp, err := svc.ParseAuth(pluginapi.AuthParseRequest{
				Provider: providerID,
				FileName: "copilot-alice.json",
				RawJSON:  []byte(test.rawJSON),
			})
			if err != nil {
				t.Fatalf("ParseAuth: %v", err)
			}
			if resp.Handled != test.wantHandled {
				t.Fatalf("Handled = %v, want %v", resp.Handled, test.wantHandled)
			}
			if resp.Auth.Attributes["auth_kind"] != test.wantAuthKind {
				t.Fatalf("auth_kind = %q, want %q", resp.Auth.Attributes["auth_kind"], test.wantAuthKind)
			}
			gotPriority, hasPriority := resp.Auth.Attributes["priority"]
			if test.expectPriority {
				if !hasPriority {
					t.Fatalf("Attributes missing priority; got %#v", resp.Auth.Attributes)
				}
				if gotPriority != test.wantPriority {
					t.Fatalf("Attributes[priority] = %q, want %q", gotPriority, test.wantPriority)
				}
				if resp.Auth.Metadata["priority"] != test.wantMetaPrio {
					t.Fatalf("Metadata[priority] = %#v, want %#v", resp.Auth.Metadata["priority"], test.wantMetaPrio)
				}
			} else if hasPriority {
				t.Fatalf("unexpected Attributes[priority] = %q", gotPriority)
			}
		})
	}
}

func TestRefreshAuthPreservesPriority(t *testing.T) {
	t.Parallel()

	storage := authStorage{
		Type:              providerID,
		GitHubAccessToken: "gh-token",
		GitHubLogin:       "alice",
		// Far-future expiry so RefreshAuth takes the no-network early path.
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, errMarshal := marshalStorage(storage)
	if errMarshal != nil {
		t.Fatalf("marshal storage: %v", errMarshal)
	}

	svc := New(nil)
	resp, err := svc.RefreshAuth(context.Background(), "callback", pluginapi.AuthRefreshRequest{
		AuthID:      "copilot-alice.json",
		StorageJSON: raw,
		Metadata: map[string]any{
			"type":         providerID,
			"github_login": "alice",
			"priority":     100,
		},
		Attributes: map[string]string{
			"auth_kind": "oauth",
			"priority":  "100",
		},
	})
	if err != nil {
		t.Fatalf("RefreshAuth: %v", err)
	}
	if got := resp.Auth.Attributes["priority"]; got != "100" {
		t.Fatalf("Attributes[priority] = %q, want %q", got, "100")
	}
	if got := resp.Auth.Metadata["priority"]; got != 100 {
		t.Fatalf("Metadata[priority] = %#v, want 100", got)
	}
}

func TestRefreshAuthSyncsPriorityFromMetadata(t *testing.T) {
	t.Parallel()

	storage := authStorage{
		Type:              providerID,
		GitHubAccessToken: "gh-token",
		GitHubLogin:       "alice",
		ExpiresAt:         time.Now().Add(24 * time.Hour).Unix(),
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	raw, errMarshal := marshalStorage(storage)
	if errMarshal != nil {
		t.Fatalf("marshal storage: %v", errMarshal)
	}

	svc := New(nil)
	resp, err := svc.RefreshAuth(context.Background(), "callback", pluginapi.AuthRefreshRequest{
		AuthID:      "copilot-alice.json",
		StorageJSON: raw,
		Metadata: map[string]any{
			"type":         providerID,
			"github_login": "alice",
			"priority":     float64(75),
		},
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	})
	if err != nil {
		t.Fatalf("RefreshAuth: %v", err)
	}
	if got := resp.Auth.Attributes["priority"]; got != "75" {
		t.Fatalf("Attributes[priority] = %q, want %q", got, "75")
	}
}

func TestParsePriorityValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		raw    any
		want   int
		wantOK bool
	}{
		{name: "float64", raw: float64(100), want: 100, wantOK: true},
		{name: "int", raw: 7, want: 7, wantOK: true},
		{name: "string", raw: "9", want: 9, wantOK: true},
		{name: "nil", raw: nil, wantOK: false},
		{name: "bad string", raw: "x", wantOK: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parsePriorityValue(test.raw)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if test.wantOK && got != test.want {
				t.Fatalf("priority = %d, want %d", got, test.want)
			}
		})
	}
}
