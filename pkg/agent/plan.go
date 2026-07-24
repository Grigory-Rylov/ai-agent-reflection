package agent

import (
	"fmt"
)

// Плановый режим - позволяет агенту создавать планы перед реализацией

// PlanModeState состояние планового режима
type PlanModeState struct {
	IsActive  bool   `json:"is_active"`
	PlanPath  string `json:"plan_path,omitempty"`
	Steps     []PlanStep `json:"steps"`
	Completed int    `json:"completed"`
}

// Шаг плана
type PlanStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"` // pending, in_progress, completed, cancelled
	Dependencies []string `json:"dependencies,omitempty"`
}

// Плановый агент
type PlanAgent struct {
	currentPlan *PlanModeState
}

// Новый плановый агент
func NewPlanAgent() *PlanAgent {
	return &PlanAgent{}
}

// Войти в плановый режим
func (p *PlanAgent) EnterPlanMode(planPath string) {
	p.currentPlan = &PlanModeState{
		IsActive: true,
		PlanPath: planPath,
		Steps:    make([]PlanStep, 0),
	}
}

// Выйти из планового режима и перейти к реализации
func (p *PlanAgent) ExitPlanMode() bool {
	if p.currentPlan != nil && p.currentPlan.IsActive {
		p.currentPlan.IsActive = false
		return true
	}
	return false
}

// Добавить шаг в план
func (p *PlanAgent) AddStep(step PlanStep) {
	if p.currentPlan != nil {
		p.currentPlan.Steps = append(p.currentPlan.Steps, step)
	}
}

// Получить текущий план
func (p *PlanAgent) GetPlan() *PlanModeState {
	return p.currentPlan
}

// Проверить находится ли агент в плановом режиме
func (p *PlanAgent) IsInPlanMode() bool {
	return p.currentPlan != nil && p.currentPlan.IsActive
}

// Отформатировать план для отправки пользователю
func (p *PlanAgent) FormatPlan() string {
	if p.currentPlan == nil || len(p.currentPlan.Steps) == 0 {
		return "План пока пустой."
	}
	
	result := "## План реализации\n\n"
	for i, step := range p.currentPlan.Steps {
		status := "○"
		if step.Status == "completed" {
			status = "●"
		} else if step.Status == "in_progress" {
			status = "◐"
		}
		
		result += fmt.Sprintf("%d. **%s** %s\n", i+1, step.Title, status)
		if step.Description != "" {
			result += fmt.Sprintf("   %s\n", step.Description)
		}
	}
	
	completed := 0
	for _, step := range p.currentPlan.Steps {
		if step.Status == "completed" {
			completed++
		}
	}
	
	result += fmt.Sprintf("\nПрогресс: %d/%d шагов", completed, len(p.currentPlan.Steps))
	
	return result
}

// Создать инструмент для выхода из планового режима (plan_exit)
func (p *PlanAgent) PlanExit() (string, error) {
	if !p.IsInPlanMode() {
		return "Агент не в плановом режиме", fmt.Errorf("not in plan mode")
	}
	
	// Проверяем что план готов
	if len(p.currentPlan.Steps) == 0 {
		return "План пустой, нужно добавить хотя бы один шаг", fmt.Errorf("empty plan")
	}
	
	return "Готов перейти к реализации плана. Начнем с первого шага.", nil
}
