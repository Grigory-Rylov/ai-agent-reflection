package tools

import (
	"fmt"
	"strings"
	"testing"
)

func zzMaskDebug(label, cmd string) {
	lines := strings.SplitAfter(cmd, "\n")
	openDelims := make(map[string]int)
	for li, raw := range lines {
		line := strings.TrimSuffix(raw, "\n")
		trimmed := strings.TrimLeft(line, " \t")
		opener := findLineHeredocOpener(trimmed)
		matchHit := ""
		for d := range openDelims {
			if trimmed == d {
				matchHit = d
				break
			}
		}
		if li < 3 || strings.Contains(line, "<<") || trimmed == "PRDESC" || opener != "" {
			fmt.Printf("%s L%d trim=%q opener=%q OPENMAP=%v HIT=%q\n", label, li, shorten(trimmed, 40), opener, openDelims, matchHit)
		}
		if matchHit != "" {
			delete(openDelims, matchHit)
			continue
		}
		if opener != "" {
			openDelims[opener] = li
		}
	}
	fmt.Printf("%s FINAL_OPEN=%v\n\n", label, openDelims)
}

func shorten(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > n {
		return s[:n] + ".."
	}
	return s
}

func TestZZMaskCompare(t *testing.T) {
	small := "--body \"$(cat <<'PRDESC'\n## S\nhi there\nPRDESC\n)\""
	real := "gh pr create --body \"$(cat <<'PRDESC'\n" + strings.Join(ghBodyLines, "\n") + "\nPRDESC\n)\""
	zzMaskDebug("SMALL ", small)
	zzMaskDebug("REAL  ", real)
	ms, mr := maskHeredocBodies(small), maskHeredocBodies(real)
	fmt.Printf("SMALL masked len=%d\n%s\n", len(ms), ms)
	fmt.Printf("REAL masked len=%d (orig %d)\n", len(mr), len(real))
}
