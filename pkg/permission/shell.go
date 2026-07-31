package permission

import (
	"regexp"
	"strings"
)

// ScanCommand splits a shell command into subcommands and produces the
// permission patterns and always-allow prefixes for each. Subcommands that
// only change directories (cd, pushd, ...) produce no pattern.
func ScanCommand(command string) Scan {
	scan := Scan{}
	for _, source := range splitCommands(command) {
		tokens := tokenize(source)
		if len(tokens) == 0 {
			continue
		}
		cmd := tokens[0]
		if cwdCommands[cmd] {
			continue
		}
		scan.addUniquePattern(source)
		scan.addUniqueAlways(strings.Join(Prefix(tokens), " ") + " *")
		scan.nested(source)
	}
	return scan
}

// cwdCommands are commands that only change the working directory and never
// need permission.
var cwdCommands = map[string]bool{
	"cd": true, "chdir": true, "popd": true, "pushd": true,
}

// splitCommands splits on command separators while respecting quotes and
// command substitution. Substitutions like $(...) are kept whole.
func splitCommands(command string) []string {
	var commands []string
	var current strings.Builder
	inSingle, inDouble := false, false
	dollarParen, paren := 0, 0

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			commands = append(commands, text)
		}
		current.Reset()
	}

	i := 0
	for i < len(command) {
		ch := command[i]
		next := byte(0)
		if i+1 < len(command) {
			next = command[i+1]
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
		case ch == '(':
			paren++
			current.WriteByte(ch)
			i++
		case ch == ')':
			if paren > 0 {
				paren--
			} else if dollarParen > 0 {
				dollarParen--
			}
			current.WriteByte(ch)
			i++
		case (ch == '&' && next == '&') || (ch == '|' && next == '|') || ch == ';' || ch == '\n':
			if paren == 0 && dollarParen == 0 {
				flush()
				i += 2
				continue
			}
			current.WriteByte(ch)
			i++
		case ch == '|':
			if paren == 0 && dollarParen == 0 {
				flush()
				i++
				continue
			}
			current.WriteByte(ch)
			i++
		default:
			current.WriteByte(ch)
			i++
		}
	}
	flush()
	return commands
}

// nestedCommandRe matches command substitution contents.
var nestedCommandRe = regexp.MustCompile(`\$\(([^)]+)\)`)

// nested recursively scans command substitutions inside a command source and
// appends their patterns and always prefixes to the scan.
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

// tokenize splits a command source into shell words, dropping empty quotes.
func tokenize(source string) []string {
	var tokens []string
	var current strings.Builder
	inSingle, inDouble := false, false
	dollarParen := 0

	flush := func() {
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
			flush()
			i++
		default:
			current.WriteByte(ch)
			i++
		}
	}
	flush()
	return tokens
}

// Scan holds the permission-relevant parts of a shell command.
type Scan struct {
	Patterns []string
	Always   []string
}

// addUnique appends value to the slice if it is not already present.
func addUnique(slice *[]string, value string) {
	for _, item := range *slice {
		if item == value {
			return
		}
	}
	*slice = append(*slice, value)
}
