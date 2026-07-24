# Agent Feature Plan — opencode-style features

## Completed

1. ✅ Восстановить проект из git
2. ✅ Добавить метод SendFile в BotClient
3. ✅ Проверить сборку проекта
4. ✅ Создать Agent System (роли агентов)
5. ✅ Создать Question system (опросы)
6. ✅ Создать Plan mode
7. ✅ Создать Background tasks
8. ✅ Создать Todo manager

## Осталось

- [ ] Интегрировать новые компоненты в основной цикл агента
- [ ] Протестировать в VK

## Что создано

- `pkg/agent/agent_system.go` - роли агентов
- `pkg/agent/question.go` - система опросов
- `pkg/agent/plan.go` - плановый режим
- `pkg/agent/task_bg.go` - фоновые задачи
- `pkg/agent/todo.go` - менеджер задач
