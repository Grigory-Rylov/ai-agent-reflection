package permission

import (
	"regexp"
	"strings"
)

// Match reports whether input matches a wildcard pattern.
//
// Wildcards:
//   - `*` matches any sequence of characters (including `/`)
//   - `?` matches exactly one character
//
// A trailing ` *` (space + wildcard) is treated as optional, so `cat *`
// matches both `cat` and `cat file.txt`. This mirrors the opencode
// wildcard matcher used for command and path permission patterns.
func Match(input, pattern string) bool {
	normalized := strings.ReplaceAll(input, "\\", "/")

	var sb strings.Builder
	sb.WriteString("^")
	for _, r := range strings.ReplaceAll(pattern, "\\", "/") {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteByte('.')
		case '.', '+', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}

	escaped := sb.String()
	if strings.HasSuffix(escaped, " .*") {
		escaped = escaped[:len(escaped)-3] + "( .*)?"
	}
	escaped += "$"

	re, err := regexp.Compile(escaped)
	if err != nil {
		return false
	}
	return re.MatchString(normalized)
}
