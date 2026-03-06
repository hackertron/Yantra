package tool

import (
	"testing"
)

func TestScrubPaths(t *testing.T) {
	s := NewScrubber([]PathMapping{
		{HostPath: "/Users/alice/projects/myapp", DisplayPath: "."},
		{HostPath: "/Users/alice/projects/myapp/src", DisplayPath: "./src"},
	})

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "replaces host path",
			input:  "read /Users/alice/projects/myapp/main.go",
			expect: "read ./main.go",
		},
		{
			name:   "longest match first",
			input:  "path /Users/alice/projects/myapp/src/handler.go",
			expect: "path ./src/handler.go",
		},
		{
			name:   "no match unchanged",
			input:  "hello world",
			expect: "hello world",
		},
		{
			name:   "empty output",
			input:  "",
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.scrubPaths(tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestScrubKeywordValues(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantRedact bool // true if [REDACTED] should appear
		wantKeep   string // substring that should be preserved (keyword prefix)
	}{
		{
			name:       "json api_key",
			input:      `{"api_key": "test-fake-abc123def456ghi789"}`,
			wantRedact: true,
			wantKeep:   `"api_key": "`,
		},
		{
			name:       "bearer token",
			input:      "Authorization: Bearer very-secret-token-value",
			wantRedact: true,
			wantKeep:   "Bearer",
		},
		{
			name:       "password equals",
			input:      "password=CorrectHorseBatteryStaple",
			wantRedact: true,
			wantKeep:   "password=",
		},
		{
			name:       "url query param",
			input:      "https://api.example.com?api_key=abc123secret",
			wantRedact: true,
			wantKeep:   "?api_key=",
		},
		{
			name:       "no keywords unchanged",
			input:      "status=ok commit=abc123",
			wantRedact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scrubKeywordValues(tt.input)
			hasRedaction := contains(got, "[REDACTED]")
			if hasRedaction != tt.wantRedact {
				t.Errorf("redaction=%v, want %v; got: %s", hasRedaction, tt.wantRedact, got)
			}
			if tt.wantKeep != "" && !contains(got, tt.wantKeep) {
				t.Errorf("expected to keep %q in output: %s", tt.wantKeep, got)
			}
		})
	}
}

func TestScrubHighEntropy(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantRedact bool
	}{
		{
			name:       "high entropy secret",
			input:      "key=A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v2",
			wantRedact: true,
		},
		{
			name:       "pure hex commit hash preserved",
			input:      "commit=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			wantRedact: false,
		},
		{
			name:       "short token preserved",
			input:      "id=abc123",
			wantRedact: false,
		},
		{
			name:       "normal text preserved",
			input:      "The quick brown fox jumps over the lazy dog",
			wantRedact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scrubHighEntropyTokens(tt.input)
			hasRedaction := contains(got, "[REDACTED]")
			if hasRedaction != tt.wantRedact {
				t.Errorf("redaction=%v, want %v; got: %s", hasRedaction, tt.wantRedact, got)
			}
		})
	}
}

func TestShannonEntropy(t *testing.T) {
	// "aaaa" has entropy 0 (only one unique char).
	if e := shannonEntropy("aaaa"); e != 0 {
		t.Errorf("expected 0, got %f", e)
	}

	// "ab" has entropy 1.0 (two equally frequent chars).
	if e := shannonEntropy("ab"); e < 0.99 || e > 1.01 {
		t.Errorf("expected ~1.0, got %f", e)
	}

	// Random-looking string should have high entropy.
	if e := shannonEntropy("A1b2C3d4E5f6G7h8I9j0K1l2"); e < 3.5 {
		t.Errorf("expected high entropy, got %f", e)
	}
}

func TestFullScrubPipeline(t *testing.T) {
	s := NewScrubber([]PathMapping{
		{HostPath: "/Users/alice/projects", DisplayPath: "."},
	})

	input := `read /Users/alice/projects/main.go
api_key=test_fake_A1b2C3d4E5f6G7h8I9j0K1l2M3n4
normal text here`

	got := s.Scrub(input)

	// Path should be scrubbed.
	if contains(got, "/Users/alice/projects") {
		t.Error("host path not scrubbed")
	}
	// Keyword value should be redacted.
	if !contains(got, "api_key=[REDACTED]") {
		t.Errorf("keyword not redacted: %s", got)
	}
	// Normal text preserved.
	if !contains(got, "normal text here") {
		t.Error("normal text was modified")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
