package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// slotSaveTimeout — таймаут на сохранение/восстановление KV-cache слота.
// Запись большого кэша на диск может занимать заметное время.
const slotSaveTimeout = 2 * time.Minute

// SlotClient выполняет HTTP-запросы к llama-server для управления KV-cache слотов.
// Используется для моделей с флагом slot-save: true в models.json.
type SlotClient struct {
	httpClient *http.Client
}

func newSlotClient() *SlotClient {
	return &SlotClient{
		httpClient: &http.Client{Timeout: slotSaveTimeout},
	}
}

// saveSlot сохраняет KV-cache слота slotID на сервере serverURL в файл filename.
// modelName передаётся в запросе: роутер llama-server требует имя модели.
func (c *SlotClient) saveSlot(ctx context.Context, serverURL string, slotID int, modelName, filename string) error {
	return c.slotAction(ctx, serverURL, slotID, modelName, "save", filename)
}

// restoreSlot восстанавливает KV-cache слота slotID на сервере serverURL из файла filename.
func (c *SlotClient) restoreSlot(ctx context.Context, serverURL string, slotID int, modelName, filename string) error {
	return c.slotAction(ctx, serverURL, slotID, modelName, "restore", filename)
}

// eraseSlot очищает KV-cache слота в памяти сервера (POST /slots/{id}?action=erase).
// Используется при сбросе сессии/вытеснении, чтобы следующий владелец слота
// не унаследовал устаревший контекст. Файл сохранённого кэша на диске
// (--slot-save-path) эта операция не трогает — llama-server не предоставляет
// HTTP-действия для удаления файла; файл остаётся и перезаписывается при
// следующем save того же слота.
func (c *SlotClient) eraseSlot(ctx context.Context, serverURL string, slotID int, modelName string) error {
	url := fmt.Sprintf("%s/slots/%d?action=erase", serverURL, slotID)
	body, _ := json.Marshal(map[string]string{"model": modelName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create erase request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("erase slot %d: %w", slotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("erase slot %d: status %d: %s", slotID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// ClearAllSlots стирает KV-cache всех серверных слотов 0..totalSlots-1
// (action=erase). Используется при старте с флагом -r для чистого старта.
// Файлы кэша на диске не удаляются (llama-server не поддерживает это через HTTP)
// — они перезапишутся при следующих save. Метод best-effort.
func (c *SlotClient) ClearAllSlots(ctx context.Context, serverURL, modelName string, totalSlots int, log Logger) {
	for slotID := 0; slotID < totalSlots; slotID++ {
		if err := c.eraseSlot(ctx, serverURL, slotID, modelName); err != nil {
			if log != nil {
				log.DebugLogf("[SLOT] startup erase slot %d: %v", slotID, err)
			}
		} else if log != nil {
			log.InfoLogf("[SLOT] startup erase slot %d", slotID)
		}
	}
}

// IsSlotConfigError возвращает true, если ошибка save/restore указывает на
// отсутствие поддержки слотов на сервере (llama-server без --slot-save-path),
// а не на разовую проблему (например, отсутствие файла при первом restore).
// Такие ошибки → пометить хост недоступным и прекратить попытки.
func IsSlotConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// HTTP 5xx от эндпоинта слота — серверная/конфигурационная проблема.
	if strings.Contains(msg, "status 5") {
		return true
	}
	return strings.Contains(msg, "slot save path") ||
		strings.Contains(msg, "save path") ||
		strings.Contains(msg, "not configured")
}

// IsSlotMissingFileError возвращает true, если restore упал из-за отсутствия
// файла (первый запуск сессии) — это не ошибка, кэш просто ещё не сохранён.
// llama-server на restore несуществующего файла отвечает 400:
// "Unable to restore slot, no available space in KV cache or invalid slot save file".
func IsSlotMissingFileError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "file not found") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "not exist") ||
		strings.Contains(msg, "invalid slot save file")
}

// slotsProbeURL строит URL эндпоинта /slots с query-параметром model.
// Обычный llama-server игнорирует лишний query-параметр.
func slotsProbeURL(serverURL, modelName string) string {
	return fmt.Sprintf("%s/slots?model=%s", serverURL, url.QueryEscape(modelName))
}

// slotAction выполняет POST /slots/{id}?action={action} с телом
// {"filename": ..., "model": ...}. Эндпоинт доступен, если llama-server
// запущен с --slot-save-path. modelName обязателен для роутера llama-server,
// обычный сервер игнорирует лишнее поле в теле.
func (c *SlotClient) slotAction(ctx context.Context, serverURL string, slotID int, modelName, action, filename string) error {
	body, _ := json.Marshal(map[string]string{"filename": filename, "model": modelName})
	url := fmt.Sprintf("%s/slots/%d?action=%s", serverURL, slotID, action)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s slot %d: %w", action, slotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s slot %d: status %d: %s", action, slotID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// firstSlotID возвращает id первого слота на сервере (GET /slots).
// При --parallel 1 это единственный слот; на ошибке возвращается 0.
// modelName передаётся query-параметром: роутер llama-server требует его.
func (c *SlotClient) firstSlotID(ctx context.Context, serverURL, modelName string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, slotsProbeURL(serverURL, modelName), nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET /slots: status %d", resp.StatusCode)
	}

	var slots []struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&slots); err != nil {
		return 0, err
	}
	if len(slots) == 0 {
		return 0, fmt.Errorf("GET /slots: no slots reported")
	}
	return slots[0].ID, nil
}
