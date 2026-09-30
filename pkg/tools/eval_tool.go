package tools

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed eval_host.py
var evalHostPy string

//go:embed eval_host.js
var evalHostJS string

type EvalTool struct{}

type evalRuntimeSpec struct {
	lang   string
	label  string
	binary string
	suffix string
	source string
}

type evalKernel struct {
	lang     string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   bytes.Buffer
	stderrMu sync.Mutex
	seq      int
}

type evalPayload struct {
	OK     bool   `json:"ok"`
	Stdout string `json:"stdout"`
	Value  string `json:"value"`
	Error  string `json:"error"`
}

type evalFrameResult struct {
	payload evalPayload
	err     error
}

var (
	evalMu      sync.Mutex
	evalKernels = map[string]*evalKernel{}
	evalRestart = map[string]bool{}
)

func (t *EvalTool) Name() string {
	return "eval"
}

func (t *EvalTool) Description() string {
	return "Execute Python or JavaScript code in a persistent kernel. State persists between calls per language: variables, imports and functions defined earlier stay available, so send small incremental steps instead of re-running setup. The value of the last expression is returned; print/console.log output is captured. On timeout the kernel is killed and restarts with clean state on the next call."
}

func (t *EvalTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"language":    CreateEnumParameter("language", `Runtime: "py" for Python, "js" for JavaScript (default: "py")`, []string{"py", "js"}, false),
			"code":        CreateStringParameter("code", "Code to run in the kernel, verbatim", true),
			"timeout_sec": CreateIntegerParameter("timeout_sec", "Execution timeout in seconds (default: 30)", false),
		},
		"required": []string{"code"},
	}
}

func (t *EvalTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	lang := inputs["language"]
	if lang == "" {
		lang = "py"
	}

	spec, ok := evalRuntime(lang)
	if !ok {
		return ToolResult{Success: false, Error: `language must be "py" or "js"`}, nil
	}

	code := inputs["code"]
	if code == "" {
		return ToolResult{Success: false, Error: "code parameter is required"}, nil
	}

	timeout := 30
	if raw, ok := inputs["timeout_sec"]; ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
			timeout = parsed
		}
	}

	evalMu.Lock()
	defer evalMu.Unlock()

	return runEvalCell(ctx, spec, code, timeout)
}

func runEvalCell(ctx context.Context, spec evalRuntimeSpec, code string, timeout int) (ToolResult, error) {
	kernel, err := ensureEvalKernel(spec)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}

	kernel.seq++
	if err := writeEvalFrame(kernel, kernel.seq, code); err != nil {
		stopEvalKernel(kernel)
		evalRestart[spec.lang] = true
		return ToolResult{Success: false, Error: fmt.Sprintf("Failed to send code to %s kernel: %v", spec.label, err)}, nil
	}

	respCh := make(chan evalFrameResult, 1)
	go func() {
		payload, err := readEvalFrame(kernel.stdout)
		respCh <- evalFrameResult{payload: payload, err: err}
	}()

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	select {
	case res := <-respCh:
		if res.err != nil {
			stopEvalKernel(kernel)
			evalRestart[spec.lang] = true
			return ToolResult{Success: false, Error: fmt.Sprintf("%s kernel failed: %v%s", spec.label, res.err, evalStderrTail(kernel))}, nil
		}
		return buildEvalResult(spec, res.payload), nil
	case <-timer.C:
		stopEvalKernel(kernel)
		evalRestart[spec.lang] = true
		return ToolResult{Success: false, Error: fmt.Sprintf("Execution timed out after %d seconds; %s kernel killed, state resets on next call", timeout, spec.label)}, nil
	case <-ctx.Done():
		stopEvalKernel(kernel)
		evalRestart[spec.lang] = true
		return ToolResult{Success: false, Error: fmt.Sprintf("Execution cancelled: %v", ctx.Err())}, nil
	}
}

func buildEvalResult(spec evalRuntimeSpec, payload evalPayload) ToolResult {
	note := ""
	if evalRestart[spec.lang] {
		note = "kernel restarted"
		delete(evalRestart, spec.lang)
	}

	if !payload.OK {
		errText := payload.Error
		if payload.Stdout != "" {
			errText = payload.Stdout + "\n" + errText
		}
		if note != "" {
			errText = note + ": state was reset\n" + errText
		}
		return ToolResult{
			Success: false,
			Error:   strings.TrimRight(errText, "\n"),
			Data:    map[string]interface{}{"stdout": payload.Stdout, "language": spec.lang},
		}
	}

	output := payload.Stdout
	if payload.Value != "" {
		if output != "" && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		output += payload.Value
	}
	if output == "" {
		output = "(no output)"
	}
	if note != "" {
		output = note + "\n" + output
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"output":   output,
			"stdout":   payload.Stdout,
			"value":    payload.Value,
			"language": spec.lang,
		},
	}
}

