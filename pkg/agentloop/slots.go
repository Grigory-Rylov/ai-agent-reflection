package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// slotSaveTimeout — таймаут на сохранение/восстановление KV-cache слота.
// Запись большого кэша на диск может занимать заметное время.
const slotSaveTimeout = 2 * time.Minute

// slotClient выполняет HTTP-запросы к llama-server для управления KV-cache слотов.
// Используется для моделей с флагом slot-save: true в models.json.
type slotClient struct {
	httpClient *http.Client
}

func newSlotClient() *slotClient {
	return &slotClient{
		httpClient: &http.Client{Timeout: slotSaveTimeout},
	}
}

// saveSlot сохраняет KV-cache слота slotID на сервере serverURL в файл filename.
func (c *slotClient) saveSlot(ctx context.Context, serverURL string, slotID int, filename string) error {
	return c.slotAction(ctx, serverURL, slotID, "save", filename)
}

// restoreSlot восстанавливает KV-cache слота slotID на сервере serverURL из файла filename.
func (c *slotClient) restoreSlot(ctx context.Context, serverURL string, slotID int, filename string) error {
	return c.slotAction(ctx, serverURL, slotID, "restore", filename)
}

// slotAction выполняет POST /slots/{id}?action={action} с телом {"filename": ...}.
// Эндпоинт доступен, если llama-server запущен с --slot-save-path.
func (c *slotClient) slotAction(ctx context.Context, serverURL string, slotID int, action, filename string) error {
	body, _ := json.Marshal(map[string]string{"filename": filename})
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
func (c *slotClient) firstSlotID(ctx context.Context, serverURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/slots", nil)
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
