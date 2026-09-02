package agentloop

import (
	"context"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type speculativeCompact struct {
	peerID     int64
	snapshot   []tokenizers.Message
	tokensAt   int
	msgCountAt int
	startedAt  time.Time
	ready      bool
	result     *compress.OpenCodeCompactResult
	err        error
	doneAt     time.Time
}

func (al *agentLoop) maybeStartSpeculativeCompact(ctx context.Context, sess *session.Session, peerID int64) {
	ratio := al.config.SpeculativeCompactRatio
	if ratio <= 0 {
		return
	}
	if al.compactor == nil {
		return
	}
	usable := compress.UsableWithLimits(al.config.MaxTokens, al.config.ModelLimitInput, al.config.CompactionReserved)
	if usable <= 0 {
		return
	}

	history := sess.GetHistory()
	visible := al.convertHistoryToMessages(history)
	tokens := compress.EstimateMessagesTokensSimple(visible)
	if tokens < int(float64(usable)*ratio) {
		return
	}

	al.specMu.Lock()
	if inFlight, ok := al.speculative[peerID]; ok && !inFlight.ready {
		al.specMu.Unlock()
		return
	}
	raw := al.convertHistoryToRawMessages(history)
	compactCtx, _ := context.WithTimeout(context.Background(), 10*time.Minute)
	task := &speculativeCompact{
		peerID:     peerID,
		snapshot:   raw,
		tokensAt:   tokens,
		msgCountAt: len(history),
		startedAt:  time.Now(),
	}
	al.speculative[peerID] = task
	al.specMu.Unlock()

	if al.log != nil {
		al.log.InfoLogf("[SPEC-COMPACT] Peer %d: started at %d tokens (%.0f%% of usable %d)", peerID, tokens, float64(tokens)/float64(usable)*100, usable)
	}

	go al.runSpeculativeCompact(compactCtx, task)
}

func (al *agentLoop) runSpeculativeCompact(ctx context.Context, task *speculativeCompact) {
	tailTurns := al.config.TailTurns
	if tailTurns <= 0 {
		tailTurns = 2
	}
	result, err := al.compactor.CompactWithOpenCode(ctx, task.snapshot, al.config.MaxTokens, tailTurns, al.config.PreserveRecentTokens)
	if err != nil {
		if al.log != nil {
			al.log.WarnLogf("[SPEC-COMPACT] Peer %d: speculative compaction failed: %v", task.peerID, err)
		}
	}
	al.specMu.Lock()
	current, ok := al.speculative[task.peerID]
	if ok && current == task {
		if err == nil {
			task.ready = true
			task.result = result
			task.doneAt = time.Now()
		}
		task.err = err
	}
	al.specMu.Unlock()

	if err == nil && al.log != nil {
		al.log.InfoLogf("[SPEC-COMPACT] Peer %d: ready after %s (%d -> %d tokens)", task.peerID, time.Since(task.startedAt), result.TokensBefore, result.TokensAfter)
	}
}

func (al *agentLoop) tryApplySpeculativeCompact(sess *session.Session, peerID int64) bool {
	al.specMu.Lock()
	task, ok := al.speculative[peerID]
	if !ok || !task.ready || task.err != nil || task.result == nil {
		al.specMu.Unlock()
		return false
	}
	history := sess.GetHistory()
	grew := len(history) > task.msgCountAt+2
	al.specMu.Unlock()

	if grew {
		al.cancelSpeculative(peerID)
		if al.log != nil {
			al.log.InfoLogf("[SPEC-COMPACT] Peer %d: discarded (history grew %d -> %d messages), falling back to synchronous compaction", peerID, task.msgCountAt, len(history))
		}
		return false
	}

	al.applyOpenCodeCompactResult(sess, task.result)
	if task.result.Summary != "" {
		sess.AddUserMessage(tokenizers.CompactionAutoContinueText)
	}
	sessionID := sess.GetSessionID()
	al.invalidateSessionSlot(context.Background(), sessionID)
	al.cancelSpeculative(peerID)
	if al.log != nil {
		al.log.InfoLogf("[SPEC-COMPACT] Peer %d: applied (%d -> %d tokens)", peerID, task.result.TokensBefore, task.result.TokensAfter)
	}
	return true
}

func (al *agentLoop) cancelSpeculative(peerID int64) {
	al.specMu.Lock()
	delete(al.speculative, peerID)
	al.specMu.Unlock()
}