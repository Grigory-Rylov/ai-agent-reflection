package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencode/llama-client/pkg/store"
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

	return nil
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

	if err := st.SaveSession(sd); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	if !s.config.AutoSave {
		return nil
	}

	if err := st.ClearMessages(s.config.PeerID); err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}

	for _, msg := range s.messages {
		d := messageToStoreMsg(msg)
		if err := st.AddMessage(s.config.PeerID, d); err != nil {
			return fmt.Errorf("add message: %w", err)
		}
	}

	return nil
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
