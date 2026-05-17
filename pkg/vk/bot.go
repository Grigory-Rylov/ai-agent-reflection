package vk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ============================================================
// Модели данных VK Bot API (согласно Python-референсу)
// ============================================================

// LongPollServerResponse — ответ от messages.getLongPollServer
type LongPollServerResponse struct {
	Server string `json:"server"`
	Key    string `json:"key"`
	Ts     int64  `json:"ts"`
}

// LongPollUpdate — обновление из long polling
type LongPollUpdate struct {
	Version   int                    `json:"version"`
	Updates   [][][]interface{}      `json:"updates"`
	Timestamp int64                  `json:"timestamp,omitempty"`
}

// VKMessage — сообщение из VK
type VKMessage struct {
	ID            int64                `json:"id"`
	PeerID        int64                `json:"peer_id"`
	FromID        int64                `json:"from_id"`
	Date          int64                `json:"date"`
	Text          string               `json:"body"`
	Attachments   []map[string]any     `json:"attachments"`
	IsImportant   bool                 `json:"is_important"`
	IsChatMessage bool                 `json:"is_chat_message"`
}

// APIErrorResponse — ошибка VK API
type APIErrorResponse struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_msg"`
}

// ============================================================
// VKBotClient — клиент для VK Bot API (long polling)
// ============================================================

// BotClient работает с VK Bot API через long polling
type BotClient struct {
	token      string
	apiVersion string
	baseURL    string
	httpClient *http.Client
}

// ============================================================
// Инициализация
// ============================================================

