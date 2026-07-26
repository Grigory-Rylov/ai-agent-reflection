package store

import "fmt"

type TodoStore struct {
	store Store
}

func NewTodoStore(s Store) *TodoStore {
	return &TodoStore{store: s}
}

func (ts *TodoStore) UpdateTodos(sessionID string, items []TodoItem) error {
	return ts.store.UpdateTodos(sessionID, items)
}

func (ts *TodoStore) GetTodos(sessionID string) ([]TodoItem, error) {
	return ts.store.GetTodos(sessionID)
}

func ToStoreTodoItems(items []TodoItem) []map[string]interface{} {
	result := make([]map[string]interface{}, len(items))
	for i, t := range items {
		result[i] = map[string]interface{}{
			"id":       t.ID,
			"content":  t.Content,
			"status":   t.Status,
			"priority": t.Priority,
		}
	}
	return result
}

func FromStoreTodoItems(items []map[string]interface{}) ([]TodoItem, error) {
	result := make([]TodoItem, len(items))
	for i, item := range items {
		id, _ := item["id"].(string)
		content, _ := item["content"].(string)
		status, _ := item["status"].(string)
		priority, _ := item["priority"].(string)
		if id == "" || content == "" {
			return nil, fmt.Errorf("todo item %d: missing id or content", i)
		}
		if status == "" {
			status = "pending"
		}
		result[i] = TodoItem{
			ID:       id,
			Content:  content,
			Status:   status,
			Priority: priority,
			Position: i,
		}
	}
	return result, nil
}
