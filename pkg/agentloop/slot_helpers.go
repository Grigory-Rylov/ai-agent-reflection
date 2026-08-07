package agentloop

import (
	"context"

	"github.com/opencode/llama-client/pkg/agent"
)

// AssignSessionSlot выделяет слот для sessionID и подготавливает его к запросу:
//   - проверяет доступность фичи (GET /slots), кэширует результат по хосту;
//   - при LRU-вытеснении сохраняет KV-cache вытесненной сессии в её файл
//     {model}_slot{N}.bin и очищает серверный слот, чтобы новая сессия не
//     унаследовала чужой контекст (файл ключуется по слоту, поэтому новый
//     владелец стартует cold);
//   - иначе восстанавливает собственный кэш сессии (no-op для свежего слота).
//
// Возвращает назначенный slotID (-1, если фича недоступна). Вызывающий
// проставляет cfg.SlotID = slotID и cfg.SlotSave = true.
//
// Контракт: один и тот же sessionID используется для слота, сессии (через
// cfg.SessionConfig.SessionID) и строки в БД — это гарантирует, что cleanup
// по sessionID освободит именно тот слот, что был выделен.
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
		// Сохраняем кэш вытесненной сессии в файл её (теперь уже бывшего) слота.
		filename := SlotFileName(modelName, slotID)
		if err := client.saveSlot(context.Background(), host, slotID, modelName, filename); err != nil {
			if log != nil {
				log.WarnLogf("[SLOT] save evicted session %s cache: %v", evicted, err)
			}
		} else if log != nil {
			log.InfoLogf("[SLOT] saved evicted session %s → slot %d (%s)", evicted, slotID, filename)
		}
		// Очищаем серверный слот (erase) — новая сессия стартует cold.
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

	// Свой слот (повторное использование) или свежий — восстанавливаем кэш.
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

// SaveSessionSlot сохраняет KV-cache слота сессии в файл {model}_slot{N}.bin
// (action=save). Вызывается после завершения LLM-запроса/ответа, пока слот
// ещё держит актуальный кэш в памяти. Ошибки конфигурации помечают хост
// недоступным (один info-лог), прочие — warn. Никогда не валят обработку.
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

// ReleaseSessionSlot стирает KV-cache слота сессии в памяти сервера
// (action=erase) и возвращает слот в свободный пул SlotManager. Файл кэша на
// диске не удаляется (llama-server не предоставляет такого HTTP-действия) —
// он перезапишется при следующем save этого слота. No-op, если у сессии нет
// слота. Используется при завершении/сбросе сессии агента.
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

// slotModelProvider — минимальный интерфейс доступа к текущей модели,
// реализуемый *modelsconfig.Holder (и тестовыми заглушками).
type slotModelProvider interface {
	GetCurrent() (alias, modelName, host string)
	GetCurrentSlotSave() bool
}

// slotSaverImpl реализует agent.SlotSaver: сохраняет KV-cache слота сессии
// после каждого ответа LLM. Вызывается из agent_impl.streamAndCollect.
type slotSaverImpl struct {
	mgr       *SlotManager
	client    *SlotClient
	holder    slotModelProvider
	sessionID string
	log       Logger
}

// NewSlotSaver строит agent.SlotSaver для сессии. Возвращает nil, если
// слот-фича не настроена — тогда агент не будет дёргать save. sessionID —
// тот же ID, что привязан к слоту в SlotManager (и к session.SessionID).
func NewSlotSaver(mgr *SlotManager, client *SlotClient, holder slotModelProvider, sessionID string, log Logger) agent.SlotSaver {
	if mgr == nil || client == nil || holder == nil || sessionID == "" {
		return nil
	}
	return &slotSaverImpl{mgr: mgr, client: client, holder: holder, sessionID: sessionID, log: log}
}

func (s *slotSaverImpl) SaveSlot(ctx context.Context) {
	SaveSessionSlot(s.mgr, s.client, s.holder, s.sessionID, s.log)
}
