package agentloop

import (
	"context"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
)


func AssignSessionSlot(mgr *SlotManager, client *SlotClient, holder slotModelProvider, sessionID string, log Logger) int {
	if mgr == nil || holder == nil || sessionID == "" {
		return -1
	}
	if !holder.GetCurrentSlotSave() {
		return -1
	}
	_, modelName, host := holder.GetCurrent()
	if host == "" {
		return -1
	}

	mgr.CheckAvailability(context.Background(), host, modelName)
	if !mgr.IsAvailable(host) {
		return -1
	}
	total := mgr.TotalSlots()
	if total <= 0 {
		return -1
	}

	slotID, evicted := mgr.GetOrAssign(sessionID, total)
	if slotID < 0 {
		return -1
	}

	if evicted != "" {
		
		filename := SlotFileName(modelName, slotID)
		if err := client.saveSlot(context.Background(), host, slotID, modelName, filename); err != nil {
			if log != nil {
				log.WarnLogf("[SLOT] save evicted session %s cache: %v", evicted, err)
			}
		} else if log != nil {
			log.InfoLogf("[SLOT] saved evicted session %s → slot %d (%s)", evicted, slotID, filename)
		}
		
		if err := client.eraseSlot(context.Background(), host, slotID, modelName); err != nil {
			if log != nil {
				log.DebugLogf("[SLOT] erase slot %d after eviction: %v", slotID, err)
			}
		}
		if log != nil {
			log.InfoLogf("[SLOT] evicted session %s from slot %d, new session %s starts cold", evicted, slotID, sessionID)
		}
		return slotID
	}

	
	filename := SlotFileName(modelName, slotID)
	if err := client.restoreSlot(context.Background(), host, slotID, modelName, filename); err != nil {
		switch {
		case IsSlotConfigError(err):
			mgr.MarkUnavailable(host)
			if mgr.ShouldLogUnavailable(host) && log != nil {
				log.InfoLogf("[SLOT] KV-cache save/restore unavailable on %s, skipping slot feature: %v", host, err)
			}
		case IsSlotMissingFileError(err):
			if log != nil {
				log.DebugLogf("[SLOT] restore slot %d for session %s: no cache file yet (cold)", slotID, sessionID)
			}
		default:
			if log != nil {
				log.WarnLogf("[SLOT] restore slot %d for session %s: %v", slotID, sessionID, err)
			}
		}
	} else if log != nil {
		log.InfoLogf("[SLOT] restore slot %d for session %s (%s)", slotID, sessionID, filename)
	}
	return slotID
}


func SaveSessionSlot(mgr *SlotManager, client *SlotClient, holder slotModelProvider, sessionID string, log Logger) {
	if mgr == nil || client == nil || holder == nil || sessionID == "" {
		return
	}
	_, modelName, host := holder.GetCurrent()
	if host == "" {
		return
	}
	slotID := mgr.GetSlotID(sessionID)
	if slotID < 0 {
		return
	}
	filename := SlotFileName(modelName, slotID)
	if err := client.saveSlot(context.Background(), host, slotID, modelName, filename); err != nil {
		if IsSlotConfigError(err) {
			mgr.MarkUnavailable(host)
			if mgr.ShouldLogUnavailable(host) && log != nil {
				log.InfoLogf("[SLOT] KV-cache save/restore unavailable on %s, skipping slot feature: %v", host, err)
			}
			return
		}
		if log != nil {
			log.WarnLogf("[SLOT] save slot %d for session %s: %v", slotID, sessionID, err)
		}
		return
	}
	if log != nil {
		log.InfoLogf("[SLOT] save slot %d for session %s (%s)", slotID, sessionID, filename)
	}
}


func ReleaseSessionSlot(mgr *SlotManager, client *SlotClient, holder slotModelProvider, sessionID string, log Logger) {
	if mgr == nil || client == nil || holder == nil || sessionID == "" {
		return
	}
	_, modelName, host := holder.GetCurrent()
	if host == "" {
		return
	}
	slotID := mgr.GetSlotID(sessionID)
	if slotID < 0 {
		return
	}
	if err := client.eraseSlot(context.Background(), host, slotID, modelName); err != nil {
		if log != nil {
			log.DebugLogf("[SLOT] release: erase slot %d: %v", slotID, err)
		}
	} else if log != nil {
		log.InfoLogf("[SLOT] release: erased slot %d (%s) for session %s", slotID, SlotFileName(modelName, slotID), sessionID)
	}
	mgr.Release(sessionID)
	if log != nil {
		log.InfoLogf("[SLOT] released slot %d for session %s", slotID, sessionID)
	}
}


type slotModelProvider interface {
	GetCurrent() (alias, modelName, host string)
	GetCurrentSlotSave() bool
}


type slotSaverImpl struct {
	mgr       *SlotManager
	client    *SlotClient
	holder    slotModelProvider
	sessionID string
	log       Logger
}


func NewSlotSaver(mgr *SlotManager, client *SlotClient, holder slotModelProvider, sessionID string, log Logger) agent.SlotSaver {
	if mgr == nil || client == nil || holder == nil || sessionID == "" {
		return nil
	}
	return &slotSaverImpl{mgr: mgr, client: client, holder: holder, sessionID: sessionID, log: log}
}

func (s *slotSaverImpl) SaveSlot(ctx context.Context) {
	SaveSessionSlot(s.mgr, s.client, s.holder, s.sessionID, s.log)
}
