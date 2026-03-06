package tool

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

const (
	redactionMarker       = "[REDACTED]"
	minEntropyTokenLen    = 24
	maxEntropyTokenLen    = 512
	highEntropyThreshold  = 3.8
	minUniqueChars        = 8
)

// PathMapping maps a host path prefix to a display path for scrubbing.
type PathMapping struct {
	HostPath    string
	DisplayPath string
}

// Scrubber removes sensitive data from tool outputs before they're sent to the LLM.
type Scrubber struct {
	pathMappings []PathMapping
}

// NewScrubber creates a Scrubber with the given path mappings.
func NewScrubber(mappings []PathMapping) *Scrubber {
	return &Scrubber{pathMappings: mappings}
}

// Scrub applies all scrubbing passes to the output string.
// Order matters: path scrubbing first (so entropy scrubber doesn't false-positive
// on long filesystem paths), then keyword values, then high-entropy tokens.
func (s *Scrubber) Scrub(output string) string {
	output = s.scrubPaths(output)
	output = scrubKeywordValues(output)
	output = scrubHighEntropyTokens(output)
	return output
}

// --- Path scrubbing ---

// scrubPaths replaces host path prefixes with display paths.
// Applies longest-first to avoid partial replacements.
func (s *Scrubber) scrubPaths(output string) string {
	if len(s.pathMappings) == 0 || output == "" {
		return output
	}

	// Sort by descending host path length so more-specific paths match first.
	sorted := make([]PathMapping, len(s.pathMappings))
	copy(sorted, s.pathMappings)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].HostPath) > len(sorted[j].HostPath)
	})

	for _, m := range sorted {
		if m.HostPath != "" {
			output = strings.ReplaceAll(output, m.HostPath, m.DisplayPath)
		}
	}
	return output
}

// --- Keyword-value scrubbing ---

// keywordCheck is a fast pre-check to avoid running expensive patterns on
// outputs that don't contain any credential keywords at all.
var keywordCheck = regexp.MustCompile(
	`(?i)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|bearer|secret)\b`,
)

// keywordValuePatterns match keyword=value, "keyword":"value", Bearer token, and
// ?api_key=value patterns. They preserve the keyword prefix and redact only the value.
var keywordValuePatterns = []*regexp.Regexp{
	// JSON: "api_key": "value"
	regexp.MustCompile(`(?i)("(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret)"\s*:\s*")([^"\n]+)(")`),
	// Plain: api_key=value or secret: value
	regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret)\s*(?:[:=]\s*))([^\s,;]+)`),
	// Bearer tokens
	regexp.MustCompile(`(?i)(\b(?:authorization\s*:\s*)?bearer\s+)([^\s,;]+)`),
	// URL query params: ?api_key=value or &token=value
	regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access_token|token|password)=)([^&\s]+)`),
}

func scrubKeywordValues(output string) string {
	if !keywordCheck.MatchString(output) {
		return output
	}

	for _, pattern := range keywordValuePatterns {
		output = pattern.ReplaceAllString(output, "${1}"+redactionMarker+"${3}")
	}
	return output
}

// --- High-entropy token scrubbing ---

// entropyCandidate matches base64/hex-like tokens that could be secrets.
var entropyCandidate = regexp.MustCompile(`[A-Za-z0-9+/_-]{24,512}={0,2}`)

func scrubHighEntropyTokens(output string) string {
	return entropyCandidate.ReplaceAllStringFunc(output, func(candidate string) string {
		if looksLikeSecretToken(candidate) {
			return redactionMarker
		}
		return candidate
	})
}

func looksLikeSecretToken(token string) bool {
	if len(token) < minEntropyTokenLen || len(token) > maxEntropyTokenLen {
		return false
	}

	// Exempt filesystem paths (starts with /, 3+ slashes).
	if strings.HasPrefix(token, "/") && strings.Count(token, "/") >= 3 {
		return false
	}

	// Exempt pure hex strings (commit hashes, etc.).
	if isAllHex(token) {
		return false
	}

	// Must have letters AND (digits OR symbols +/_-=).
	hasLetter := false
	hasDigit := false
	hasSymbol := false
	unique := make(map[rune]struct{})
	for _, r := range token {
		unique[r] = struct{}{}
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+' || r == '/' || r == '=' || r == '_' || r == '-':
			hasSymbol = true
		}
	}
	if !(hasLetter && (hasDigit || hasSymbol)) {
		return false
	}

	// Need sufficient character diversity.
	if len(unique) < minUniqueChars {
		return false
	}

	return shannonEntropy(token) >= highEntropyThreshold
}

func isAllHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// shannonEntropy computes the Shannon entropy of a string in bits per character.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}

	freq := make(map[rune]int)
	total := 0
	for _, r := range s {
		freq[r]++
		total++
	}

	entropy := 0.0
	for _, count := range freq {
		p := float64(count) / float64(total)
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}
