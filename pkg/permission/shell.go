package permission

import (
	"regexp"
	"strings"
)

func ScanCommand(command string) Scan {
	scan := Scan{}
	for _, source := range SplitCommands(command) {
		tokens := Tokenize(source)
		if len(tokens) == 0 {
			continue
		}
		scan.addUniquePattern(source)
		scan.addUniqueAlways(strings.Join(Prefix(tokens), " ") + " *")
		scan.nested(source)
	}
	return scan
}

type heredocRange struct {
	start int
	end   int
}

type heredocOpener struct {
	delim string
}

func findHeredocOpener(line string) *heredocOpener {
	inSingle, inDouble := false, false
	i := 0
	for i < len(line) {
		ch := line[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
			i++
		case inDouble:
			if ch == '"' {
				inDouble = false
			}
			i++
		case ch == '\'':
			inSingle = true
			i++
		case ch == '"':
			inDouble = true
			i++
		case ch == '<' && i+1 < len(line) && line[i+1] == '<':
			opener, ok := parseHeredocDelim(line, i+2)
			if !ok {
				return nil
			}
			return opener
		default:
			i++
		}
	}
	return nil
}

func parseHeredocDelim(line string, j int) (*heredocOpener, bool) {
	if j < len(line) && line[j] == '-' {
		j++
	}
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if j >= len(line) {
		return nil, false
	}
	switch line[j] {
	case '\'':
		k := j + 1
		for k < len(line) && line[k] != '\'' {
			k++
		}
		if k >= len(line) {
			return nil, false
		}
		return &heredocOpener{delim: line[j+1 : k]}, true
	case '"':
		k := j + 1
		for k < len(line) && line[k] != '"' {
			k++
		}
		if k >= len(line) {
			return nil, false
		}
		return &heredocOpener{delim: line[j+1 : k]}, true
	default:
		k := j
		for k < len(line) && line[k] != ' ' && line[k] != '\t' {
			k++
		}
		if k == j {
			return nil, false
		}
		return &heredocOpener{delim: line[j:k]}, true
	}
}

func heredocRanges(command string) []heredocRange {
	var ranges []heredocRange
	var open *heredocRange
	var pending []string
	lineStart := 0
	for i := 0; i <= len(command); i++ {
		boundary := i == len(command) || command[i] == '\n'
		if !boundary {
			continue
		}
		line := strings.TrimRight(command[lineStart:i], "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if open != nil {
			lastPending := pending[len(pending)-1]
			open.end = i + 1
			if trimmed == lastPending {
				pending = pending[:len(pending)-1]
				if len(pending) == 0 {
					open = nil
				}
			}
			lineStart = i + 1
			continue
		}
		if opener := findHeredocOpener(line); opener != nil {
			r := heredocRange{start: lineStart, end: i + 1}
			ranges = append(ranges, r)
			open = &ranges[len(ranges)-1]
			pending = append(pending, opener.delim)
		}
		lineStart = i + 1
	}
	for i := range ranges {
		if ranges[i].end == ranges[i].start {
			ranges[i].end = len(command)
		}
	}
	return ranges
}

type splitter struct {
	command     string
	ranges      []heredocRange
	rangeIdx    int
	inSingle    bool
	inDouble    bool
	dollarParen int
	paren       int
}

func (sp *splitter) inHeredoc(pos int) bool {
	if sp.rangeIdx >= len(sp.ranges) {
		return false
	}
	r := sp.ranges[sp.rangeIdx]
	if pos < r.start {
		return false
	}
	if pos >= r.end {
		sp.rangeIdx++
		return sp.inHeredoc(pos)
	}
	return true
}

func (sp *splitter) copyChar(b byte) {
	if sp.inSingle {
		if b == '\'' {
			sp.inSingle = false
		}
	} else if sp.inDouble {
		if b == '"' {
			sp.inDouble = false
		}
	} else if b == '\'' {
		sp.inSingle = true
	} else if b == '"' {
		sp.inDouble = true
	}
}

