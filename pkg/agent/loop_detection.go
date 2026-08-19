package agent

import (
		"fmt"
		"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

const loopThreshold = 2


type responseLoopState struct {
	lastContent string
	lastSigs    []string
	count       int
	alertSent   bool
}


func (s *responseLoopState) observe(content string, sigs []string) int {
	eq := content == s.lastContent
	eqSigs := equalStringSlices(sigs, s.lastSigs)
	if eq && eqSigs {
		s.count++
		return s.count
	}
	s.lastContent = content
	s.lastSigs = sigs
	s.count = 0
	s.alertSent = false
	return 0
}


func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}


func normalizeLoopText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}


func (a *agentImpl) checkResponseLoop(sessionID int64, responseText, reasoningText string, toolCalls []ToolCall) int {
	if responseText == "" && reasoningText == "" && len(toolCalls) == 0 {
		return 0
	}
	content := responseText
	if content == "" {
		content = reasoningText
	}

	sigs := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		sigs = append(sigs, toolCallSignature(tc))
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	st := a.loopState(sessionID)
	st.count = st.observe(normalizeLoopText(content), sigs)

	if st.count < loopThreshold-1 || st.alertSent {
		return 0
	}

	st.alertSent = true
	repeats := st.count + 1
	logMsg := fmt.Sprintf("[LOOP] Model appears to be stuck in a loop: the same response was received %d times in a row", repeats)
	a.debugLog.Error("%s%s", a.agentPrefix(), logMsg)
	logger.DebugToFile("%s%s", a.agentPrefix(), logMsg)
	a.sendThinking(sessionID, fmt.Sprintf("[LOOP] It seems you are getting stuck in a loop: you have sent the same response %d times in a row. Please break out of the loop and try a different approach.", repeats))
	return repeats
}


func (a *agentImpl) injectLoopCorrection(session *sess.Session, repeats int) {
	correction := fmt.Sprintf("[SYSTEM] You are repeating yourself: you have sent the same response %d times in a row. You are stuck in a loop. Stop repeating. Analyze why your previous attempts did not progress and take a fundamentally different action, or provide the final answer if the task is already complete.", repeats)
	session.AddUserMessage(correction)
	a.sendThinking(session.GetPeerID(), "[LOOP] Injected corrective message into context")
}


func (a *agentImpl) resetResponseLoop(sessionID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.responseLoops, sessionID)
}


func (a *agentImpl) loopState(sessionID int64) *responseLoopState {
	st, ok := a.responseLoops[sessionID]
	if !ok {
		st = &responseLoopState{}
		a.responseLoops[sessionID] = st
	}
	return st
}
