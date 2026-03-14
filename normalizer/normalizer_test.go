package normalizer_test

import (
	"testing"

	"api-profiler/normalizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autoNorm(t *testing.T) *normalizer.Normalizer {
	t.Helper()
	n, err := normalizer.New(nil, true)
	require.NoError(t, err)
	return n
}

// TC-01: Integer segment replaced with :id.
func TestNormalizeInteger(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/users/:id", n.Normalize("/users/123"))
}

// TC-02: Multiple integer segments each replaced.
func TestNormalizeMultipleIntegers(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/a/:id/b/:id", n.Normalize("/a/1/b/2"))
}

// TC-03: UUID segment replaced with :id.
func TestNormalizeUUID(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/orders/:id", n.Normalize("/orders/550e8400-e29b-41d4-a716-446655440000"))
}

// TC-04: Hex string ≥12 chars replaced with :id.
func TestNormalizeLongHex(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/tokens/:id", n.Normalize("/tokens/a1b2c3d4e5f6"))
}

// TC-05: Hex string < 12 chars NOT replaced.
func TestNormalizeShortHexUnchanged(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/tokens/a1b2c3", n.Normalize("/tokens/a1b2c3"))
}

// TC-06: Alphabetic segment not replaced.
func TestNormalizeTextUnchanged(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/users/john", n.Normalize("/users/john"))
}

// TC-07: Root "/" returned unchanged.
func TestNormalizeRoot(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/", n.Normalize("/"))
}

// TC-08: Trailing slash preserved.
func TestNormalizeTrailingSlash(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/users/:id/", n.Normalize("/users/123/"))
}

// TC-09: Custom rule applied before built-in rules.
func TestNormalizeCustomRuleFirst(t *testing.T) {
	n, err := normalizer.New([]normalizer.Rule{
		{Pattern: `^v[0-9]+$`, Replacement: ":version"},
	}, true)
	require.NoError(t, err)
	// "v2" matches custom rule → :version, not :id
	assert.Equal(t, "/api/:version/users", n.Normalize("/api/v2/users"))
	// integers still replaced by built-in
	assert.Equal(t, "/api/:version/users/:id", n.Normalize("/api/v2/users/42"))
}

// TC-10: Invalid custom pattern returns error from New.
func TestNormalizeInvalidPattern(t *testing.T) {
	_, err := normalizer.New([]normalizer.Rule{
		{Pattern: `[invalid(`, Replacement: ":id"},
	}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pattern")
}

// TC-11: autoDetect=false with no custom rules → path unchanged.
func TestNormalizeNoAutoDetect(t *testing.T) {
	n, err := normalizer.New(nil, false)
	require.NoError(t, err)
	assert.Equal(t, "/users/123", n.Normalize("/users/123"))
}

// TC-12: autoDetect=false with custom rules → only custom rules applied.
func TestNormalizeCustomOnlyNoAutoDetect(t *testing.T) {
	n, err := normalizer.New([]normalizer.Rule{
		{Pattern: `^[0-9]+$`, Replacement: ":num"},
	}, false)
	require.NoError(t, err)
	assert.Equal(t, "/users/:num", n.Normalize("/users/42"))
	// UUID not replaced (no auto-detect)
	assert.Equal(t, "/orders/550e8400-e29b-41d4-a716-446655440000",
		n.Normalize("/orders/550e8400-e29b-41d4-a716-446655440000"))
}

// TC-13: Query string preserved unchanged.
func TestNormalizeQueryStringPreserved(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/users/:id?foo=bar&page=2", n.Normalize("/users/123?foo=bar&page=2"))
}

// TC-14: Version-like segment "v2" is NOT an integer → unchanged.
func TestNormalizeVersionSegmentUnchanged(t *testing.T) {
	n := autoNorm(t)
	assert.Equal(t, "/api/v2/products", n.Normalize("/api/v2/products"))
}
