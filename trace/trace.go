package trace

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// ParseTraceparent parses a W3C traceparent header value.
// Format: version(2)-traceid(32)-parentid(16)-flags(2)
// Returns trace-id and parent-id as hex strings and ok=true if valid.
func ParseTraceparent(header string) (traceID, parentID string, ok bool) {
	parts := strings.Split(header, "-")
	if len(parts) != 4 {
		return "", "", false
	}
	if len(parts[0]) != 2 {
		return "", "", false
	}
	if len(parts[3]) != 2 {
		return "", "", false
	}
	traceID = parts[1]
	parentID = parts[2]
	if len(traceID) != 32 || isAllZeros(traceID) {
		return "", "", false
	}
	if len(parentID) != 16 || isAllZeros(parentID) {
		return "", "", false
	}
	return traceID, parentID, true
}

func isAllZeros(s string) bool {
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

// NewTraceID generates a random 16-byte trace ID as 32 lowercase hex chars.
func NewTraceID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// NewSpanID generates a random 8-byte span ID as 16 lowercase hex chars.
func NewSpanID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// FormatTraceparent formats a W3C traceparent header value.
// flags is always 01 (sampled).
func FormatTraceparent(traceID, spanID string) string {
	return "00-" + traceID + "-" + spanID + "-01"
}