func ensureEvalKernel(spec evalRuntimeSpec) (*evalKernel, error) {
	if kernel, ok := evalKernels[spec.lang]; ok {
		return kernel, nil
	}

	binary, err := exec.LookPath(spec.binary)
	if err != nil {
		return nil, fmt.Errorf("%s is required for %s eval but was not found in PATH", spec.binary, spec.label)
	}

	scriptPath, err := writeEvalHostScript(spec)
	if err != nil {
		return nil, err
	}

	kernel, err := startEvalKernel(spec, binary, scriptPath)
	if err != nil {
		return nil, err
	}

	evalKernels[spec.lang] = kernel
	return kernel, nil
}

func startEvalKernel(spec evalRuntimeSpec, binary, scriptPath string) (*evalKernel, error) {
	cmd := exec.Command(binary, scriptPath)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open %s kernel stdin: %w", spec.label, err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open %s kernel stdout: %w", spec.label, err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open %s kernel stderr: %w", spec.label, err)
	}

	kernel := &evalKernel{
		lang:   spec.lang,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
	}

	go func() {
		kernel.stderrMu.Lock()
		defer kernel.stderrMu.Unlock()
		io.Copy(&kernel.stderr, stderrPipe) // nolint: errcheck
	}()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s kernel: %w", spec.label, err)
	}

	go func() { _ = cmd.Wait() }()

	return kernel, nil
}

func writeEvalHostScript(spec evalRuntimeSpec) (string, error) {
	file, err := os.CreateTemp("", "eval_host_*"+spec.suffix)
	if err != nil {
		return "", fmt.Errorf("failed to create temp host script: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(spec.source); err != nil {
		return "", fmt.Errorf("failed to write temp host script: %w", err)
	}

	return file.Name(), nil
}

func writeEvalFrame(kernel *evalKernel, id int, code string) error {
	raw := []byte(code)
	if _, err := fmt.Fprintf(kernel.stdin, "%d %d\n", id, len(raw)); err != nil {
		return err
	}
	_, err := kernel.stdin.Write(raw)
	return err
}

func readEvalFrame(reader *bufio.Reader) (evalPayload, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return evalPayload{}, fmt.Errorf("failed to read frame header: %w", err)
	}

	fields := strings.Fields(header)
	if len(fields) != 2 {
		return evalPayload{}, fmt.Errorf("malformed frame header: %q", header)
	}

	size, err := strconv.Atoi(fields[1])
	if err != nil {
		return evalPayload{}, fmt.Errorf("malformed frame size in header: %q", header)
	}

	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		return evalPayload{}, fmt.Errorf("failed to read frame body: %w", err)
	}

	var payload evalPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return evalPayload{}, fmt.Errorf("failed to decode frame payload: %w", err)
	}

	return payload, nil
}

func stopEvalKernel(kernel *evalKernel) {
	if kernel.cmd.Process != nil {
		if kernel.cmd.SysProcAttr != nil && kernel.cmd.SysProcAttr.Setpgid {
			syscall.Kill(-kernel.cmd.Process.Pid, syscall.SIGKILL) // nolint: errcheck
		} else {
			syscall.Kill(kernel.cmd.Process.Pid, syscall.SIGKILL) // nolint: errcheck
		}
	}
	kernel.stdin.Close() // nolint: errcheck
	delete(evalKernels, kernel.lang)
}

func evalStderrTail(kernel *evalKernel) string {
	kernel.stderrMu.Lock()
	defer kernel.stderrMu.Unlock()

	text := strings.TrimSpace(kernel.stderr.String())
	if text == "" {
		return ""
	}
	return ": " + text
}

func evalRuntime(lang string) (evalRuntimeSpec, bool) {
	switch lang {
	case "py":
		return evalRuntimeSpec{lang: "py", label: "Python", binary: "python3", suffix: ".py", source: evalHostPy}, true
	case "js":
		return evalRuntimeSpec{lang: "js", label: "JavaScript", binary: "node", suffix: ".js", source: evalHostJS}, true
	default:
		return evalRuntimeSpec{}, false
	}
}
