package vk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// VKMessage — сообщение из VK Long Poll
type VKMessage struct {
	ID     int64  `json:"id"`
	PeerID int64  `json:"peer_id"`
	FromID int64  `json:"from_id"`
	Date   int64  `json:"date"`
	Text   string `json:"text"`
}

// APIErrorResponse — ошибка VK API
type APIErrorResponse struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_msg"`
}

// ============================================================
// VKBotClient — клиент для VK Bot API
// ============================================================

// BotClient работает с VK Bot API
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
// VK API messages.send ожидает form-data в теле запроса
func (c *BotClient) doRequestPOST(endpoint string, params map[string]interface{}) ([]byte, error) {
	endpointURL := fmt.Sprintf("%s%s", c.baseURL, endpoint)

	// Формируем form-data тело (как в питоне)
	body := &bytes.Buffer{}
	for k, v := range params {
		if body.Len() > 0 {
			body.WriteString("&")
		}
		val := formatValue(v)
		body.WriteString(url.QueryEscape(k) + "=" + url.QueryEscape(val))
	}

	// Отправляем POST запрос с form-data телом
	req, err := http.NewRequest("POST", endpointURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
		return nil, fmt.Errorf("HTTP error: %d, body: %s", resp.StatusCode, string(responseBody[:min(500, len(responseBody))]))
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
	reqURL := fmt.Sprintf("%s%s", c.baseURL, endpoint)

	// Добавляем access_token и v как URL query параметры
	query := "access_token=" + c.token + "&v=" + c.apiVersion

	for k, v := range params {
		if k == "access_token" || k == "v" {
			continue
		}
		val := formatValue(v)
		query += "&" + url.QueryEscape(k) + "=" + url.QueryEscape(val)
	}

	reqURL += "?" + query

	resp, err := c.httpClient.Get(reqURL)
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

// formatValue конвертирует значение в строку
func formatValue(v interface{}) string {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%v", v)
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

// CheckUpdates проверяет обновления через VK Long Poll API (версия 2.0)
func (c *BotClient) CheckUpdates(server, key string, ts int64) ([]VKMessage, int64, error) {
	// Формируем URL для long poll (без токена!)
	lpURL := fmt.Sprintf("https://%s?act=a_check&key=%s&ts=%d&wait=25&mode=74&version=3",
		server, key, ts)

	resp, err := c.httpClient.Get(lpURL)
	if err != nil {
		return nil, ts, fmt.Errorf("long poll request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ts, fmt.Errorf("failed to read response: %w", err)
	}

	// Парсим ответ
	var result struct {
		Failed  int                  `json:"failed"`
		Ts      int64                `json:"ts"`
		Updates [][]interface{}      `json:"updates"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, ts, fmt.Errorf("failed to parse response: %w (body: %s)", err, string(responseBody[:min(200, len(responseBody))]))
	}

	// Проверяем на ошибку failed
	if result.Failed != 0 {
		return nil, ts, fmt.Errorf("long poll failed: code=%d", result.Failed)
	}

	// VK Long Poll v2.0 формат: [type, msg_id, flags, peer_id, timestamp, text, ...]
	var messages []VKMessage

	for _, update := range result.Updates {
		if len(update) >= 6 {
			msgType, ok := update[0].(float64)
			if !ok {
				continue
			}

			if int(msgType) == 4 {
				// Фильтруем исходящие сообщения (флаг 2)
				flags, _ := update[2].(float64)
				if int(flags)&2 != 0 {
					continue
				}

				msgID := extractFloat64(update, 1)
				peerID := extractFloat64(update, 3)
				timestamp := extractFloat64(update, 4)

				msg := VKMessage{
					ID:     int64(msgID),
					PeerID: int64(peerID),
					Date:   int64(timestamp),
				}

				// Текст на индексе 5
				if text, ok := update[5].(string); ok {
					msg.Text = text
				}

				messages = append(messages, msg)
			}
		}
	}

	return messages, result.Ts, nil
}

// extractFloat64 безопасно извлекает float64 из массива
func extractFloat64(arr []interface{}, index int) float64 {
	if index < len(arr) {
		if v, ok := arr[index].(float64); ok {
			return v
		}
	}
	return 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractMessageID извлекает ID сообщения из ответа VK
func extractMessageID(response interface{}) (int64, error) {
	// Формат 1: { "response": 12345 } — просто число
	if msgID, ok := response.(float64); ok {
		return int64(msgID), nil
	}

	// Формат 2: { "response": [{ "message_id": 12345 }] } — массив с объектом
	if arr, ok := response.([]interface{}); ok {
		if len(arr) > 0 {
			if msgMap, ok := arr[0].(map[string]interface{}); ok {
				if msgID, ok := msgMap["message_id"].(float64); ok {
					return int64(msgID), nil
				}
			}
		}
	}

	// Формат 3: { "response": { "message_id": 12345 } } — объект
	if msgMap, ok := response.(map[string]interface{}); ok {
		if msgID, ok := msgMap["message_id"].(float64); ok {
			return int64(msgID), nil
		}
	}

	return 0, fmt.Errorf("unexpected response format: %v", response)
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
		"v":         c.apiVersion,
		"access_token": c.token,
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

	// VK messages.send возвращает: { "response": 12345 }
	// Иногда: { "response": [{ "message_id": 12345 }] }
	var fullResponse struct {
		Response interface{} `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &fullResponse); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	// Извлекаем message_id из ответа
	return extractMessageID(fullResponse.Response)
}

// EditMessage редактирует существующее сообщение
func (c *BotClient) EditMessage(peerID, messageID int64, text string, keyboard map[string]interface{}) error {
	params := map[string]interface{}{
		"peer_id":      peerID,
		"message_id":   messageID,
		"message":      text,
		"v":            c.apiVersion,
		"access_token": c.token,
	}
	if keyboard != nil {
		kbJSON, _ := json.Marshal(keyboard)
		params["keyboard"] = string(kbJSON)
	}

	_, err := c.doRequestPOST("messages.edit", params)
	return err
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
	safeLength := maxLength - 20

	parts := []string{}
	currentPart := ""
	lines := splitLines(text)

	for _, line := range lines {
		if len(line) > safeLength {
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

// ============================================================
// Thinking Messages
// ============================================================

// SendThinking отправляет сообщение о "размышлении"
func (c *BotClient) SendThinking(peerID int64, content string) (int64, error) {
	if content == "" {
		return 0, fmt.Errorf("empty thinking content")
	}

	thinkingText := fmt.Sprintf("[THINKING] %s", content)
	return c.SendMessage(peerID, thinkingText)
}

// ============================================================
// Клавиатуры
// ============================================================

// CreateQuestionKeyboard создаёт клавиатуру для вопросов
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
