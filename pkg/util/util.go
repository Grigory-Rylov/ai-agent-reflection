package util

import "github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"


func Truncate(s string, maxLen int) string {
	return stringutil.Truncate(s, maxLen, "...")
}
