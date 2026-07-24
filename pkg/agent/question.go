package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Вопрос для опроса пользователя
type Question struct {
	Question  string   `json:"question"`
	Header    string   `json:"header,omitempty"`
	Custom    bool     `json:"custom,omitempty"`
	Options   []Option `json:"options,omitempty"`
}

// Вариант ответа
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Ответ от пользователя
type UserAnswer struct {
	Answer   string   `json:"answer"`
	Selected []string `json:"selected"`
}

// ProcessAnswer обрабатывает ответ пользователя
func ProcessAnswer(question Question, answer string) UserAnswer {
	var selected []string
	
	for _, opt := range question.Options {
		if strings.EqualFold(answer, opt.Label) {
			selected = append(selected, opt.Label)
		}
	}
	
	if len(selected) == 0 {
		selected = append(selected, answer)
	}
	
	return UserAnswer{
		Answer:   answer,
		Selected: selected,
	}
}

// FormatQuestionsForDisplay форматирует вопросы для отправки в чат
func FormatQuestionsForDisplay(questions []Question) string {
	if len(questions) == 0 {
		return ""
	}
	
	var result string
	for i, q := range questions {
		if i > 0 {
			result += "\n\n"
		}
		result += fmt.Sprintf("**%s**\n", q.Question)
		if q.Header != "" {
			result += fmt.Sprintf("*(%s)*\n", q.Header)
		}
		if len(q.Options) > 0 {
			result += "Варианты:\n"
			for _, opt := range q.Options {
				result += fmt.Sprintf("- **%s**", opt.Label)
				if opt.Description != "" {
					result += fmt.Sprintf(" (%s)", opt.Description)
				}
				result += "\n"
			}
		} else {
			result += "Напишите свой ответ\n"
		}
	}
	
	return result
}

// ParseAnswerFromJSON извлекает ответ из JSON payload
func ParseAnswerFromJSON(payload string) (string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return "", err
	}
	
	if ans, ok := data["answer"].(string); ok {
		return ans, nil
	}
	
	return "", fmt.Errorf("no answer in payload: %s", payload)
}
