package kit

import (
	"runtime/debug"
	"strings"
	"testing"
)

// TestVersionOverride covers the deterministic surface: the override arg and the fallback.
// The build-info branches depend on how the test binary was linked, so they're asserted for
// shape (non-empty, prefix), never for an exact version string.
func TestVersionOverride(t *testing.T) {
	tests := []struct {
		name     string
		module   string
		fallback string
		override []string
		expected string
	}{
		{"override wins over everything", "github.com/etkecc/go-kit", "fb", []string{"v1.2.3"}, "v1.2.3"},
		{"empty override is ignored, unknown module falls back", "github.com/nope/nope", "fb", []string{""}, "fb"},
		{"no override, unknown module falls back", "github.com/nope/nope", "fb", nil, "fb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Version(tt.module, tt.fallback, tt.override...); got != tt.expected {
				t.Errorf("Version(%q, %q, %v) = %q, want %q", tt.module, tt.fallback, tt.override, got, tt.expected)
			}
		})
	}
}

// TestVersionMainNonEmpty guards that resolving the main module (module == "") never returns
// the empty string: it lands on Main.Version, a VCS stamp, or the fallback.
func TestVersionMainNonEmpty(t *testing.T) {
	if got := Version("", "dev"); got == "" {
		t.Error("Version(\"\", \"dev\") returned empty; expected Main version, VCS revision, or the fallback")
	}
}

func TestUserAgent(t *testing.T) {
	got := UserAgent("Go-Test-client", "github.com/nope/nope")
	if got != "Go-Test-client/v0.0.0" {
		t.Errorf("UserAgent unknown module = %q, want Go-Test-client/v0.0.0", got)
	}

	got = UserAgent("Go-Test-client", "github.com/nope/nope", "v9.9.9")
	if got != "Go-Test-client/v9.9.9" {
		t.Errorf("UserAgent with override = %q, want Go-Test-client/v9.9.9", got)
	}

	if got := UserAgent("Go-Test-client", ""); !strings.HasPrefix(got, "Go-Test-client/") {
		t.Errorf("UserAgent main module = %q, want Go-Test-client/ prefix", got)
	}
}

func TestCleanVersion(t *testing.T) {
	tests := map[string]string{
		"(devel)": "",
		"":        "",
		"v1.2.3":  "v1.2.3",
	}
	for in, want := range tests {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVCSVersion(t *testing.T) {
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		expected string
	}{
		{"long revision is truncated to 7", []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef1234567890"}}, "abcdef1"},
		{"dirty tree gets the suffix", []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef1234567890"}, {Key: "vcs.modified", Value: "true"}}, "abcdef1-dirty"},
		{"short revision is left whole, no panic", []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}, "abc"},
		{"no revision is empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vcsVersion(&debug.BuildInfo{Settings: tt.settings})
			if got != tt.expected {
				t.Errorf("vcsVersion(%v) = %q, want %q", tt.settings, got, tt.expected)
			}
		})
	}
}
