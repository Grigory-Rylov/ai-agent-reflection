package vk

import (
	"fmt"
	"os/exec"
	"strings"
)

// PowerCommands implements the /shutdown and /reboot host-power commands.
// Only the restarter executes them (it stays alive even when the agent is
// stopped); the agent bot treats these commands as restarter commands and
// ignores them, so a single VK update is executed exactly once.
type PowerCommands struct {
	OwnerPeerID int64
	ShutdownCmd string
	RebootCmd   string
	// Send delivers the response text to the owner peer. May be nil (tests).
	Send func(peerID int64, text string)
}

// Handle processes a power command sent by senderPeerID.
// The command is a fixed configured string (never free-form input), run via
// sh -c in a detached goroutine so the reply is sent even if the command hangs.
func (p *PowerCommands) Handle(kind string, senderPeerID int64) {
	var resp string
	switch {
	case p.OwnerPeerID <= 0 || senderPeerID != p.OwnerPeerID:
		resp = "❌ Команда доступна только владельцу"
	case kind == "shutdown" && p.ShutdownCmd == "":
		resp = "❌ shutdown не настроен (укажите shutdown_cmd / reboot_cmd в config.json)"
	case kind == "reboot" && p.RebootCmd == "":
		resp = "❌ reboot не настроен (укажите shutdown_cmd / reboot_cmd в config.json)"
	default:
		cmd := p.ShutdownCmd
		if kind == "reboot" {
			cmd = p.RebootCmd
		}
		go func() {
			out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
			if err != nil {
				fmt.Printf("[power] %s failed: %v: %s\n", kind, err, strings.TrimSpace(string(out)))
				return
			}
			fmt.Printf("[power] %s completed\n", kind)
		}()
		if kind == "shutdown" {
			resp = "⚠️ Выполняю shutdown, машина выключается..."
		} else {
			resp = "⚠️ Выполняю reboot, машина перезагружается..."
		}
	}
	if p.Send != nil {
		p.Send(p.OwnerPeerID, resp)
	}
}
