package permission

import (
	"regexp"
	"strings"
)


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
