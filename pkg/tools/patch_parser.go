package tools

import (
	"regexp"
	"strconv"
	"strings"
)

func ParsePatch(patch string) []PatchFile {
	lines := strings.Split(patch, "\n")
	var files []PatchFile
	var current *PatchFile

	for _, line := range lines {
		switch {
		case matchOldPath(line):
			flushCurrent(&files, &current)
			current = &PatchFile{OldPath: parsePathLine(line)}
			if current.OldPath == "/dev/null" {
				current.IsNew = true
			}

		case matchNewPath(line):
			if current == nil {
				current = &PatchFile{}
			}
			current.NewPath = parsePathLine(line)
			if current.NewPath == "/dev/null" {
				current.IsDelete = true
				current.IsNew = false
			}

		case matchHunkHeader(line):
			if current != nil {
				hunk := parseHunkHeader(line)
				current.Hunks = append(current.Hunks, hunk)
			}

		default:
			addLineToCurrent(current, line)
		}
	}

	flushCurrent(&files, &current)
	return files
}

func matchOldPath(line string) bool {
	return len(line) > 4 && line[:4] == "--- "
}

func matchNewPath(line string) bool {
	return len(line) > 4 && line[:4] == "+++ "
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@`)

func matchHunkHeader(line string) bool {
	return hunkRe.MatchString(line)
}

func parsePathLine(line string) string {
	path := line[4:]
	if len(path) > 2 && (path[:2] == "a/" || path[:2] == "b/") {
		path = path[2:]
	}
	return path
}

func parseHunkHeader(line string) Hunk {
	m := hunkRe.FindStringSubmatch(line)
	if m == nil {
		return Hunk{}
	}

	h := Hunk{}
	h.OldStart, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		h.OldCount, _ = strconv.Atoi(m[2])
	}
	h.NewStart, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		h.NewCount, _ = strconv.Atoi(m[4])
	}
	return h
}

func addLineToCurrent(current *PatchFile, line string) {
	if current == nil || len(current.Hunks) == 0 {
		return
	}
	h := &current.Hunks[len(current.Hunks)-1]
	h.Lines = append(h.Lines, line)
}

func flushCurrent(files *[]PatchFile, current **PatchFile) {
	if *current != nil && len((*current).Hunks) > 0 {
		*files = append(*files, **current)
	}
	*current = nil
}
