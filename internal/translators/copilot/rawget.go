package copilot

// Checked accessors for the raw (map[string]any) request shape: the quota
// pipeline navigates client JSON defensively — a malformed shape classifies
// as the safe default, never panics. nil map / "" / nil slice mean "not
// this shape", callers treat them as absent.

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}

	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}

	return nil
}
