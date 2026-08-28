package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
)

func (s *Session) loadFromStore(st store.Store) error {
	sd, err := st.GetSession(s.config.PeerID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sd == nil {
		return nil
	}

	s.createdAt = sd.CreatedAt
	s.updatedAt = sd.UpdatedAt
	s.workingDir = sd.WorkingDir
	s.pinned = make([]string, len(sd.Pinned))
	copy(s.pinned, sd.Pinned)
	s.resumePrompt = sd.ResumePrompt

	if sd.WorkingDir != "" {
		s.config.WorkingDir = sd.WorkingDir
	}

	msgData, err := st.GetMessages(s.config.PeerID)
	if err != nil {
		return fmt.Errorf("get messages: %w", err)
	}

	s.messages = make([]Message, 0, len(msgData))
	for _, d := range msgData {
		msg, err := storeMsgToMessage(d)
		if err != nil {
			continue
		}

		if msg.Role == SystemRole && len(s.messages) == 0 {
			s.config.SystemPrompt = msg.Content
		}

		s.messages = append(s.messages, msg)

		if msg.Role == AssistantRole {
			s.checkLoop(msg.Content)
		}
	}

	s.messages = TrimDanglingTrailingToolCalls(s.messages)

	return nil
}


func TrimDanglingTrailingToolCalls(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for {
		n := len(out)
		if n == 0 {
			break
		}
		last := out[n-1]
		if !(last.Role == AssistantRole && len(last.ToolCalls) > 0) {
			break
		}
		out = out[:n-1]
	}
	return out
}

func (s *Session) saveToStore(st store.Store) error {
	sd := &store.SessionData{
		PeerID:       s.config.PeerID,
		CreatedAt:    s.createdAt,
		UpdatedAt:    s.updatedAt,
		WorkingDir:   s.workingDir,
		LoopCount:    s.loopCount,
		IsLooped:     s.isLooped,
		Pinned:       s.pinned,
		ResumePrompt: s.resumePrompt,
	}

	if s.isLooped {
		if last := s.getLastAssistantMessageLocked(); last != nil {
			sd.LastLooped = last.Content
		}
	}

	msgs := make([]store.MessageData, 0, len(s.messages))
	for _, msg := range s.messages {
		msgs = append(msgs, messageToStoreMsg(msg))
	}

	return st.ReplaceSessionMessages(sd, msgs)
}

func messageToStoreMsg(msg Message) store.MessageData {
	d := store.MessageData{
		Role:        string(msg.Role),
		Content:     msg.Content,
		ToolCallID:  msg.ToolCallID,
		ToolName:    msg.Name,
		Timestamp:   msg.Timestamp.Format(time.RFC3339),
		Summary:     msg.Summary,
		Compacted:   msg.Compacted,
		TailStartID: msg.TailStartID,
	}
	if len(msg.ToolCalls) > 0 {
		data, _ := json.Marshal(msg.ToolCalls)
		d.ToolCalls = string(data)
	}
	return d
}

func storeMsgToMessage(d store.MessageData) (Message, error) {
	ts, _ := time.Parse(time.RFC3339, d.Timestamp)
	msg := Message{
		Role:        Role(d.Role),
		Content:     d.Content,
		ToolCallID:  d.ToolCallID,
		Name:        d.ToolName,
		Timestamp:   ts,
		Summary:     d.Summary,
		Compacted:   d.Compacted,
		TailStartID: d.TailStartID,
	}
	if d.ToolCalls != "" {
		var calls []MsgToolCall
		if err := json.Unmarshal([]byte(d.ToolCalls), &calls); err != nil {
			return msg, fmt.Errorf("parse tool_calls: %w", err)
		}
		msg.ToolCalls = calls
	}
	return msg, nil
}
