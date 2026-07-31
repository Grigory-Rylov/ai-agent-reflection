package buildinfo

import (
	"time"
)

// BuildTime заполняется на этапе сборки через
// -ldflags "-X github.com/opencode/llama-client/pkg/buildinfo.BuildTime=...".
// Хранится в RFC3339 (UTC).
var BuildTime = "unknown"

// HumanReadable возвращает время сборки в локальном времени для вывода
// в логах и служебных сообщениях.
func HumanReadable() string {
	t, err := time.Parse(time.RFC3339, BuildTime)
	if err != nil {
		return BuildTime
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
