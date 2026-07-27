package agent

import (
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
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
	stateFunctionNoWrapper  // для формата без обёртки
	stateSimplifiedParam    // для упрощённого формата <path>value</path>
)

// ParseXMLToolCalls парсит XML tool calls в двух форматах:
// 1. С обёрткой: <tool_call><function=name><parameter=key>value</parameter></function></tool_call>
// 2. Без обёртки: <function=name><parameter=key>value</parameter></function>
// Пропускает XML внутри code blocks (``` ... ```)
// Если ни один формат не распознан, делает общий стриппинг <tool_call>...</tool_call> блоков.
func ParseXMLToolCalls(input string) XMLParseResult {
	// Сначала пробуем парсить с обёрткой
	result := parseWithWrapper(input)
	if len(result.ToolCalls) > 0 {
		logger.DebugToFile("ParseXMLToolCalls: found %d tool calls with wrapper", len(result.ToolCalls))
		return result
	}
	// Если не нашли — пробуем парсить без обёртки
	result = parseWithoutWrapper(input)
	if len(result.ToolCalls) > 0 {
		logger.DebugToFile("ParseXMLToolCalls: found %d tool calls without wrapper", len(result.ToolCalls))
		return result
	}
	// Fallback: общий стриппинг <tool_call>...</tool_call> блоков любых форматов
	stripped := stripToolCallBlocks(result.Content)
	if stripped != result.Content {
		logger.DebugToFile("ParseXMLToolCalls: stripped tool_call blocks via fallback")
		result.Content = stripped
	}
	return result
}

// stripToolCallBlocks удаляет все <tool_call>...</tool_call> блоки из текста,
// включая их содержимое, независимо от внутреннего формата.
// Используется как fallback, когда основной парсер не распознал формат.
func stripToolCallBlocks(input string) string {
	var result strings.Builder
	for {
		start := strings.Index(input, "<tool_call>")
		if start < 0 {
			start = strings.Index(input, "<tool_call >")
			if start < 0 {
				start = strings.Index(input, "<tool_call")
			}
		}
		if start < 0 {
			result.WriteString(input)
			break
		}
		// Пишем текст до <tool_call
		result.WriteString(input[:start])
		// Ищем закрывающий тег
		rest := input[start:]
		end := strings.Index(rest, "</tool_call>")
		if end < 0 {
			// Нет закрывающего тега — оставляем как есть
			result.WriteString(rest)
			break
		}
		// Пропускаем весь блок <tool_call>...</tool_call>
		input = rest[end+len("</tool_call>"):]
	}
	return result.String()
}

// isInCodeBlock проверяет, находится ли позиция внутри code block
func isInCodeBlock(input string, pos int) bool {
	count := 0
	for i := 0; i < pos && i+2 < len(input); i++ {
		if input[i:i+3] == "```" {
			count++
		}
	}
	// Нечётное количество означает, что мы внутри code block
	return count%2 == 1
}

