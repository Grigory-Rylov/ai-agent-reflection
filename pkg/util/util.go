package util

import "github.com/opencode/llama-client/pkg/util/stringutil"

// Truncate обрезает строку до maxLen символов, добавляя многоточие,
// если строка была обрезана. Фасад над stringutil.Truncate.
func Truncate(s string, maxLen int) string {
	return stringutil.Truncate(s, maxLen, "...")
}
