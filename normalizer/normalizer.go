package normalizer

import (
	"fmt"
	"regexp"
	"strings"
)

// Rule defines a regexp pattern applied to each path segment and its replacement.
type Rule struct {
	Pattern     string
	Replacement string
}

type compiledRule struct {
	re          *regexp.Regexp
	replacement string
}

// Normalizer replaces dynamic path segments (IDs, UUIDs, hex tokens) with
// stable placeholders so metrics are grouped by endpoint shape, not by value.
type Normalizer struct {
	rules []compiledRule
}

// builtinRules are applied when autoDetect=true, after any custom rules.
var builtinRules = []Rule{
	// UUID: 8-4-4-4-12 hex groups
	{
		Pattern:     `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
		Replacement: ":id",
	},
	// Pure integer
	{
		Pattern:     `^[0-9]+$`,
		Replacement: ":id",
	},
	// Long hex string (≥12 chars, only hex digits)
	{
		Pattern:     `^[0-9a-fA-F]{12,}$`,
		Replacement: ":id",
	},
}

// New compiles the provided custom rules and, when autoDetect is true, appends
// the built-in rules (UUID, integer, hex). Returns an error if any custom rule
// pattern is not a valid regular expression.
func New(rules []Rule, autoDetect bool) (*Normalizer, error) {
	compiled := make([]compiledRule, 0, len(rules)+len(builtinRules))

	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("normalizer: invalid pattern %q: %w", r.Pattern, err)
		}
		compiled = append(compiled, compiledRule{re: re, replacement: r.Replacement})
	}

	if autoDetect {
		for _, r := range builtinRules {
			compiled = append(compiled, compiledRule{
				re:          regexp.MustCompile(r.Pattern),
				replacement: r.Replacement,
			})
		}
	}

	return &Normalizer{rules: compiled}, nil
}

// Normalize applies the rules segment by segment and returns the normalised path.
// Query strings (everything from '?' onward) are preserved unchanged.
// The root path "/" and empty strings are returned as-is.
func (n *Normalizer) Normalize(path string) string {
	if path == "" || path == "/" {
		return path
	}

	// Preserve query string.
	query := ""
	if i := strings.IndexByte(path, '?'); i >= 0 {
		query = path[i:]
		path = path[:i]
	}

	trailingSlash := len(path) > 1 && strings.HasSuffix(path, "/")
	segments := strings.Split(path, "/")

	for i, seg := range segments {
		if seg == "" {
			continue
		}
		for _, rule := range n.rules {
			if rule.re.MatchString(seg) {
				segments[i] = rule.replacement
				break // first match wins
			}
		}
	}

	result := strings.Join(segments, "/")
	if trailingSlash && !strings.HasSuffix(result, "/") {
		result += "/"
	}
	return result + query
}
