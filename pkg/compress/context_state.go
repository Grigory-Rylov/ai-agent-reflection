package compress

import (
	"fmt"
	"strings"
	"time"
)

// ============================================================
// ContextState — структурированное состояние контекста
// ============================================================

// ContextState представляет структурированное состояние сессии.
// Вместо полной истории хранит только значимую информацию.
type ContextState struct {
	// Goal — текущая цель пользователя
	Goal string `json:"goal"`

	// CurrentStep — текущий шаг выполнения
	CurrentStep string `json:"current_step"`

	// Plan — план на ближайшие шаги (3-7)
	Plan []string `json:"plan"`

	// Done — завершённые шаги
	Done []string `json:"done"`

	// OpenQuestions — нерешённые вопросы
	OpenQuestions []string `json:"open_questions"`

	// WorkingMemory — важные факты (до 10)
	WorkingMemory []string `json:"working_memory"`

	// Decisions — принятые решения
	Decisions []string `json:"decisions"`

	// Artifacts — ссылки на файлы/ресурсы
	Artifacts []ArtifactRef `json:"artifacts"`

	// RecentContext — последние результаты (сжатые)
	RecentContext []string `json:"recent_context"`

	// NextSteps — следующие шаги
	NextSteps []string `json:"next_steps"`

	// LastUpdated — время последнего обновления
	LastUpdated time.Time `json:"last_updated"`
}

// ArtifactRef — ссылка на внешний артефакт
type ArtifactRef struct {
	Type        string `json:"type"`         // "file", "url", "tool_result"
	Path        string `json:"path"`         // путь или URL
	Description string `json:"description"`  // краткое описание
	Tokens      int    `json:"tokens"`       // примерный размер в токенах
	Summary     string `json:"summary"`      // выжимка содержания
}

// ToPrompt конвертирует состояние в промпт для модели.
func (s *ContextState) ToPrompt() string {
	var sb strings.Builder

	if s.Goal != "" {
		sb.WriteString(fmt.Sprintf("## Current Goal\n%s\n\n", s.Goal))
	}

	if len(s.Plan) > 0 {
		sb.WriteString("## Plan\n")
		for i, step := range s.Plan {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
		sb.WriteString("\n")
	}

	if len(s.Done) > 0 {
		sb.WriteString("## Completed\n")
		for _, item := range s.Done {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
		sb.WriteString("\n")
	}

	if len(s.Decisions) > 0 {
		sb.WriteString("## Decisions Made\n")
		for _, d := range s.Decisions {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
		sb.WriteString("\n")
	}

	if len(s.WorkingMemory) > 0 {
		sb.WriteString("## Important Facts\n")
		for _, fact := range s.WorkingMemory {
			sb.WriteString(fmt.Sprintf("- %s\n", fact))
		}
		sb.WriteString("\n")
	}

	if len(s.OpenQuestions) > 0 {
		sb.WriteString("## Open Questions\n")
		for _, q := range s.OpenQuestions {
			sb.WriteString(fmt.Sprintf("- %s\n", q))
		}
		sb.WriteString("\n")
	}

	if len(s.Artifacts) > 0 {
		sb.WriteString("## Available Artifacts\n")
		for _, a := range s.Artifacts {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", a.Path, a.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// EstimateTokens оценивает количество токенов в состоянии.
func (s *ContextState) EstimateTokens() int {
	return EstimateTokensSimple(s.ToPrompt())
}
