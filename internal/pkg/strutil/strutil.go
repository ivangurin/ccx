// Package strutil — tiny shared string helpers (one copy instead of one
// per package).
package strutil

// FirstNonEmpty returns the first non-empty value, or "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