// parseWithWrapper парсит формат с ิ обёрткой
func parseWithWrapper(input string) XMLParseResult {
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
		// Пропускаем парсинг если внутри code block на верхнем уровне
		// Внутри <tool_call> не применяем — иначе backtick-и внутри параметров
		// меняют счётчик и блокируют matchCloseTag для </function>/</tool_call>.
		if state == stateText && isInCodeBlock(input, i) {
			content.WriteByte(input[i])
			i++
			continue
		}

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
			if name, n := parseFunctionTagAsContent(input, i); n > 0 {
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
			if n := matchCloseTag(input, i, "tool_call"); n > 0 {
				if funcName != "" {
					result.ToolCalls = append(result.ToolCalls, XMLToolCall{
						Name: funcName,
						Args: args,
					})
				}
				if paramName != "" {
					args[paramName] = paramValue.String()
				}
				funcName = ""
				args = nil
				paramName = ""
				paramValue.Reset()
				pendingContent.Reset()
				state = stateText
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
			// Стандартный XML: <parameter name="key">value</parameter>
			if attrName, content, n := parseAttrTag(input, i); n > 0 {
				pendingContent.WriteString(input[i:n])
				if args == nil {
					args = make(map[string]string)
				}
				args[attrName] = content
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

// parseWithoutWrapper парсит формат БЕЗ ิ обёртки
// Формат: <function=name><parameter=key>value</parameter></function>
func parseWithoutWrapper(input string) XMLParseResult {
	var result XMLParseResult
	var content strings.Builder
	state := stateText

	var funcName string
	var paramName string
	var paramValue strings.Builder
	var args map[string]string
	var depth int // глубина вложенности тегов в content

	i := 0
	for i < len(input) {
		// Пропускаем парсинг если внутри code block на верхнем уровне
		// Внутри <function> не применяем — иначе backtick-и внутри параметров
		// меняют счётчик и блокируют matchCloseTag для </function>.
		if state == stateText && isInCodeBlock(input, i) {
			content.WriteByte(input[i])
			i++
			continue
		}

		switch state {
		case stateText:
			// Ищем <function=name>
			if name, n := parseTagWithValue(input, i, "function"); n > 0 {
				logger.DebugToFile("parseWithoutWrapper: found <function=%s> at pos %d", name, i)
				funcName = name
				args = make(map[string]string)
				state = stateFunctionNoWrapper
				i = n
				continue
			}
			if name, n := parseFunctionTagAsContent(input, i); n > 0 {
				logger.DebugToFile("parseWithoutWrapper: found <function>%s</function> at pos %d", name, i)
				funcName = name
				args = make(map[string]string)
				state = stateFunctionNoWrapper
				i = n
				continue
			}
			content.WriteByte(input[i])
			i++

		case stateFunctionNoWrapper:
			// Ищем </function> или <parameter=name> или упрощённый <param>value</param>
			if n := matchCloseTag(input, i, "function"); n > 0 {
				// Сохраняем tool call
				if funcName != "" {
					logger.DebugToFile("parseWithoutWrapper: completed tool call %s with %d args", funcName, len(args))
					result.ToolCalls = append(result.ToolCalls, XMLToolCall{
						Name: funcName,
						Args: args,
					})
				}
				funcName = ""
				args = nil
				state = stateText
				i = n
				continue
			}
			if name, n := parseTagWithValue(input, i, "parameter"); n > 0 {
				logger.DebugToFile("parseWithoutWrapper: found <parameter=%s>", name)
				paramName = name
				paramValue.Reset()
				depth = 0
				state = stateParam
				i = n
				continue
			}
			// Стандартный XML: <parameter name="key">value</parameter>
			if attrName, content, n := parseAttrTag(input, i); n > 0 {
				logger.DebugToFile("parseWithoutWrapper: found <parameter name=%q>%s</parameter>", attrName, content)
				if args == nil {
					args = make(map[string]string)
				}
				args[attrName] = content
				i = n
				continue
			}
			// Пробуем упрощённый формат <param>value</param>
			if tagName, value, n := parseSimpleTag(input, i); n > 0 {
				logger.DebugToFile("parseWithoutWrapper: found simplified param <%s>%s</%s>", tagName, value, tagName)
				if args == nil {
					args = make(map[string]string)
				}
				args[tagName] = value
				i = n
				continue
			}
			i++

		case stateParam:
			// Ищем </parameter>, учитывая возможные вложенные теги в значении
			if n := matchCloseTag(input, i, "parameter"); n > 0 && depth == 0 {
				if paramName != "" {
					args[paramName] = strings.TrimSpace(paramValue.String())
				}
				paramName = ""
				state = stateFunctionNoWrapper
				i = n
				continue
			}
			// Отслеживаем вложенные теги в значении параметра
			if i < len(input) && input[i] == '<' {
				if i+1 < len(input) && input[i+1] == '/' {
					depth--
				} else {
					depth++
				}
			}
			paramValue.WriteByte(input[i])
			i++
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

// parseFunctionTagAsContent парсит <function>name</function>, <function>name>
// или <function>name<parameter...>
// Возвращает (name, newPos) где newPos — позиция для продолжения парсинга,
// после которой state machine обработает </function> сама.
func parseFunctionTagAsContent(input string, i int) (string, int) {
	if !hasPrefixAt(input, i, "<function>") {
		return "", -1
	}
	contentStart := i + len("<function>")

	closeFunc := strings.Index(input[contentStart:], "</function>")
	endBracket := findChar(input, contentStart, '>')
	firstLT := findChar(input, contentStart, '<')

	var name string
	var nextPos int

	if endBracket >= 0 && (firstLT < 0 || endBracket < firstLT) {
		// Случай 1: <function>name> — malformed, > после имени без вложенных тегов
		name = strings.TrimSpace(input[contentStart:endBracket])
		nextPos = endBracket + 1
	} else if closeFunc >= 0 && (firstLT < 0 || firstLT == contentStart+closeFunc) {
		// Случай 2: <function>name</function> — без вложенных тегов
		name = strings.TrimSpace(input[contentStart : contentStart+closeFunc])
		nextPos = contentStart + closeFunc
	} else if firstLT >= 0 {
		// Случай 3: <function>name<...> — имя до первого вложенного тега
		name = strings.TrimSpace(input[contentStart:firstLT])
		nextPos = firstLT
	} else {
		return "", -1
	}

	if name == "" {
		return "", -1
	}

	logger.DebugToFile("parseFunctionTagAsContent: name=%q, nextPos=%d", name, nextPos)
	return name, nextPos
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
	logger.DebugToFile("parseTagWithValue(tagName=%s): parsed name=%q", tagName, name)
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

// parseAttrTag парсит формат: <tag attr="value">content</tag> или <tag attr='value'>content</tag>
// Возвращает (attrValue, content, endPosition) или ("", "", -1).
// Парсит ТОЛЬКО для тега "parameter" — извлекает name-атрибут и содержимое.
func parseAttrTag(input string, i int) (string, string, int) {
	if i >= len(input) || input[i] != '<' {
		return "", "", -1
	}
	close := findChar(input, i+1, '>')
	if close < 0 {
		return "", "", -1
	}
	openTag := input[i+1 : close]
	// Должно быть: parameter name="..." или parameter name='...'
	if !strings.HasPrefix(openTag, "parameter ") {
		return "", "", -1
	}
	rest := strings.TrimSpace(openTag[len("parameter "):])
	var attrVal string
	if strings.HasPrefix(rest, `name="`) {
		end := strings.Index(rest[6:], `"`)
		if end < 0 {
			return "", "", -1
		}
		attrVal = rest[6 : 6+end]
	} else if strings.HasPrefix(rest, `name='`) {
		end := strings.Index(rest[6:], `'`)
		if end < 0 {
			return "", "", -1
		}
		attrVal = rest[6 : 6+end]
	} else {
		return "", "", -1
	}
	if attrVal == "" {
		return "", "", -1
	}
	contentStart := close + 1
	closeTag := "</parameter>"
	closePos := strings.Index(input[contentStart:], closeTag)
	if closePos < 0 {
		return "", "", -1
	}
	content := strings.TrimSpace(input[contentStart : contentStart+closePos])
	return attrVal, content, contentStart + closePos + len(closeTag)
}

// parseSimpleTag парсит упрощённый формат тега: <tagname>value</tagname>
// Возвращает (tagname, value, endPosition) или ("", "", -1) если не удалось распарсить
func parseSimpleTag(input string, i int) (string, string, int) {
	if i >= len(input) || input[i] != '<' {
		return "", "", -1
	}

	// Ищем конец открывающего тега
	tagEnd := findChar(input, i+1, '>')
	if tagEnd < 0 {
		return "", "", -1
	}

	// Извлекаем имя тега
	tagName := strings.TrimSpace(input[i+1 : tagEnd])
	if tagName == "" || strings.Contains(tagName, " ") || strings.Contains(tagName, "=") {
		// Пустой тег, тег с пробелами или с = (это не упрощённый формат)
		return "", "", -1
	}

	// Проверяем, что это не закрывающий тег
	if strings.HasPrefix(tagName, "/") {
		return "", "", -1
	}

	// Ищем закрывающий тег </tagname>
	closeTag := "</" + tagName + ">"
	closePos := strings.Index(input[tagEnd+1:], closeTag)
	if closePos < 0 {
		// Пробуем с пробелами
		closeTag = "</ " + tagName + ">"
		closePos = strings.Index(input[tagEnd+1:], closeTag)
		if closePos < 0 {
			closeTag = "</" + tagName + " >"
			closePos = strings.Index(input[tagEnd+1:], closeTag)
		}
		if closePos < 0 {
			return "", "", -1
		}
	}

	valueStart := tagEnd + 1
	valueEnd := tagEnd + 1 + closePos
	value := strings.TrimSpace(input[valueStart:valueEnd])

	return tagName, value, valueEnd + len(closeTag)
}
