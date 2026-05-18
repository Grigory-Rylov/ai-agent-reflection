package agent

import (
	"strings"
)

type XMLToolCall struct {
	Name string
	Args map[string]string
}

type XMLParseResult struct {
	Content   string
	ToolCalls []XMLToolCall
}

type xmlState int

const (
	stateText xmlState = iota
	stateToolCall
	stateFunction
	stateParam
)

func ParseXMLToolCalls(input string) XMLParseResult {
	var result XMLParseResult
	var content strings.Builder
	var pendingContent strings.Builder
	state := stateText

	var funcName string
	var paramName string
	var paramValue strings.Builder
	var args map[string]string

	i := 0
	for i < len(input) {
		switch state {
		case stateText:
			if n := matchOpenTag(input, i, "tool_call"); n > 0 {
				pendingContent.Reset()
				pendingContent.WriteString(input[i:n])
				state = stateToolCall
				funcName = ""
				args = nil
				i = n
				continue
			}
			if n := matchCloseTag(input, i, "tool_call"); n > 0 {
				content.WriteString(input[i:n])
				i = n
				continue
			}
			content.WriteByte(input[i])
			i++

		case stateToolCall:
			if n := matchCloseTag(input, i, "tool_call"); n > 0 {
				if funcName != "" {
					result.ToolCalls = append(result.ToolCalls, XMLToolCall{
						Name: funcName,
						Args: args,
					})
				} else {
					content.WriteString(pendingContent.String())
				}
				funcName = ""
				args = nil
				pendingContent.Reset()
				state = stateText
				i = n
				continue
			}
			if name, n := parseTagWithValue(input, i, "function"); n > 0 {
				pendingContent.WriteString(input[i:n])
				funcName = name
				args = make(map[string]string)
				state = stateFunction
				i = n
				continue
			}
			if input[i] == '<' {
				end := findChar(input, i+1, '>')
				if end > 0 {
					pendingContent.WriteString(input[i : end+1])
					i = end + 1
					continue
				}
			}
			pendingContent.WriteByte(input[i])
			i++

		case stateFunction:
			if n := matchCloseTag(input, i, "function"); n > 0 {
				pendingContent.WriteString(input[i:n])
				if paramName != "" {
					args[paramName] = paramValue.String()
					paramName = ""
					paramValue.Reset()
				}
				state = stateToolCall
				i = n
				continue
			}
			if name, n := parseTagWithValue(input, i, "parameter"); n > 0 {
				pendingContent.WriteString(input[i:n])
				if paramName != "" {
					args[paramName] = paramValue.String()
				}
				paramName = name
				paramValue.Reset()
				state = stateParam
				i = n
				continue
			}
			if input[i] == '<' {
				end := findChar(input, i+1, '>')
				if end > 0 {
					pendingContent.WriteString(input[i : end+1])
					i = end + 1
					continue
				}
			}
			pendingContent.WriteByte(input[i])
			i++

		case stateParam:
			if n := matchCloseTag(input, i, "parameter"); n > 0 {
				pendingContent.WriteString(input[i:n])
				if paramName != "" {
					args[paramName] = strings.TrimSpace(paramValue.String())
				}
				paramName = ""
				paramValue.Reset()
				state = stateFunction
				i = n
				continue
			}
			paramValue.WriteByte(input[i])
			i++
		}
	}

	// Flush pending content if tool call was malformed
	if state != stateText {
		content.WriteString(pendingContent.String())
		if paramName != "" {
			content.WriteString(paramValue.String())
		}
	}

	result.Content = content.String()
	return result
}

func matchOpenTag(input string, i int, tagName string) int {
	prefix := "<" + tagName + ">"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	prefix = "<" + tagName + " >"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	prefix = "< " + tagName + ">"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	prefix = "< " + tagName + " >"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	return -1
}

func matchCloseTag(input string, i int, tagName string) int {
	prefix := "</" + tagName + ">"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	prefix = "</ " + tagName + " >"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	prefix = "</" + tagName + " >"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	prefix = "< /" + tagName + ">"
	if hasPrefixAt(input, i, prefix) {
		return i + len(prefix)
	}

	return -1
}

func parseTagWithValue(input string, i int, tagName string) (string, int) {
	if i >= len(input) || input[i] != '<' {
		return "", -1
	}
	i++

	i = skipWS(input, i)

	if !hasPrefixAt(input, i, tagName) {
		return "", -1
	}
	i += len(tagName)

	i = skipWS(input, i)

	if i >= len(input) || input[i] != '=' {
		return "", -1
	}
	i++

	i = skipWS(input, i)

	end := findChar(input, i, '>')
	if end < 0 {
		return "", -1
	}

	name := strings.TrimSpace(input[i:end])
	return name, end + 1
}

func skipWS(input string, i int) int {
	for i < len(input) && (input[i] == ' ' || input[i] == '\t' || input[i] == '\n' || input[i] == '\r') {
		i++
	}
	return i
}

func hasPrefixAt(s string, i int, prefix string) bool {
	if i+len(prefix) > len(s) {
		return false
	}
	return s[i:i+len(prefix)] == prefix
}

func findChar(s string, start int, ch byte) int {
	for i := start; i < len(s); i++ {
		if s[i] == ch {
			return i
		}
	}
	return -1
}