func (sp *splitter) step(command string, i int, current *strings.Builder) (next int, separator bool) {
	ch := command[i]
	nextByte := byte(0)
	if i+1 < len(command) {
		nextByte = command[i+1]
	}
	if sp.inHeredoc(i) {
		current.WriteByte(ch)
		return i + 1, false
	}
	switch {
	case sp.inSingle:
		current.WriteByte(ch)
		if ch == '\'' {
			sp.inSingle = false
		}
		return i + 1, false
	case sp.inDouble:
		current.WriteByte(ch)
		if ch == '"' {
			sp.inDouble = false
		}
		return i + 1, false
	case ch == '\'':
		sp.inSingle = true
		current.WriteByte(ch)
		return i + 1, false
	case ch == '"':
		sp.inDouble = true
		current.WriteByte(ch)
		return i + 1, false
	case ch == '$' && nextByte == '(':
		sp.dollarParen++
		current.WriteString("$(")
		return i + 2, false
	case ch == '\\':
		current.WriteByte(ch)
		if nextByte != 0 {
			current.WriteByte(nextByte)
		}
		return i + 2, false
	case ch == '(':
		sp.paren++
		current.WriteByte(ch)
		return i + 1, false
	case ch == ')':
		if sp.paren > 0 {
			sp.paren--
		} else if sp.dollarParen > 0 {
			sp.dollarParen--
		}
		current.WriteByte(ch)
		return i + 1, false
	case (ch == '&' && nextByte == '&') || (ch == '|' && nextByte == '|') || ch == ';' || ch == '\n':
		if sp.paren == 0 && sp.dollarParen == 0 {
			skip := 1
			if (ch == '&' && nextByte == '&') || (ch == '|' && nextByte == '|') {
				skip = 2
			}
			return i + skip, true
		}
		current.WriteByte(ch)
		return i + 1, false
	case ch == '|':
		if sp.paren == 0 && sp.dollarParen == 0 {
			return i + 1, true
		}
		current.WriteByte(ch)
		return i + 1, false
	default:
		current.WriteByte(ch)
		return i + 1, false
	}
}

func SplitCommands(command string) []string {
	var commands []string
	current := new(strings.Builder)
	sp := &splitter{command: command, ranges: heredocRanges(command)}
	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			commands = append(commands, text)
		}
		current.Reset()
	}
	i := 0
	for i < len(command) {
		next, separator := sp.step(command, i, current)
		if separator {
			flush()
		}
		i = next
	}
	flush()
	return commands
}

var nestedCommandRe = regexp.MustCompile(`\$\(([^)]+)\)`)

func (s *Scan) nested(source string) {
	for _, match := range nestedCommandRe.FindAllStringSubmatch(source, -1) {
		inner := ScanCommand(match[1])
		for _, pattern := range inner.Patterns {
			s.addUniquePattern(pattern)
		}
		for _, always := range inner.Always {
			s.addUniqueAlways(always)
		}
	}
}

func (s *Scan) addUniquePattern(pattern string) {
	addUnique(&s.Patterns, pattern)
}

func (s *Scan) addUniqueAlways(always string) {
	addUnique(&s.Always, always)
}

func Tokenize(source string) []string {
	var tokens []string
	var current strings.Builder
	inSingle, inDouble := false, false
	dollarParen := 0

	flushToken := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	i := 0
	for i < len(source) {
		ch := source[i]
		next := byte(0)
		if i+1 < len(source) {
			next = source[i+1]
		}

		switch {
		case inSingle:
			current.WriteByte(ch)
			if ch == '\'' {
				inSingle = false
			}
			i++
		case inDouble:
			current.WriteByte(ch)
			if ch == '"' {
				inDouble = false
			}
			i++
		case ch == '\'':
			inSingle = true
			current.WriteByte(ch)
			i++
		case ch == '"':
			inDouble = true
			current.WriteByte(ch)
			i++
		case ch == '$' && next == '(':
			dollarParen++
			current.WriteString("$(")
			i += 2
		case ch == '\\':
			current.WriteByte(ch)
			if next != 0 {
				current.WriteByte(next)
			}
			i += 2
		case ch == ')' && dollarParen > 0:
			dollarParen--
			current.WriteByte(ch)
			i++
		case (ch == ' ' || ch == '\t') && !inSingle && !inDouble && dollarParen == 0:
			flushToken()
			i++
		default:
			current.WriteByte(ch)
			i++
		}
	}
	flushToken()
	return tokens
}

type Scan struct {
	Patterns []string
	Always   []string
}

func addUnique(slice *[]string, value string) {
	for _, item := range *slice {
		if item == value {
			return
		}
	}
	*slice = append(*slice, value)
}
