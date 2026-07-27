package agent

import (
	"strings"
	"testing"
)

func eq(a, b XMLParseResult) bool {
	if a.Content != b.Content {
		return false
	}
	if len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i, tc := range a.ToolCalls {
		if tc.Name != b.ToolCalls[i].Name {
			return false
		}
		if len(tc.Args) != len(b.ToolCalls[i].Args) {
			return false
		}
		for k, v := range tc.Args {
			if b.ToolCalls[i].Args[k] != v {
				return false
			}
		}
	}
	return true
}

func assertParse(t *testing.T, input string, expected XMLParseResult) {
	t.Helper()
	result := ParseXMLToolCalls(input)
	if !eq(result, expected) {
		t.Errorf("ParseXMLToolCalls(%q) = %+v, want %+v", input, result, expected)
	}
}

func TestParseXMLToolCalls_Empty(t *testing.T) {
	assertParse(t, "", XMLParseResult{})
}

func TestParseXMLToolCalls_PlainText(t *testing.T) {
	text := "Hello, world!"
	assertParse(t, text, XMLParseResult{Content: text})
}

func TestParseXMLToolCalls_TimeGet(t *testing.T) {
	input := `<tool_call>
<function=time_get>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_FileWrite(t *testing.T) {
	input := `<tool_call>
<function=file_write>
<parameter=content>Hello world</parameter>
<parameter=path>/tmp/test.txt</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "file_write",
				Args: map[string]string{
					"content": "Hello world",
					"path":    "/tmp/test.txt",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_TextBefore(t *testing.T) {
	input := `Let me check the time.

<tool_call>
<function=time_get>
</function>
</tool_call>`
	expected := XMLParseResult{
		Content: "Let me check the time.\n\n",
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_TextBeforeAndAfter(t *testing.T) {
	input := `Let me check.

<tool_call>
<function=time_get>
</function>
</tool_call>

Done!`
	expected := XMLParseResult{
		Content: "Let me check.\n\n\n\nDone!",
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_MultipleTools(t *testing.T) {
	input := `<tool_call>
<function=time_get>
</function>
</tool_call>
<tool_call>
<function=calc>
<parameter=expression>2 + 2</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		Content: "\n",
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
			{Name: "calc", Args: map[string]string{"expression": "2 + 2"}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_MultipleToolsWithText(t *testing.T) {
	input := `First, let me check time.

<tool_call>
<function=time_get>
</function>
</tool_call>

Now a calculation:

<tool_call>
<function=calc>
<parameter=expression>42 * 2</parameter>
</function>
</tool_call>

Here is the result.`
	expected := XMLParseResult{
		Content: "First, let me check time.\n\n\n\nNow a calculation:\n\n\n\nHere is the result.",
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
			{Name: "calc", Args: map[string]string{"expression": "42 * 2"}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_MultilineParamValue(t *testing.T) {
	input := `<tool_call>
<function=file_write>
<parameter=content>
Hello world
This is a multiline file.
</parameter>
<parameter=path>
/tmp/test.txt
</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "file_write" {
		t.Errorf("expected name file_write, got %q", tc.Name)
	}
	if tc.Args["content"] != "Hello world\nThis is a multiline file." {
		t.Errorf("expected multiline content, got %q", tc.Args["content"])
	}
	if tc.Args["path"] != "/tmp/test.txt" {
		t.Errorf("expected /tmp/test.txt, got %q", tc.Args["path"])
	}
}

func TestParseXMLToolCalls_ExtraWhitespace(t *testing.T) {
	input := `  <tool_call>
  <function=time_get>
  </function>
  </tool_call>  `
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
}

func TestParseXMLToolCalls_NoToolCallsJustTags(t *testing.T) {
	input := `<some_random_tag>
content
</some_random_tag>`
	expected := XMLParseResult{
		Content: input,
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_PartialToolCall(t *testing.T) {
	input := `<tool_call>
<function=time_get`
	// Malformed — no closing tags, should return content as-is
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(result.ToolCalls))
	}
	if result.Content != input {
		t.Errorf("expected content %q, got %q", input, result.Content)
	}
}

func TestParseXMLToolCalls_MissingCloseToolCall(t *testing.T) {
	input := `<tool_call<function=time_get>
</function>`
	// Has <tool_call< opening but missing </tool_call< closing
	// Parser should still find the tool call via fallback to no-wrapper format
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
}

func TestParseXMLToolCalls_TextBetweenParams(t *testing.T) {
	// If there's text between <parameter> and </parameter> that looks like XML
	input := `<tool_call>
<function=file_write>
<parameter=content>Hello <world> text</parameter>
<parameter=path>/tmp/test.txt</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Args["content"] != "Hello <world> text" {
		t.Errorf("expected 'Hello <world> text', got %q", tc.Args["content"])
	}
}

func TestParseXMLToolCalls_EmptyParamValue(t *testing.T) {
	input := `<tool_call>
<function=file_write>
<parameter=content></parameter>
<parameter=path>/tmp/test.txt</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Args["content"] != "" {
		t.Errorf("expected empty content, got %q", tc.Args["content"])
	}
}

func TestParseXMLToolCalls_OnlyToolCallNoContent(t *testing.T) {
	input := `<tool_call>
<function=time_get>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_WebFetch(t *testing.T) {
	input := `<tool_call>
<function=web_fetch>
<parameter=method>GET</parameter>
<parameter=url>https://example.com</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "web_fetch",
				Args: map[string]string{
					"method": "GET",
					"url":    "https://example.com",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_ShellExecute(t *testing.T) {
	input := `<tool_call>
<function=shell_execute>
<parameter=command>ls -la</parameter>
<parameter=timeout>10</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "shell_execute",
				Args: map[string]string{
					"command": "ls -la",
					"timeout": "10",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_MixedContent(t *testing.T) {
	input := `I'll help you with that.

<tool_call>
<function=web_search>
<parameter=query>Go programming language</parameter>
</function>
</tool_call>

Let me also check the time.

<tool_call>
<function=time_get>
</function>
</tool_call>

Here are the results.`
	expected := XMLParseResult{
		Content: "I'll help you with that.\n\n\n\nLet me also check the time.\n\n\n\nHere are the results.",
		ToolCalls: []XMLToolCall{
			{
				Name: "web_search",
				Args: map[string]string{"query": "Go programming language"},
			},
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_GlobTool(t *testing.T) {
	input := `<tool_call>
<function=glob>
<parameter=pattern>**/*.go</parameter>
<parameter=path>./src</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "glob",
				Args: map[string]string{
					"pattern": "**/*.go",
					"path":    "./src",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_GrepTool(t *testing.T) {
	input := `<tool_call>
<function=search_code>
<parameter=pattern>func main</parameter>
<parameter=include>*.go</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "search_code",
				Args: map[string]string{
					"pattern": "func main",
					"include": "*.go",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_EditTool(t *testing.T) {
	input := `<tool_call>
<function=edit>
<parameter=path>/tmp/file.txt</parameter>
<parameter=old_string>foo</parameter>
<parameter=new_string>bar</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "edit",
				Args: map[string]string{
					"path":       "/tmp/file.txt",
					"old_string": "foo",
					"new_string": "bar",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_FileList(t *testing.T) {
	input := `<tool_call>
<function=file_list>
<parameter=path>/tmp</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "file_list",
				Args: map[string]string{"path": "/tmp"},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_FileRead(t *testing.T) {
	input := `<tool_call>
<function=file_read>
<parameter=path>/tmp/test.txt</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "file_read",
				Args: map[string]string{"path": "/tmp/test.txt"},
			},
		},
	}
	assertParse(t, input, expected)
}

func TestParseXMLToolCalls_ThreeToolCalls(t *testing.T) {
	input := `<tool_call>
<function=time_get>
</function>
</tool_call>
<tool_call>
<function=calc>
<parameter=expression>1+1</parameter>
</function>
</tool_call>
<tool_call>
<function=file_write>
<parameter=content>test</parameter>
<parameter=path>/tmp/t.txt</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[1].Name != "calc" {
		t.Errorf("expected calc, got %q", result.ToolCalls[1].Name)
	}
	if result.ToolCalls[2].Name != "file_write" {
		t.Errorf("expected file_write, got %q", result.ToolCalls[2].Name)
	}
}

func TestParseXMLToolCalls_ParamValueWithSpecialChars(t *testing.T) {
	input := `<tool_call>
<function=web_search>
<parameter=query>Go & Rust: сравнение языков</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Args["query"] != "Go & Rust: сравнение языков" {
		t.Errorf("expected query with special chars, got %q", tc.Args["query"])
	}
}

func TestParseXMLToolCalls_NestedAngleBracketsInContent(t *testing.T) {
	input := `<tool_call>
<function=file_write>
<parameter=content>if x < 10 && y > 20 { return }</parameter>
<parameter=path>/tmp/code.go</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	expected := `if x < 10 && y > 20 { return }`
	if tc.Args["content"] != expected {
		t.Errorf("expected %q, got %q", expected, tc.Args["content"])
	}
}

func TestParseXMLToolCalls_XmlInParamValue(t *testing.T) {
	input := `<tool_call>
<function=file_write>
<parameter=content><note><to>Tove</to></note></parameter>
<parameter=path>/tmp/note.xml</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	expected := `<note><to>Tove</to></note>`
	if tc.Args["content"] != expected {
		t.Errorf("expected %q, got %q", expected, tc.Args["content"])
	}
}

func TestParseXMLToolCalls_ExtraSpacesInTags(t *testing.T) {
	input := `<tool_call >
<function = time_get>
</function >
</tool_call >`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertParse(t, input, expected)
}

// ============================================================
// Тесты для формата БЕЗ ิ обёртки
// ============================================================

func TestParseXMLToolCalls_NoWrapper_FileRead(t *testing.T) {
	input := `<function=read_file>
<parameter=path>
/Users/g.rylov/Documents/projects/go/confluence_exporter/out/VK Android • Automation.html
</parameter>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("expected read_file, got %q", tc.Name)
	}
	expectedPath := "/Users/g.rylov/Documents/projects/go/confluence_exporter/out/VK Android • Automation.html"
	if tc.Args["path"] != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, tc.Args["path"])
	}
}

func TestParseXMLToolCalls_NoWrapper_Simple(t *testing.T) {
	input := `<function=time_get>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
}

func TestParseXMLToolCalls_NoWrapper_WithParams(t *testing.T) {
	input := `<function=file_write>
<parameter=path>/tmp/test.txt</parameter>
<parameter=content>Hello world</parameter>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "file_write" {
		t.Errorf("expected file_write, got %q", tc.Name)
	}
	if tc.Args["path"] != "/tmp/test.txt" {
		t.Errorf("expected path /tmp/test.txt, got %q", tc.Args["path"])
	}
	if tc.Args["content"] != "Hello world" {
		t.Errorf("expected content Hello world, got %q", tc.Args["content"])
	}
}

func TestParseXMLToolCalls_NoWrapper_MultipleTools(t *testing.T) {
	input := `<function=time_get>
</function>
<function=calc>
<parameter=expression>2 + 2</parameter>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[1].Name != "calc" {
		t.Errorf("expected calc, got %q", result.ToolCalls[1].Name)
	}
}

func TestParseXMLToolCalls_NoWrapper_WithText(t *testing.T) {
	input := `Let me read the file.

<function=read_file>
<parameter=path>/tmp/test.txt</parameter>
</function>

Here is the result.`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("expected read_file, got %q", result.ToolCalls[0].Name)
	}
	// Content должен содержать текст до и после
	if !strings.Contains(result.Content, "Let me read the file") {
		t.Errorf("content should contain 'Let me read the file', got %q", result.Content)
	}
}

func TestParseXMLToolCalls_NoWrapper_MultilineParam(t *testing.T) {
	input := `<function=file_write>
<parameter=content>
Line 1
Line 2
Line 3
</parameter>
<parameter=path>/tmp/test.txt</parameter>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if !strings.Contains(tc.Args["content"], "Line 1") {
		t.Errorf("content should contain 'Line 1', got %q", tc.Args["content"])
	}
}

func TestParseXMLToolCalls_NoWrapper_NestedTags(t *testing.T) {
	input := `<function=file_write>
<parameter=content><div><p>Hello</p></div></parameter>
<parameter=path>/tmp/test.html</parameter>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	expected := "<div><p>Hello</p></div>"
	if tc.Args["content"] != expected {
		t.Errorf("expected %q, got %q", expected, tc.Args["content"])
	}
}

// ============================================================
// Тесты для игнорирования XML в code blocks
// ============================================================

func TestParseXMLToolCalls_CodeBlockIgnored(t *testing.T) {
	input := "Here is an example:\n```xml\n<function=time_get>\n</function>\n```\nNo tool calls here."
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls inside code block, got %d", len(result.ToolCalls))
	}
}

func TestParseXMLToolCalls_CodeBlockWithRealToolAfter(t *testing.T) {
	input := "```xml\n<function=time_get>\n</function>\n```\n\n<function=calc>\n<parameter=expression>1+1</parameter>\n</function>"
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call outside code block, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "calc" {
		t.Errorf("expected calc, got %q", result.ToolCalls[0].Name)
	}
}

func TestParseXMLToolCalls_CodeBlockWithRealToolBefore(t *testing.T) {
	input := "<function=time_get>\n</function>\n\n```xml\n<function=calc>\n<parameter=expression>1+1</parameter>\n</function>\n```"
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call outside code block, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
}

func TestParseXMLToolCalls_MultipleCodeBlocks(t *testing.T) {
	input := "```xml\n<function=time_get>\n</function>\n```\nSome text\n```xml\n<function=calc>\n</function>\n```\n<function=file_read>\n<parameter=path>/tmp/test.txt</parameter>\n</function>"
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call outside code blocks, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "file_read" {
		t.Errorf("expected file_read, got %q", result.ToolCalls[0].Name)
	}
}

// TestParseXMLToolCalls_CodeBlockInParamValue — регрессионный тест для бага,
// когда внутри <parameter=task> есть секция ```\n```go\n...\n``` с нечётным
// количеством backtick-последовательностей. Это заставляло isInCodeBlock()
// блокировать matchCloseTag для </parameter>, из-за чего парсер застревал
// в stateParam до EOF и возвращал 0 tool calls.
func TestParseXMLToolCalls_CodeBlockInParamValue(t *testing.T) {
	bt := "\x60\x60\x60"
	input := "<tool_call>\n<function=subagent>\n<parameter=name>\nworker\n</parameter>\n<parameter=task>\nСоздать Go приложение\n\nНапишите код:\n" + bt + "\n" + bt + "go\n<код>\n" + bt + "\n</parameter>\n</function>\n</tool_call>"
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "subagent" {
		t.Errorf("expected subagent, got %q", tc.Name)
	}
	if tc.Args["name"] != "worker" {
		t.Errorf("expected worker, got %q", tc.Args["name"])
	}
	if !strings.Contains(tc.Args["task"], "Создать Go приложение") {
		t.Errorf("task should contain 'Создать Go приложение', got %q", tc.Args["task"])
	}
	if !strings.Contains(tc.Args["task"], bt) {
		t.Errorf("task should contain backtick code blocks, got %q", tc.Args["task"])
	}
	if !strings.Contains(tc.Args["task"], "<код>") {
		t.Errorf("task should contain '<код>', got %q", tc.Args["task"])
	}
}

// TestParseXMLToolCalls_CodeBlockInParamValue_EvenCount — проверка,
// что нормальный code block (с чётным count) по-прежнему работает.
func TestParseXMLToolCalls_CodeBlockInParamValue_EvenCount(t *testing.T) {
	bt := "\x60\x60\x60"
	input := "<tool_call>\n<function=subagent>\n<parameter=name>\nqa\n</parameter>\n<parameter=task>\n" + bt + "go\npackage main\nimport \"fmt\"\n" + bt + "\n</parameter>\n</function>\n</tool_call>"
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "subagent" {
		t.Errorf("expected subagent, got %q", tc.Name)
	}
	if tc.Args["name"] != "qa" {
		t.Errorf("expected qa, got %q", tc.Args["name"])
	}
	if !strings.Contains(tc.Args["task"], bt+"go") {
		t.Errorf("task should contain code block, got %q", tc.Args["task"])
	}
}

// Тест формата который модель реально выдаёт с префиксом
func TestParseXMLToolCalls_NoWrapper_WithContentStartPrefix(t *testing.T) {
	input := `_CONTENT_START__
<function=read_file>
<parameter=path>
/Users/g.rylov/Documents/projects/go/confluence_exporter/out/Разработка_ Документация/Android.html
</parameter>
</function>
___`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("expected read_file, got %q", tc.Name)
	}
}

// ============================================================
// Тесты для упрощённого формата параметров <path>value</path>
// (без parameter= префикса)
// ============================================================

func TestParseXMLToolCalls_SimplifiedParams_SingleParam(t *testing.T) {
	input := `<function=read_file>
<path>/tmp/test.txt</path>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("expected read_file, got %q", tc.Name)
	}
	if tc.Args["path"] != "/tmp/test.txt" {
		t.Errorf("expected path /tmp/test.txt, got %q", tc.Args["path"])
	}
}

func TestParseXMLToolCalls_SimplifiedParams_MultipleParams(t *testing.T) {
	input := `<function=file_write>
<path>/tmp/test.txt</path>
<content>Hello world</content>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "file_write" {
		t.Errorf("expected file_write, got %q", tc.Name)
	}
	if tc.Args["path"] != "/tmp/test.txt" {
		t.Errorf("expected path /tmp/test.txt, got %q", tc.Args["path"])
	}
	if tc.Args["content"] != "Hello world" {
		t.Errorf("expected content 'Hello world', got %q", tc.Args["content"])
	}
}

func TestParseXMLToolCalls_SimplifiedParams_MultipleValuesForSameParam(t *testing.T) {
	// Модель может сгенерировать несколько параметров с одинаковым именем
	// (например, несколько path для read_file)
	// В этом случае значения должны быть объединены или взято последнее
	input := `<function=read_file>
<path>/tmp/file1.txt</path>
<path>/tmp/file2.txt</path>
<path>/tmp/file3.txt</path>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("expected read_file, got %q", tc.Name)
	}
	// Проверяем, что путь был распарсен (последнее значение или все объединены)
	if tc.Args["path"] == "" {
		t.Errorf("expected non-empty path, got empty")
	}
}

func TestParseXMLToolCalls_SimplifiedParams_MultilineValue(t *testing.T) {
	input := `<function=file_write>
<path>/tmp/test.txt</path>
<content>Line 1
Line 2
Line 3</content>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if !strings.Contains(tc.Args["content"], "Line 1") {
		t.Errorf("expected content to contain 'Line 1', got %q", tc.Args["content"])
	}
}

func TestParseXMLToolCalls_SimplifiedParams_RealWorldExample(t *testing.T) {
	// Реальный пример из баг-репорта
	input := `<function=read_file>
<path>/Users/g.rylov/Documents/projects/go/confluence_exporter/out/Разработка_ Документация.html</path>
<path>/Users/g.rylov/Documents/projects/go/confluence_exporter/out/Сервис TestData.html</path>
<path>/Users/g.rylov/Documents/projects/go/confluence_exporter/out/VK Android • Automation.html</path>
</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("expected read_file, got %q", tc.Name)
	}
}

// TestParseXMLToolCalls_FunctionTagContentFormat — тест для формата,
// который реально выдаёт модель: <function>name</function> (значение как содержимое тега).
// Параметры могут быть внутри <function>...</function>.
func TestParseXMLToolCalls_FunctionTagContentFormat(t *testing.T) {
	input := `<tool_call>
<function>read_file_text<parameter=path>
/home/orangepi/projects/go/agent/README.md
</parameter></function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "read_file_text",
				Args: map[string]string{
					"path": "/home/orangepi/projects/go/agent/README.md",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

// TestParseXMLToolCalls_FunctionTagContentFormat_Malformed — тест для формата,
// где модель пишет <function>read_file_text> (без закрывающего тега, с > в конце содержимого).
func TestParseXMLToolCalls_FunctionTagContentFormat_Malformed(t *testing.T) {
	input := `<tool_call>
<function>read_file_text>
<parameter=path>
/home/orangepi/projects/go/agent/README.md
</parameter>
</function>
</tool_call>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (content: %q)", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read_file_text" {
		t.Errorf("expected read_file_text, got %q", tc.Name)
	}
	if tc.Args["path"] != "/home/orangepi/projects/go/agent/README.md" {
		t.Errorf("expected path, got %q", tc.Args["path"])
	}
}

// TestParseXMLToolCalls_FunctionTagContentFormat_NoWrapper — упрощённый формат без <tool_call>
func TestParseXMLToolCalls_FunctionTagContentFormat_NoWrapper(t *testing.T) {
	input := `<function>time_get</function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "time_get" {
		t.Errorf("expected time_get, got %q", result.ToolCalls[0].Name)
	}
}

// TestParseXMLToolCalls_MalformedCloseTagInFunction проверяет, что парсер
// корректно обрабатывает malformed XML: <tool_call><function=name></parameter></task></tool_call>
// (LLM закрывает </parameter> и </task> вместо </function>).
func TestParseXMLToolCalls_MalformedCloseTagInFunction(t *testing.T) {
	input := "<tool_call>\n<function=review_approve>\n</parameter>\n</task>\n</tool_call>"
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "review_approve" {
		t.Errorf("expected tool name 'review_approve', got %q", result.ToolCalls[0].Name)
	}
}

// TestParseXMLToolCalls_SubagentAttrParam тестирует формат:
// <tool_call><function=subagent><parameter name="subagent_type">worker</parameter><parameter name="prompt">Create HTTP server</parameter></function></tool_call>
func TestParseXMLToolCalls_SubagentAttrParam(t *testing.T) {
	input := `<tool_call><function=subagent><parameter name="subagent_type">worker</parameter><parameter name="prompt">Create a simple HTTP server in Go</parameter></function></tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "subagent",
				Args: map[string]string{
					"subagent_type": "worker",
					"prompt":        "Create a simple HTTP server in Go",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

// TestParseXMLToolCalls_SubagentAttrParamNoWrapper тестирует формат без <tool_call> обёртки:
// <function=subagent><parameter name="subagent_type">worker</parameter><parameter name="prompt">Create HTTP server</parameter></function>
func TestParseXMLToolCalls_SubagentAttrParamNoWrapper(t *testing.T) {
	input := `<function=subagent><parameter name="subagent_type">worker</parameter><parameter name="prompt">Create a simple HTTP server in Go</parameter></function>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "subagent",
				Args: map[string]string{
					"subagent_type": "worker",
					"prompt":        "Create a simple HTTP server in Go",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

// TestParseXMLToolCalls_SubagentSimpleTags тестирует упрощённый формат:
// <function=subagent><subagent_type>worker</subagent_type><prompt>Create</prompt></function>
func TestParseXMLToolCalls_SubagentSimpleTags(t *testing.T) {
	input := `<function=subagent><subagent_type>worker</subagent_type><prompt>Create HTTP server</prompt></function>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "subagent",
				Args: map[string]string{
					"subagent_type": "worker",
					"prompt":        "Create HTTP server",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

// TestParseXMLToolCalls_SubagentMixedFormats тестирует смешанный формат:
// <function=subagent><parameter=subagent_type>worker</parameter><parameter name="prompt">Create</parameter></function>
func TestParseXMLToolCalls_SubagentMixedFormats(t *testing.T) {
	input := `<function=subagent><parameter=type>worker</parameter><parameter name="prompt">Create HTTP server</parameter></function>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "subagent",
				Args: map[string]string{
					"type":   "worker",
					"prompt": "Create HTTP server",
				},
			},
		},
	}
	result := ParseXMLToolCalls(input)
	if !eq(result, expected) {
		t.Errorf("ParseXMLToolCalls(%q) =\n  %+v\n  want %+v", input, result, expected)
	}
}

// TestParseXMLToolCalls_SubagentStandardFormat тестирует стандартный XML формат:
// <tool_call><function=subagent><parameter name="subagent_type">worker</parameter><parameter name="prompt">task</parameter><parameter name="description">desc</parameter></function></tool_call>
func TestParseXMLToolCalls_SubagentStandardFormat(t *testing.T) {
	input := `<tool_call>
<function=subagent>
<parameter name="subagent_type">worker</parameter>
<parameter name="prompt">Create HTTP server</parameter>
<parameter name="description">Simple HTTP server</parameter>
</function>
</tool_call>`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "subagent",
				Args: map[string]string{
					"subagent_type": "worker",
					"prompt":        "Create HTTP server",
					"description":   "Simple HTTP server",
				},
			},
		},
	}
	assertParse(t, input, expected)
}

// TestParseXMLToolCalls_AttrParamInReasoning тестирует формат с аттрибутами в reasoning тексте
func TestParseXMLToolCalls_AttrParamInReasoning(t *testing.T) {
	input := "I'll create the HTTP server for you.\n\n<function=subagent>\n<parameter name=\"subagent_type\">worker</parameter>\n<parameter name=\"prompt\">Create a simple HTTP server in Go that serves GET /hello returning {\"message\":\"hello\"}. Use only stdlib. Listen on :8080.</parameter>\n</function>"
	result := ParseXMLToolCalls(input)
	// Check tool calls first
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "subagent" {
		t.Errorf("expected subagent, got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].Args["subagent_type"] != "worker" {
		t.Errorf("expected worker, got %q", result.ToolCalls[0].Args["subagent_type"])
	}
	if !strings.Contains(result.ToolCalls[0].Args["prompt"], "HTTP server") {
		t.Errorf("prompt should mention HTTP server, got: %s", result.ToolCalls[0].Args["prompt"])
	}
	if !strings.Contains(result.Content, "I'll create") {
		t.Errorf("content should retain text before tool call, got: %s", result.Content)
	}
}

// TestParseXMLToolCalls_EmptyAttrParam тестирует пустые значения в attr параметрах
func TestParseXMLToolCalls_EmptyAttrParam(t *testing.T) {
	input := `<function=subagent><parameter name="name"></parameter><parameter name="prompt">task</parameter></function>`
	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Args["prompt"] != "task" {
		t.Errorf("expected prompt='task', got %q", result.ToolCalls[0].Args["prompt"])
	}
}

// TestParseXMLToolCalls_SubagentAllAliases тестирует что все алиасы имён параметров работают
func TestParseXMLToolCalls_SubagentAllAliases(t *testing.T) {
	tests := []struct {
		name     string
		xml      string
		expected string // expected value for the "name" equivalent
	}{
		{
			"parameter=subagent_type",
			`<function=subagent><parameter=subagent_type>worker</parameter><parameter=prompt>task</parameter></function>`,
			"worker",
		},
		{
			"parameter=name",
			`<function=subagent><parameter=name>worker</parameter><parameter=prompt>task</parameter></function>`,
			"worker",
		},
		{
			"parameter=agent",
			`<function=subagent><parameter=agent>worker</parameter><parameter=prompt>task</parameter></function>`,
			"worker",
		},
		{
			"parameter=type",
			`<function=subagent><parameter=type>worker</parameter><parameter=prompt>task</parameter></function>`,
			"worker",
		},
		{
			"attr subagent_type",
			`<function=subagent><parameter name="subagent_type">worker</parameter><parameter name="prompt">task</parameter></function>`,
			"worker",
		},
		{
			"attr name",
			`<function=subagent><parameter name="type">worker</parameter><parameter name="task">task</parameter></function>`,
			"worker",
		},
		{
			"simplified subagent_type",
			`<function=subagent><subagent_type>worker</subagent_type><prompt>task</prompt></function>`,
			"worker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseXMLToolCalls(tt.xml)
			if len(result.ToolCalls) != 1 {
				t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
			}
			// Check that at least one of the name aliases has the expected value
			args := result.ToolCalls[0].Args
			found := args["subagent_type"] == tt.expected ||
				args["name"] == tt.expected ||
				args["agent"] == tt.expected ||
				args["type"] == tt.expected
			if !found {
				t.Errorf("no name alias found with value %q in args: %v", tt.expected, args)
			}
			// Check that prompt or task has content
			if args["prompt"] == "" && args["task"] == "" {
				t.Errorf("no task/prompt found in args: %v", args)
			}
		})
	}
}
