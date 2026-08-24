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

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/buildinfo"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/vk"
)

var Version = "dev"

type Config struct {
	TokenVK        string `json:"token_vk"`
	PeerID         int64  `json:"peer_id"`
	ThinkingPeerID int64  `json:"thinking_peer_id"`
	Debug          bool   `json:"debug"`
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

	go func(c *exec.Cmd) {
		waitErr := c.Wait()
		ap.mu.Lock()
		defer ap.mu.Unlock()
		if ap.cmd != c {
			return
		}
		ap.cmd = nil
		if waitErr != nil {
			fmt.Printf("[restarter] Agent exited (PID %d): %v\n", c.Process.Pid, waitErr)
		} else {
			fmt.Printf("[restarter] Agent stopped (PID %d)\n", c.Process.Pid)
		}
	}(cmd)
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

func buildAgentArgs(cfgDebug bool) []string {
	restarterDebug := flag.Bool("d", false, "Enable debug mode")
	flag.Parse()

	var args []string
	if cfgDebug || *restarterDebug {
		args = append(args, "-d")
	}
	return args
}

func main() {
	agentDir, _ := os.Getwd()

	config, err := loadConfig(filepath.Join(agentDir, "config.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[restarter] Error loading config: %v\n", err)
		os.Exit(1)
	}
	agentArgs := buildAgentArgs(config.Debug)
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		<-sigChan
		fmt.Println("[restarter] Shutting down...")
		agent.stop()
		cancel()
		close(done)
	}()

	go monitorAgent(ctx, &agent, agentPath, agentArgs, vkClient, config.PeerID)
	go runVKListener(ctx, vkClient, config, &agent, agentPath, agentArgs)

	sendWelcome(vkClient, config.PeerID)

	if err := agent.start(agentPath, agentArgs); err != nil {
		fmt.Fprintf(os.Stderr, "[restarter] Failed to auto-start agent: %v\n", err)
		vkClient.SendMessage(config.PeerID, fmt.Sprintf("❌ Не удалось запустить агента: %v", err))
	}

	<-done
	fmt.Println("[restarter] Shutdown complete")
}


func sendWelcome(vkClient *vk.BotClient, peerID int64) {
	if peerID <= 0 {
		return
	}
	msg := fmt.Sprintf("🤖 Restarter v%s запущен. Агент стартовал автоматически.\nКоманды: /status, /stop, /restart, /update\n", Version)
	vkClient.SendMessage(peerID, msg)
}


func monitorAgent(ctx context.Context, ap *agentProc, agentPath string, agentArgs []string, vkClient *vk.BotClient, peerID int64) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	
	wd, _ := os.Getwd()
	restartSignal := filepath.Join(wd, ".agent-restart")
	updateSignal := filepath.Join(wd, ".agent-update")
	branchSignal := filepath.Join(wd, ".agent-branch")

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[restarter] monitorAgent stopping")
			return
		case <-ticker.C:
			isRunning := ap.isRunning()
			if !isRunning {
				fmt.Println("[restarter] Agent not running — waiting for /restart command")
			}

			
			if _, err := os.Stat(restartSignal); err == nil {
				os.Remove(restartSignal)
				fmt.Println("[restarter] Restart requested via signal file")
				restartAgent(ap, agentPath, agentArgs, vkClient, peerID)
				continue
			}

			
			if _, err := os.Stat(updateSignal); err == nil {
				os.Remove(updateSignal)
				fmt.Println("[restarter] Update requested via signal file")
				updateAgent(ap, agentPath, agentArgs, vkClient, peerID)
				continue
			}

			
			if branchData, err := os.ReadFile(branchSignal); err == nil {
				os.Remove(branchSignal)
				branch := strings.TrimSpace(string(branchData))
				fmt.Printf("[restarter] Branch switch requested: %s\n", branch)
				switchBranch(ap, agentPath, agentArgs, vkClient, peerID, branch)
			}
		}
	}
}

func restartAgent(ap *agentProc, agentPath string, agentArgs []string, vkClient *vk.BotClient, peerID int64) {
	fmt.Println("[restarter] Restarting agent...")
	ap.stop()
	if err := ap.start(agentPath, agentArgs); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Ошибка перезапуска: %v", err))
		time.Sleep(5 * time.Second)
	}
}

func updateAgent(ap *agentProc, agentPath string, agentArgs []string, vkClient *vk.BotClient, peerID int64) {
	fmt.Println("[restarter] Updating agent: git pull + build + restart...")
	ap.stop()

	output, err := exec.Command("git", "pull").CombinedOutput()
	if err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ git pull failed:\n%s", stringutil.Truncate(string(output), 500, "...")))
		ap.start(agentPath, agentArgs)
		return
	}

	if err := buildAgent(agentPath); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Build failed: %v", err))
		ap.start(agentPath, agentArgs)
		return
	}

	if err := ap.start(agentPath, agentArgs); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Restart failed: %v", err))
	} else {
		vkClient.SendMessage(peerID, "✅ Агент обновлён и перезапущен")
	}
}

