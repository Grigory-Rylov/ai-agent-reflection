package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"flag"

	"github.com/opencode/llama-client/pkg/vk"
)

var Version = "dev"

type Config struct {
	TokenVK        string `json:"token_vk"`
	PeerID         int64  `json:"peer_id"`
	ThinkingPeerID int64  `json:"thinking_peer_id"`
}

type agentProc struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	restarting bool
}

func (ap *agentProc) start(agentPath string, args []string) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	ap.killLocked()

	cmd := exec.Command(agentPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	ap.cmd = cmd
	fmt.Printf("[restarter] Agent started (PID %d)\n", cmd.Process.Pid)
	return nil
}

func (ap *agentProc) stop() {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	ap.killLocked()
}

func (ap *agentProc) killLocked() {
	if ap.cmd == nil || ap.cmd.Process == nil {
		return
	}
	pid := ap.cmd.Process.Pid
	ap.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		ap.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		ap.cmd.Process.Kill()
		<-done
	}
	fmt.Printf("[restarter] Agent stopped (PID %d)\n", pid)
	ap.cmd = nil
}

func (ap *agentProc) isRunning() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.cmd != nil && ap.cmd.Process != nil && ap.cmd.ProcessState == nil
}

func (ap *agentProc) setRestarting(v bool) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	ap.restarting = v
}

func (ap *agentProc) pid() int {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.cmd != nil && ap.cmd.Process != nil {
		return ap.cmd.Process.Pid
	}
	return 0
}

func buildAgentArgs() []string {
	restarterDebug := flag.Bool("d", false, "Enable debug mode")
	flag.Parse()

	var args []string
	if *restarterDebug {
		args = append(args, "-d")
	}
	return args
}

