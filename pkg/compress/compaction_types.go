package compress

// ============================================================
// CompactionLevel — уровни сжатия
// ============================================================

// CompactionLevel определяет степень сжатия.
type CompactionLevel int

const (
	CompactionNone      CompactionLevel = iota // < 50%
	CompactionWarn                             // 50-70%
	CompactionNormal                           // 70-85%
	CompactionAggressive                       // > 85%
)

// CompactionThresholds — пороги для разных уровней.
type CompactionThresholds struct {
	WarnPercent       float64 // 0.50
	NormalPercent     float64 // 0.70
	AggressivePercent float64 // 0.85
}

// DefaultThresholds возвращает пороги по умолчанию.
func DefaultThresholds() CompactionThresholds {
	return CompactionThresholds{
		WarnPercent:       0.50,
		NormalPercent:     0.70,
		AggressivePercent: 0.85,
	}
}

// GetLevel определяет уровень сжатия по проценту заполнения.
func (t CompactionThresholds) GetLevel(usagePercent float64) CompactionLevel {
	if usagePercent >= t.AggressivePercent {
		return CompactionAggressive
	}
	if usagePercent >= t.NormalPercent {
		return CompactionNormal
	}
	if usagePercent >= t.WarnPercent {
		return CompactionWarn
	}
	return CompactionNone
}

// ============================================================
// CompactionConfig — конфигурация сжатия
// ============================================================

// CompactionConfig настраивает поведение сжатия.
type CompactionConfig struct {
	// Thresholds — пороги сжатия
	Thresholds CompactionThresholds

	// KeepLastMessages — сколько последних сообщений сохранять целиком
	KeepLastMessages int

	// MaxWorkingMemory — максимум элементов в рабочей памяти
	MaxWorkingMemory int

	// MaxArtifacts — максимум ссылок на артефакты
	MaxArtifacts int

	// ToolResultMaxTokens — макс. токенов для результата инструмента в чате
	ToolResultMaxTokens int

	// ExternalStorePath — путь для хранения больших результатов
	ExternalStorePath string
}

// DefaultCompactionConfig возвращает конфиг по умолчанию.
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		Thresholds:          DefaultThresholds(),
		KeepLastMessages:    8,
		MaxWorkingMemory:    10,
		MaxArtifacts:        20,
		ToolResultMaxTokens: 500,
		ExternalStorePath:   "./artifacts",
	}
}

// ============================================================
// CompactionResult — результат сжатия
// ============================================================

// CompactionResult содержит результат операции сжатия.
type CompactionResult struct {
	// State — новое структурированное состояние
	State *ContextState

	// KeptMessages — сообщения, оставленные без изменений
	KeptMessages []Message

	// SummarizedCount — количество сообщений, которые были сжаты
	SummarizedCount int

	// TokensBefore — токенов до сжатия
	TokensBefore int

	// TokensAfter — токенов после сжатия
	TokensAfter int

	// Level — применённый уровень сжатия
	Level CompactionLevel

	// ArtifactsSaved — артефакты, вынесенные во внешнее хранилище
	ArtifactsSaved []ArtifactRef
}

// CompressionRatio возвращает коэффициент сжатия.
func (r *CompactionResult) CompressionRatio() float64 {
	if r.TokensBefore == 0 {
		return 1.0
	}
	return float64(r.TokensAfter) / float64(r.TokensBefore)
}
