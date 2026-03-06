package tool

import (
	"testing"
)

func TestParseDDGResults(t *testing.T) {
	payload := map[string]any{
		"Heading":        "Yantra",
		"AbstractURL":    "https://example.com/yantra",
		"AbstractText":   "Main summary about Yantra.",
		"AbstractSource": "example.com",
		"RelatedTopics": []any{
			map[string]any{
				"FirstURL": "https://example.com/topic-1",
				"Text":     "Topic One - Details about topic one",
			},
			map[string]any{
				"Name": "Group",
				"Topics": []any{
					map[string]any{
						"FirstURL": "https://example.com/topic-2",
						"Text":     "Topic Two - More details",
					},
				},
			},
		},
	}

	results := parseDDGResults("yantra", payload, 5)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Abstract result.
	if results[0].Title != "Yantra" {
		t.Errorf("result[0] title: got %q, want %q", results[0].Title, "Yantra")
	}
	if results[0].URL != "https://example.com/yantra" {
		t.Errorf("result[0] url: got %q", results[0].URL)
	}
	if results[0].Index != 1 {
		t.Errorf("result[0] index: got %d, want 1", results[0].Index)
	}

	// Related topic.
	if results[1].URL != "https://example.com/topic-1" {
		t.Errorf("result[1] url: got %q", results[1].URL)
	}
	if results[1].Title != "Topic One" {
		t.Errorf("result[1] title: got %q, want %q", results[1].Title, "Topic One")
	}

	// Nested topic.
	if results[2].URL != "https://example.com/topic-2" {
		t.Errorf("result[2] url: got %q", results[2].URL)
	}
}

func TestParseDDGResultsEmpty(t *testing.T) {
	payload := map[string]any{
		"Heading":      "",
		"AbstractURL":  "",
		"AbstractText": "",
		"RelatedTopics": []any{},
	}

	results := parseDDGResults("test", payload, 5)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestParseDDGResultsDedup(t *testing.T) {
	payload := map[string]any{
		"Heading":     "Test",
		"AbstractURL": "https://example.com/dup",
		"AbstractText": "Summary",
		"RelatedTopics": []any{
			map[string]any{
				"FirstURL": "https://example.com/dup", // duplicate
				"Text":     "Same URL",
			},
			map[string]any{
				"FirstURL": "https://example.com/unique",
				"Text":     "Unique result",
			},
		},
	}

	results := parseDDGResults("test", payload, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results (deduped), got %d", len(results))
	}
}

func TestCleanSnippet(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"<b>bold</b> text", "bold text"},
		{"&amp; &lt; &gt;", "& < >"},
		{"  multiple   spaces  ", "multiple spaces"},
		{"no tags", "no tags"},
	}

	for _, tt := range tests {
		got := cleanSnippet(tt.input)
		if got != tt.expect {
			t.Errorf("cleanSnippet(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

func TestParseDDGResultsCountLimit(t *testing.T) {
	topics := make([]any, 20)
	for i := range topics {
		topics[i] = map[string]any{
			"FirstURL": "https://example.com/" + string(rune('a'+i)),
			"Text":     "Result",
		}
	}

	payload := map[string]any{
		"Heading":        "",
		"AbstractURL":    "",
		"RelatedTopics":  topics,
	}

	results := parseDDGResults("test", payload, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results (capped), got %d", len(results))
	}
}