func switchBranch(ap *agentProc, agentPath string, agentArgs []string, vkClient *vk.BotClient, peerID int64, branch string) {
	fmt.Printf("[restarter] Switching to branch: %s\n", branch)
	ap.stop()

	output, err := exec.Command("git", "fetch", "--all").CombinedOutput()
	if err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ git fetch failed:\n%s", stringutil.Truncate(string(output), 500, "...")))
		ap.start(agentPath, agentArgs)
		return
	}

	output, err = exec.Command("git", "checkout", branch).CombinedOutput()
	if err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ git checkout %s failed:\n%s", branch, stringutil.Truncate(string(output), 500, "...")))
		ap.start(agentPath, agentArgs)
		return
	}

	output, err = exec.Command("git", "pull", "--ff-only").CombinedOutput()
	if err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ git pull failed:\n%s", stringutil.Truncate(string(output), 500, "...")))
		ap.start(agentPath, agentArgs)
		return
	}

	if err := buildAgent(agentPath); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Build failed: %v", err))
		ap.start(agentPath, agentArgs)
		return
	}

	if err := ap.start(agentPath, agentArgs); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Restart failed: %v", err))
	} else {
		vkClient.SendMessage(peerID, fmt.Sprintf("✅ Переключено на %s, агент перезапущен", branch))
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
				if msg.EventID != "" {
					continue
				}
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

				
				
				
				
				
				
				if ap.isRunning() {
					switch {
					case cmd == "/status":
						sendRestarterStatus(vkClient, replyPeerID, ap)
					case cmd == "/help":
						vkClient.SendMessage(replyPeerID, restarterHelpText())
					case cmd == "/stop":
						vkClient.SendMessage(replyPeerID, "Останавливаю агента...")
						ap.stop()
					case strings.HasPrefix(cmd, "/r "):
						handleModelSwitch(vkClient, replyPeerID, cmd)
					}
					continue
				}

				switch {
				case cmd == "/restart":
					vkClient.SendMessage(replyPeerID, "Перезапуск агента...")
					ap.setRestarting(true)
					ap.stop()
					if err := ap.start(agentPath, agentArgs); err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ Ошибка перезапуска: %v", err))
					}
					ap.setRestarting(false)

				case cmd == "/update":
					vkClient.SendMessage(replyPeerID, "Обновление агента: git pull, build, restart...")
					ap.setRestarting(true)
					ap.stop()

					output, err := exec.Command("git", "pull").CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ git pull failed:\n%s", stringutil.Truncate(string(output), 500, "...")))
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
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ git fetch failed:\n%s", stringutil.Truncate(string(output), 500, "...")))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					output, err = exec.Command("git", "checkout", branch).CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("❌ git checkout %s failed:\n%s", branch, stringutil.Truncate(string(output), 500, "...")))
						ap.start(agentPath, agentArgs)
						ap.setRestarting(false)
						break
					}

					output, err = exec.Command("git", "pull").CombinedOutput()
					if err != nil {
						vkClient.SendMessage(replyPeerID, fmt.Sprintf("⚠️ git pull warning:\n%s", stringutil.Truncate(string(output), 500, "...")))
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
					sendRestarterStatus(vkClient, replyPeerID, ap)

				case cmd == "/stop":
					vkClient.SendMessage(replyPeerID, "Агент не запущен")

				case cmd == "/help":
					vkClient.SendMessage(replyPeerID, restarterHelpText())

				case strings.HasPrefix(cmd, "/r "):
					handleModelSwitch(vkClient, replyPeerID, cmd)

				default:
				}
			}
		}
	}
}


func sendRestarterStatus(vkClient *vk.BotClient, peerID int64, ap *agentProc) {
	status := fmt.Sprintf("Restarter v%s (build %s)\n", Version, buildinfo.HumanReadable())
	if ap.isRunning() {
		status += fmt.Sprintf("Агент: запущен (PID %d)\n", ap.pid())
	} else {
		status += "Агент: остановлен\n"
	}
	branchOutput, _ := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if len(branchOutput) > 0 {
		status += fmt.Sprintf("Ветка: %s", strings.TrimSpace(string(branchOutput)))
	}
	vkClient.SendMessage(peerID, status)
}

func restarterHelpText() string {
	return "Restarter команды:\n" +
		"/restart - Перезапустить агента без пересборки\n" +
		"/stop - Остановить агента\n" +
		"/update - git pull, пересобрать, перезапустить\n" +
		"/b <branch> - Переключиться на ветку, пересобрать, перезапустить\n" +
		"/status - Статус агента и текущая ветка\n" +
		"/help - Показать список команд"
}

func buildAgent(agentPath string) error {
	output, err := exec.Command("sh", "./build.sh").CombinedOutput()
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

func handleModelSwitch(vkClient *vk.BotClient, peerID int64, cmd string) {
	alias := strings.TrimSpace(strings.TrimPrefix(cmd, "/r"))
	if alias == "" {
		vkClient.SendMessage(peerID, "Укажите модель: /r <alias>. Доступные модели см. в models.json")
		return
	}

	wd, _ := os.Getwd()
	modelsPath := filepath.Join(wd, "models.json")
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Не удалось прочитать models.json: %v", err))
		return
	}

	var cfg modelsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Не удалось разобрать models.json: %v", err))
		return
	}

	if _, ok := cfg.Models[alias]; !ok {
		avail := make([]string, 0, len(cfg.Models))
		for k := range cfg.Models {
			avail = append(avail, k)
		}
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Модель %q не найдена. Доступные: %s", alias, strings.Join(avail, ", ")))
		return
	}

	cfg.Default = alias
	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Ошибка: %v", err))
		return
	}
	if err := os.WriteFile(modelsPath, out, 0644); err != nil {
		vkClient.SendMessage(peerID, fmt.Sprintf("❌ Не удалось сохранить models.json: %v", err))
		return
	}
	vkClient.SendMessage(peerID, fmt.Sprintf("✅ Модель переключена на %q", alias))
}

type modelsConfig struct {
	Default string                `json:"default"`
	Models  map[string]struct{}   `json:"models"`
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

