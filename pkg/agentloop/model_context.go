package agentloop

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)

// ModelContextResolver определяет лимит контекста модели и кэширует его
// по алиасу модели. Приоритет: поле context в models.json (override),
// иначе реальный контекст с llama-server. Сервер опрашивается один раз
// на модель; при смене модели (команда /r) для нового алиаса выполняется
// новый запрос. Фоллбеков нет — при неудаче возвращается ошибка.
type ModelContextResolver struct {
	mu     sync.Mutex
	holder *modelsconfig.Holder
	log    Logger
	cache  map[string]int
}

func NewModelContextResolver(holder *modelsconfig.Holder, log Logger) *ModelContextResolver {
	return &ModelContextResolver{
		holder: holder,
		log:    log,
		cache:  make(map[string]int),
	}
}

// Resolve возвращает лимит контекста текущей модели, кэшируя результат
// по алиасу. Возвращает ошибку, если лимит не удалось получить ни из
// models.json, ни с llama-server.
func (r *ModelContextResolver) Resolve() (int, error) {
	if r.holder == nil {
		return 0, fmt.Errorf("models.json not loaded")
	}
	alias, _, _ := r.holder.GetCurrent()

	r.mu.Lock()
	defer r.mu.Unlock()

	if ctx, ok := r.cache[alias]; ok {
		return ctx, nil
	}

	ctx, err := r.resolveOnce(alias)
	if err != nil {
		return 0, err
	}
	r.cache[alias] = ctx
	return ctx, nil
}

// resolveOnce запрашивает лимит контекста для конкретного алиаса модели.
func (r *ModelContextResolver) resolveOnce(alias string) (int, error) {
	if ctx := r.holder.GetModelContext(alias); ctx > 0 {
		if r.log != nil {
			r.log.InfoLogf("Model context from models.json for %s: %d tokens", alias, ctx)
		}
		return ctx, nil
	}

	entry, ok := r.holder.GetModelHost(alias)
	if !ok || entry.Host == "" {
		return 0, fmt.Errorf("model %q has no host configured", alias)
	}
	llamaURL := entry.Host
	if !strings.HasPrefix(llamaURL, "http://") && !strings.HasPrefix(llamaURL, "https://") {
		llamaURL = "http://" + llamaURL
	}
	modelName := entry.Name
	if modelName == "" {
		modelName = alias
	}

	tok := tokenizers.NewLlamaServerTokenizer(llamaURL, modelName, 0)
	if err := tok.InitializeContextLimit(); err != nil {
		return 0, fmt.Errorf("failed to get context from llama-server for %s (%s): %w", modelName, llamaURL, err)
	}
	ctx := tok.GetActualContextLimit()
	if ctx <= 0 {
		return 0, fmt.Errorf("llama-server returned invalid context limit for %s (%s)", modelName, llamaURL)
	}
	if r.log != nil {
		r.log.InfoLogf("Using actual server context for %s: %d tokens", alias, ctx)
	}
	return ctx, nil
}
