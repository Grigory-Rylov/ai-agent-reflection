package stringutil

// Truncate truncates s to maxLen runes, adding suffix if truncated.
// Returns s unchanged if it fits within maxLen runes.
func Truncate(s string, maxLen int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + suffix
}