// NewBotClient создаёт новый клиент VK Bot API
func NewBotClient(token string) *BotClient {
	return &BotClient{
		token:      token,
		apiVersion: "5.200",
		baseURL:    "https://api.vk.com/method/",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ============================================================
// Внутренние методы
// ============================================================

// doRequestPOST выполняет POST запрос к VK API (для отправки сообщений)
func (c *BotClient) doRequestPOST(endpoint string, params map[string]interface{}) ([]byte, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, endpoint)

	// Добавляем общие параметры
	params["access_token"] = c.token
	params["v"] = c.apiVersion

	// Формируем тело запроса
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Отправляем POST запрос
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Проверяем на наличие API ошибки
	var apiError struct {
		Error APIErrorResponse `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &apiError); err == nil && apiError.Error.ErrorCode > 0 {
		return nil, fmt.Errorf("VK API error %d: %s", apiError.Error.ErrorCode, apiError.Error.ErrorMessage)
	}

	return responseBody, nil
}

// doRequestGET выполняет GET запрос к VK API (для получения данных)
func (c *BotClient) doRequestGET(endpoint string, params map[string]interface{}) ([]byte, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, endpoint)

	// Добавляем общие параметры
	params["access_token"] = c.token
	params["v"] = c.apiVersion

	// Формируем URL с query params
	query := ""
	for k, v := range params {
		if query != "" {
			query += "&"
		}
		query += fmt.Sprintf("%s=%v", k, v)
	}

	if query != "" {
		url += "?" + query
	}

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Проверяем на наличие API ошибки
	var apiError struct {
		Error APIErrorResponse `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &apiError); err == nil && apiError.Error.ErrorCode > 0 {
		return nil, fmt.Errorf("VK API error %d: %s", apiError.Error.ErrorCode, apiError.Error.ErrorMessage)
	}

	return responseBody, nil
}

// ============================================================
// Long Polling
// ============================================================

// GetLongPollServer получает параметры long polling сервера
func (c *BotClient) GetLongPollServer() (string, string, int64, error) {
	params := map[string]interface{}{}

	responseBody, err := c.doRequestGET("messages.getLongPollServer", params)
	if err != nil {
		return "", "", 0, err
	}

	var response struct {
		Response LongPollServerResponse `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	return response.Response.Server, response.Response.Key, response.Response.Ts, nil
}

// CheckUpdates проверяет обновления через long polling
func (c *BotClient) CheckUpdates(server, key string, ts int64) ([]VKMessage, int64, error) {
	params := map[string]interface{}{
		"key":     key,
		"ts":      ts,
		"wait":    25,
		"mode":    2,
		"version": 3,
	}

	url := fmt.Sprintf("%s?%s", server, encodeParams(params))

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, 0, fmt.Errorf("long poll request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}

	var update LongPollUpdate
	if err := json.Unmarshal(responseBody, &update); err != nil {
		return nil, 0, fmt.Errorf("failed to parse update: %w", err)
	}

	// Парсим обновления — формат: [type, subType, timestamp, data]
	var messages []VKMessage
	for _, updateItem := range update.Updates {
		if len(updateItem) >= 4 {
			// Проверяем тип обновления (5 = новое сообщение в чате)
			if len(updateItem[0]) > 0 {
				if typeVal, ok := updateItem[0][0].(float64); ok && int(typeVal) == 5 {
					// Парсим данные сообщения
					if data, ok := updateItem[3][0].([]interface{}); ok {
						for _, msgRaw := range data {
							if msgBytes, err := json.Marshal(msgRaw); err == nil {
								var msg VKMessage
								if err := json.Unmarshal(msgBytes, &msg); err == nil {
									messages = append(messages, msg)
								}
							}
						}
					}
				}
			}
		}
	}

	return messages, update.Timestamp, nil
}

// ============================================================
// Отправка сообщений
// ============================================================

// SendMessage отправляет текстовое сообщение (с авто-дроблением длинных)
func (c *BotClient) SendMessage(peerID int64, text string) (int64, error) {
	if text == "" {
		return 0, fmt.Errorf("empty message text")
	}

	// Если сообщение длинное — дробим на части
	if len(text) > 2000 {
		parts := c.splitText(text, 2000)
		lastMsgID := int64(0)

		for i, part := range parts {
			partText := fmt.Sprintf("[%d/%d]\n%s", i+1, len(parts), part)
			msgID, err := c.sendSingleMessage(peerID, partText, "", nil)
			if err != nil {
				continue
			}
			lastMsgID = msgID

			// Пауза между сообщениями
			if i < len(parts)-1 {
				time.Sleep(300 * time.Millisecond)
			}
		}
		return lastMsgID, nil
	}

	return c.sendSingleMessage(peerID, text, "", nil)
}

// SendMessageWithKeyboard отправляет сообщение с клавиатурой
func (c *BotClient) SendMessageWithKeyboard(peerID int64, text string, keyboard map[string]interface{}) (int64, error) {
	return c.sendSingleMessage(peerID, text, "", keyboard)
}

// sendSingleMessage отправляет одно сообщение
func (c *BotClient) sendSingleMessage(peerID int64, text, attachment string, keyboard map[string]interface{}) (int64, error) {
	params := map[string]interface{}{
		"peer_id":   peerID,
		"random_id": time.Now().UnixMilli(),
	}
	if text != "" {
		params["message"] = text
	}
	if attachment != "" {
		params["attachment"] = attachment
	}
	if keyboard != nil {
		kbJSON, _ := json.Marshal(keyboard)
		params["keyboard"] = string(kbJSON)
	}

	responseBody, err := c.doRequestPOST("messages.send", params)
	if err != nil {
		return 0, err
	}

	// Парсим ответ — может быть массивом или объектом
	var result interface{}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	// Проверяем формат ответа
	switch v := result.(type) {
	case []interface{}:
		// Массив — берём первый элемент
		if len(v) > 0 {
			if msgMap, ok := v[0].(map[string]interface{}); ok {
				if msgID, ok := msgMap["message_id"].(float64); ok {
					return int64(msgID), nil
				}
			}
		}
	case map[string]interface{}:
		// Объект — возвращаем message_id напрямую
		if msgID, ok := v["message_id"].(float64); ok {
			return int64(msgID), nil
		}
	}

	return 0, fmt.Errorf("unexpected response format")
}

// ============================================================
// Получение сообщений
// ============================================================

// GetMessagesByID получает сообщения по ID
func (c *BotClient) GetMessagesByID(messageIDs []int64) ([]VKMessage, error) {
	idsStr := ""
	for i, id := range messageIDs {
		if i > 0 {
			idsStr += ","
		}
		idsStr += fmt.Sprintf("%d", id)
	}

	params := map[string]interface{}{
		"message_ids": idsStr,
	}

	responseBody, err := c.doRequestGET("messages.getById", params)
	if err != nil {
		return nil, err
	}

	var response struct {
		Response struct {
			Items []VKMessage `json:"items"`
		} `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return response.Response.Items, nil
}

// ============================================================
// Утилиты для работы с текстом
// ============================================================

// splitText разбивает текст на части по строкам
func (c *BotClient) splitText(text string, maxLength int) []string {
	safeLength := maxLength - 20 // Резервируем место для нумерации [N/M]\n

	parts := []string{}
	currentPart := ""
	lines := splitLines(text)

	for _, line := range lines {
		if len(line) > safeLength {
			// Одна строка слишком длинная — разбиваем
			if currentPart != "" {
				parts = append(parts, currentPart)
				currentPart = ""
			}
			for i := 0; i < len(line); i += safeLength {
				end := i + safeLength
				if end > len(line) {
					end = len(line)
				}
				parts = append(parts, line[i:end])
			}
		} else if currentPart != "" && len(currentPart)+len(line)+1 > safeLength {
			// Текущая часть заполнена
			parts = append(parts, currentPart)
			currentPart = line
		} else {
			if currentPart != "" {
				currentPart += "\n" + line
			} else {
				currentPart = line
			}
		}
	}

	if currentPart != "" {
		parts = append(parts, currentPart)
	}

	return parts
}

// splitLines разбивает текст на строки
func splitLines(text string) []string {
	lines := []string{}
	currentLine := ""
	for _, char := range text {
		if char == '\n' {
			lines = append(lines, currentLine)
			currentLine = ""
		} else {
			currentLine += string(char)
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	return lines
}

// encodeParams кодирует параметры для GET запроса
func encodeParams(params map[string]interface{}) string {
	query := ""
	for k, v := range params {
		if query != "" {
			query += "&"
		}
		query += fmt.Sprintf("%s=%v", k, v)
	}
	return query
}

// ============================================================
// Thinking Messages — отправка промежуточных сообщений
// ============================================================

// SendThinking отправляет сообщение о "размышлении" (промежуточный статус)
func (c *BotClient) SendThinking(peerID int64, content string) (int64, error) {
	if content == "" {
		return 0, fmt.Errorf("empty thinking content")
	}

	// Форматируем как thinking message
	thinkingText := fmt.Sprintf("[THINKING] %s", content)

	return c.SendMessage(peerID, thinkingText)
}

// ============================================================
// Клавиатуры
// ============================================================

// CreateQuestionKeyboard создаёт клавиатуру для вопросов (каждый вариант — кнопка)
func CreateQuestionKeyboard(header string, questionText string, options []map[string]string) map[string]interface{} {
	buttons := [][]map[string]interface{}{}
	for _, opt := range options {
		row := []map[string]interface{}{
			{
				"action": map[string]interface{}{
					"type":  "text",
					"label": opt["label"],
				},
				"color": "primary",
			},
		}
		buttons = append(buttons, row)
	}

	keyboard := map[string]interface{}{
		"inline": false,
		"buttons": buttons,
	}

	return keyboard
}

// CreateKeyboard создает клавиатуру с указанными кнопками
func CreateKeyboard(buttons [][]map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"inline": false,
		"buttons": buttons,
	}
}
