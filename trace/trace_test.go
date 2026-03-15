package trace_test

import (
	"strings"
	"testing"

	"api-profiler/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-01: ParseTraceparent válido.
func TestParseTraceparentValid(t *testing.T) {
	traceID, parentID, ok := trace.ParseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	require.True(t, ok)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", traceID)
	assert.Equal(t, "00f067aa0ba902b7", parentID)
}

// TC-02: ParseTraceparent con header vacío → ok=false.
func TestParseTraceparentEmpty(t *testing.T) {
	_, _, ok := trace.ParseTraceparent("")
	assert.False(t, ok)
}

// TC-03: ParseTraceparent con trace-id todo ceros → ok=false.
func TestParseTraceparentZeroTraceID(t *testing.T) {
	_, _, ok := trace.ParseTraceparent("00-00000000000000000000000000000000-00f067aa0ba902b7-01")
	assert.False(t, ok)
}

// TC-04: ParseTraceparent con formato incorrecto → ok=false.
func TestParseTraceparentBadFormat(t *testing.T) {
	for _, header := range []string{
		"invalid",
		"00-abc-def",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7", // missing flags
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", // parent all zeros
	} {
		_, _, ok := trace.ParseTraceparent(header)
		assert.False(t, ok, "expected false for header %q", header)
	}
}

// TC-05: NewTraceID genera 32 hex chars únicos.
func TestNewTraceIDUnique(t *testing.T) {
	id1 := trace.NewTraceID()
	id2 := trace.NewTraceID()
	assert.Len(t, id1, 32)
	assert.Len(t, id2, 32)
	assert.True(t, isHex(id1), "expected hex string, got %q", id1)
	assert.NotEqual(t, id1, id2)
}

// TC-05b: NewSpanID genera 16 hex chars únicos.
func TestNewSpanIDUnique(t *testing.T) {
	id1 := trace.NewSpanID()
	id2 := trace.NewSpanID()
	assert.Len(t, id1, 16)
	assert.Len(t, id2, 16)
	assert.True(t, isHex(id1), "expected hex string, got %q", id1)
	assert.NotEqual(t, id1, id2)
}

// TC-06: FormatTraceparent produce string correcto.
func TestFormatTraceparent(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID := "a2fb4a1d1a96d312"
	result := trace.FormatTraceparent(traceID, spanID)
	assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-a2fb4a1d1a96d312-01", result)
}

func isHex(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool {
		return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
	}) == ""
}
