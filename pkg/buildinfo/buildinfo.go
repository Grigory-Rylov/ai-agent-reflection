package buildinfo

import (
	"time"
)


var BuildTime = "unknown"


func HumanReadable() string {
	t, err := time.Parse(time.RFC3339, BuildTime)
	if err != nil {
		return BuildTime
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