func main() {
	agentArgs := buildAgentArgs()
	agentDir, _ := os.Getwd()

	config, err := loadConfig(filepath.Join(agentDir, "config.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[restarter] Error loading config: %v\n", err)
		os.Exit(1)
	}
	if config.TokenVK == "" {
		fmt.Fprintln(os.Stderr, "[restarter] token_vk is required in config.json")
		os.Exit(1)
	}

	vkClient := vk.NewBotClient(config.TokenVK)
	var agent agentProc
	agentPath := filepath.Join(agentDir, "agent")

	if _, err := os.Stat(agentPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[restarter] Agent binary not found at %s, building...\n", agentPath)
		if err := buildAgent(agentPath); err != nil {
			fmt.Fprintf(os.Stderr, "[restarter] Initial build failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[restarter] Agent built successfully")
	}

	if err := agent.start(agentPath, agentArgs); err != nil {
		fmt.Fprintf(os.Stderr, "[restarter] Failed to start agent: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("[restarter] Shutting down...")
		agent.stop()
		cancel()
		os.Exit(0)
	}()

	go monitorAgent(&agent, agentPath, agentArgs)

	runVKListener(ctx, vkClient, config, &agent, agentPath, agentArgs)
}

func monitorAgent(ap *agentProc, agentPath string, agentArgs []string) {
	for {
		time.Sleep(2 * time.Second)
		if ap.isRunning() {
			continue
		}
		ap.mu.Lock()
		if ap.restarting {
			ap.mu.Unlock()
			continue
		}
		ap.mu.Unlock()

		fmt.Println("[restarter] Agent died, restarting...")
		if err := ap.start(agentPath, agentArgs); err != nil {
			fmt.Fprintf(os.Stderr, "[restarter] Restart failed: %v\n", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func runVKListener(ctx context.Context, vkClient *vk.BotClient, config Config, ap *agentProc, agentPath string, agentArgs []string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			server, key, ts, err := vkClient.GetLongPollServer()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[restarter] GetLongPollServer error: %v\n", err)
				time.Sleep(3 * time.Second)
				continue
			}
			if err := pollLoop(ctx, vkClient, server, key, ts, config, ap, agentPath, agentArgs); err != nil {
				fmt.Fprintf(os.Stderr, "[restarter] Poll error: %v\n", err)
				time.Sleep(3 * time.Second)
			}
		}
	}
}

func pollLoop(ctx context.Context, vkClient *vk.BotClient, server, key string, ts int64, config Config, ap *agentProc, agentPath string, agentArgs []string) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			messages, newTs, err := vkClient.CheckUpdates(ctx, server, key, ts)
			if err != nil {
				return err
			}
			ts = newTs

			for _, msg := range messages {
				if config.ThinkingPeerID > 0 && msg.PeerID == config.ThinkingPeerID {
					continue
				}

				cmd := extractCommand(msg.Text)
				if cmd == "" {
					continue
				}

				replyPeerID := msg.PeerID
				if config.PeerID > 0 {
					replyPeerID = config.PeerID
				}

				switch {
				case cmd == "/restart":
					vkClient.SendMessage(replyPeerID, "Перезапуск агента...")
					ap.setRestarting(true)
					ap.stop()
					if err := ap.start(agentPath, agentArgs); err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ Ошибка перезапуска: %v", err))
					} else {
						vkClient.SendMessage(replyPeerID, "✅ Агент перезапущен")
					}
					ap.setRestarting(false)

				case cmd == "/update":
					vkClient.SendMessage(replyPeerID, "Обновление агента: git pull, build, restart...")
					ap.setRestarting(true)
					ap.stop()

					output, err := exec.Command("git", "pull").CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ git pull failed:\n%s", truncate(string(output), 500)))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					if err := buildAgent(agentPath); err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ Build failed: %v", err))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					if err := ap.start(agentPath, agentArgs); err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ Restart failed: %v", err))
					} else {
						vkClient.SendMessage(replyPeerID, "✅ Агент обновлён и перезапущен")
					}
					ap.setRestarting(false)

				case strings.HasPrefix(cmd, "/b "):
					branch := strings.TrimSpace(strings.TrimPrefix(cmd, "/b "))
					if branch == "" {
						vkClient.SendMessage(replyPeerID, "Укажите ветку: /b <branch>")
						break
					}
					vkClient.SendMessage(replyPeerID, fmt.Sprintf("Переключение на ветку %s...", branch))
					ap.setRestarting(true)
					ap.stop()

					output, err := exec.Command("git", "fetch", "--all").CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ git fetch failed:\n%s", truncate(string(output), 500)))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					output, err = exec.Command("git", "checkout", branch).CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ git checkout %s failed:\n%s", branch, truncate(string(output), 500)))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					output, err = exec.Command("git", "pull").CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("⚠️ git pull warning:\n%s", truncate(string(output), 500)))
					}

					if err := buildAgent(agentPath); err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ Build failed: %v", err))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					if err := ap.start(agentPath, agentArgs); err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ Restart failed: %v", err))
					} else {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("✅ Переключено на %s, агент перезапущен", branch))
					}
					ap.setRestarting(false)

				case cmd == "/status":
					status := fmt.Sprintf("Restarter v%s\n", Version)
					if ap.isRunning() {
						status += fmt.Sprintf("Агент: запущен (PID %d)\n", ap.pid())
					} else {
						status += "Агент: остановлен\n"
					}
					branchOutput, _ := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
					if len(branchOutput) > 0 {
						status += fmt.Sprintf("Ветка: %s", strings.TrimSpace(string(branchOutput)))
					}
					vkClient.SendMessage(replyPeerID, status)

				case cmd == "/help":
					help := "Restarter команды:\n" +
						"/restart - Перезапустить агента без пересборки\n" +
						"/update - git pull, пересобрать, перезапустить\n" +
						"/b <branch> - Переключиться на ветку, пересобрать, перезапустить\n" +
						"/status - Статус агента и текущая ветка\n" +
						"/help - Показать список команд"
					vkClient.SendMessage(replyPeerID, help)

				default:
				}
			}
		}
	}
}

func buildAgent(agentPath string) error {
	output, err := exec.Command("/usr/local/go/bin/go", "build", "-o", agentPath, ".").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	fmt.Println("[restarter] Agent rebuilt successfully")
	return nil
}

func extractCommand(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 0 && message[0] == '[' {
		closeIdx := strings.Index(message, "]")
		if closeIdx > 0 && closeIdx < len(message)-1 {
			rest := strings.TrimSpace(message[closeIdx+1:])
			if strings.HasPrefix(rest, "/") {
				return rest
			}
			return ""
		}
	}
	if strings.HasPrefix(message, "/") {
		return message
	}
	return ""
}

func loadConfig(path string) (Config, error) {
	var config Config
	data, err := os.ReadFile(path)
	if err != nil {
		homeDir, _ := os.UserHomeDir()
		globalPath := filepath.Join(homeDir, ".config", "ai-agent", "config.json")
		data, err = os.ReadFile(globalPath)
		if err != nil {
			return config, fmt.Errorf("config not found at '%s' or '%s': %w", path, globalPath, err)
		}
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	return config, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
