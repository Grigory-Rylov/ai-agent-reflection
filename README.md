# AI Agent Gateway (VK Bot)

Go-based AI agent gateway connecting VK Bot API with local LLM models (llama-server). The agent supports function calling with 11 tools: file operations, shell execution, web search/fetch, code search, math evaluation, and more.

## Architecture

```
VK Bot API → Agent Gateway → llama-server (local LLM)
                │
         ┌──────┼──────┐
         │      │      │
     Session  Tools   VK Client
```

## Binaries

| Binary | Description |
|--------|-------------|
| `agent` | Main AI agent with tools and LLM integration |
| `agent-restarter` | Watchdog for remote updates via VK commands |

## Quick Start

```bash
# Build both binaries
go build -o agent .
go build -o agent-restarter ./cmd/vk-gateway-restarter

# Configure config.json with VK token (models and their servers live in models.json)
# Run agent directly:
./agent

# Or run via restarter (recommended for remote updates):
./agent-restarter
```

Config is loaded from `./config.json` (priority), with fallback to `~/.config/ai-agent/config.json`.

```json
{
    "token_vk": "vk1.a.your_vk_bot_token",
    "peer_id": 2000000001,
    "thinking_peer_id": 2000000002,
    "max_tokens": 4096,
    "temperature": 0.7,
    "db_path": "./agent.db",
    "allowed_dirs": ["/path/to/working/dir"]
}
```

Model settings (which model, which llama-server address) are **not** part of `config.json` — they live in `models.json` (see next section).

## Models config

Models are configured in `models.json` (in the agent directory):

```json
{
    "default": "gemma-4",
    "models": {
        "gemma-4": {
            "name": "gemma-4-12b-it-UD-Q4_K_XL.gguf",
            "host": "127.0.0.1:8081",
            "context": 32768,
            "vision": true,
            "slot-save": true
        }
    }
}
```

Fields per model:

| Field | Description |
|-------|-------------|
| `name` | Model file name on the server |
| `host` | llama-server address (with or without `http://`) |
| `context` | Context limit in tokens (optional) |
| `vision` | Model supports images (optional) |
| `slot-save` | Persist/restore the llama-server slot KV-cache per session (optional). See [Slot cache](#slot-cache-slot-save) below. |

## Slot cache (`slot-save`)

Long multi-turn conversations re-process the whole history on every request. With `"slot-save": true` on the model the agent persists the server-side KV-cache of a slot between turns:

- before each prompt the agent tries to **restore** the slot file for this session;
- after the response it **saves** the slot again;
- slot file is named after session and model — `agent_<peer_id>_<model>.bin` — and stored server-side in the directory from `--slot-save-path`;
- errors are only logged, never shown in chat; a missing/expired slot file is a logged HTTP 400 and the request proceeds with an empty cache.

### llama-server flags

Slot save/restore uses the llama-server `/slots` API, so the server must be started with:

- `--slot-save-path <dir>` — **required**; directory where slot files are stored (e.g. `/tmp/llama-cache`);
- `--swa-full` — **required for SWA/hybrid models** (e.g. gemma-4). Without it the restored cache is *not* reused: for such models llama-server relies on in-memory context checkpoints that the `/slots` save/restore API does not persist, so after a restore the whole history is re-processed (`cache_n: 0`).

Recommended also: run the server with `--parallel 1` (or a single user/agent per server), otherwise a request may be served by a different slot than the one the agent saves, making the save no-op.

## Multi-Agent Mode

The agent system supports multiple AI agent roles with individual prompts and tool permissions.

### Configuration

Register agents in the `"agents"` block of `config.json`:

```json
{
    "agents": {
        "lead": {
            "description": "Lead agent, coordinates the pipeline",
            "mode": "primary",
            "prompt": "agents/lead.md"
        },
        "worker": {
            "description": "Developer, writes and implements code",
            "mode": "subagent",
            "prompt": "agents/worker.md",
            "subagentTypes": ["reviewer", "explore"]
        },
        "reviewer": {
            "description": "Code reviewer",
            "mode": "subagent",
            "leaf": true,
            "review": true,
            "prompt": "agents/reviewer.md",
            "permission": {
                "file_write": "deny",
                "edit": "deny",
                "apply_patch": "deny"
            }
        },
        "qa": {
            "description": "QA engineer, writes and runs tests",
            "mode": "subagent",
            "leaf": true,
            "prompt": "agents/qa.md"
        }
    }
}
```

### Agent config fields

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Human-readable description (shown in available agents list) |
| `mode` | string | `"primary"` (user-facing) or `"subagent"` (invoked via `#name`) |
| `prompt` | string | Path to `.md` file with the agent's system prompt |
| `leaf` | bool | If `true`, agent cannot delegate to sub-agents |
| `review` | bool | If `true`, agent gets read-only tools + approve/reject |
| `hidden` | bool | If `true`, hidden from available agents list |
| `subagentTypes` | []string | List of agent types this agent can delegate to (e.g. `["reviewer", "explore"]`) |
| `permission` | object | Tool permissions: `"allow"`, `"deny"`, or `"ask"` per tool |

### Permission rules

Each permission entry maps a tool name to an action:

| Tool name | Description |
|-----------|-------------|
| `file_read` | Read files |
| `file_write` | Create/overwrite files |
| `edit` | Search-and-replace edit |
| `apply_patch` | Apply patches |
| `shell_execute` | Run shell commands |
| `glob` | Find files by pattern |
| `search_code` | Grep file contents |
| `web_fetch` | Fetch URLs |
| `web_search` | Search web |
| `*` | Wildcard — applies to all tools |

Example — deny all editing, allow reading and searching:

```json
"permission": {
    "file_write": "deny",
    "edit": "deny",
    "apply_patch": "deny"
}
```

Unspecified tools default to `"allow"`.

### Настройка разрешений (русский)

#### Действия

Каждому инструменту (или паттерну команды) назначается одно из действий:

| Действие | Что происходит |
|----------|----------------|
| `"allow"` | Разрешить без вопросов |
| `"deny"` | Запретить всегда |
| `"ask"` | Спрашивать пользователя при каждом вызове |

По умолчанию (если инструмент не указан) — `"allow"`.

#### Разрешения для инструментов

В блоке `permission` агента в `config.json` указывается `"инструмент": "действие"`:

```json
"permission": {
    "file_write": "ask",
    "edit": "deny",
    "apply_patch": "deny",
    "shell_execute": "ask",
    "web_fetch": "allow"
}
```

`"*"` задаёт действие для всех инструментов сразу:

```json
"permission": {
    "*": "allow",
    "shell_execute": "ask"
}
```

#### Разрешения для shell-команд

Команды оболочки (`shell_execute`) оцениваются по **паттернам команд**, а не только по имени инструмента. В конфиге паттерны не задаются — для `bash` указывается только действие для всех команд:

```json
"permission": {
    "bash": "ask"
}
```

`"bash": "ask"` — все shell-команды с подтверждением; `"bash": "deny"` — все shell-команды запрещены. Для пользовательского агента `shell_execute` по умолчанию уже стоит `ask`.

Специфичные паттерны (`git *`, `cat *`, `npm run dev *`) добавляются **в рантайме** при выборе "Always allow" в диалоге подтверждения и действуют до конца сессии. Пример правил, которые накапливаются за сессию:

- `"git *"` — любые команды `git` с аргументами (и просто `git`);
- `"cat *"` — любые `cat`;
- `"npm run dev *"` — запуск конкретной команды;
- `"*"` — любая команда.

Wildcard `*` может перекрывать `/` и несколько слов.

##### Порядок правил

Правила обрабатываются по порядку добавления, **выигрывает последнее подходящее правило**. Если команда не подпадает ни под одно правило — агент спросит пользователя (действие `ask`).

Пример — разрешить `git log`, но всё остальное спрашивать:

```json
"permission": {
    "bash": "ask"
}
```

Выбираем "Always allow" для `git log --oneline` → добавляется правило `"git log *": allow` → все команды `git log ...` выполняются без вопросов, остальное по-прежнему спрашивается.

##### Подкоманды

Одна команда может содержать несколько команд через `&&`, `||`, `;`, `|` или подстановку `$(...)`. Каждая подкоманда оценивается отдельно: если хотя бы одна запрещена — вся команда запрещается; если все разрешены — команда выполняется; иначе агент спросит.

##### Команды смены каталога

`cd`, `chdir`, `popd`, `pushd` никогда не требуют разрешения и в паттерны не попадают.

##### Разрешённые директории не спрашиваются

Если команда работает **только внутри разрешённых директорий** (рабочая папка сессии + `allowed_dirs` из конфига + выданные через `grant_access`), запрос разрешения не показывается вовсе — даже при `ask`. Команда считается «в разрешённых директориях», если:

- все извлечённые пути (`cat`, `ls`, `cp`, `mv`, `rm`, `grep`, `find`, `git` и др.), цели редиректов (`>`, `>>`, `2>` и т.п.) и явные пути (абсолютные, `~`, `..`) в любом месте команды находятся внутри разрешённых директорий;
- либо это файловая команда без явных путей (неявно работает в рабочей папке): `ls`, `git status`, `git pull`, `go build`, `make` и т.п.;
- цель `cd`/`pushd` тоже находится в разрешённых директориях.

Ведущие env-присваивания (`VAR=...`) при определении команды пропускаются: `LD_LIBRARY_PATH=... nohup ~/Android/Sdk/emulator/emulator ... > /tmp/emulator.log 2>&1` достаточно добавить `~/Android/Sdk/emulator` в `allowed_dirs` — команда не будет спрашиваться. Точковые токены, не являющиеся путями (`com.avito.android`, `1.2.3`, версии пакетов), путями не считаются.

##### Удалённые устройства (adb, ssh, scp) не проверяются

Пути, принадлежащие файловой системе удалённого устройства/хоста, против `allowed_dirs` хоста не проверяются:

- `adb shell ...`, `adb exec-out/exec-in ...` — всё после глагола работает на устройстве (`adb shell uiautomator dump /data/local/tmp/ui.xml`);
- `adb push <local> <remote>` / `adb pull <remote> <local>` / `adb install` — проверяется только хостовый файл (источник/приёмник/пакет);
- `ssh [user@]host <cmd>` — команда после host выполняется на удалённой машине;
- `scp host:...` — пути `host:path` считаются удалёнными.

Пути устройства, упомянутые в последующих подкомандах цепочки, тоже не считаются хостовыми: `adb shell uiautomator dump /data/local/tmp/ui.xml && cat /data/local/tmp/ui.xml && head -30` не содержит хостовых файловых операций (файл живёт на устройстве). Хостовые редиректы по-прежнему проверяются: `adb shell screencap /sdcard/x.png > /etc/out.png` спросит разрешение.

Если команда трогает что-то вне разрешённых директорий (`cat /etc/passwd`, `cd /tmp && ...`, `rm -rf ..`, `echo hi > /etc/file`) или не оперирует файлами (`pip install`, `curl ... | bash`) — применяются обычные правила паттернов и спрашивается пользователь. Это позволяет агенту работать ночью без остановок, пока он не выходит за пределы разрешённых папок.

##### Пропуск проверки для команд без файловых операций

Если в `config.json` задано `"skip_shell_permission_without_paths": true`, запрос разрешения не показывается, если команда **не трогает файлы вне разрешённых директорий**:

- в ней нет хостовых файловых путей вовсе — `adb -s emulator-5554 devices -l`, `adb shell am force-stop com.avito.android`, `git log --oneline`, `echo hi`, `sleep 1`;
- либо все хостовые пути находятся в `allowed_dirs` (рабочая папка сессии тоже считается) — например цепочка `adb shell uiautomator dump /data/local/tmp/ui.xml && sleep 1 && adb pull /data/local/tmp/ui.xml ./ui_test.xml && head -30 ui_test.xml`: `./ui_test.xml` попадает в рабочую папку;
- пути устройства (`adb shell`, `adb pull <remote>`, `ssh host`, `scp host:...`) хостовыми не считаются.

Команда, которая читает/пишет файл вне `allowed_dirs` (`echo hi > /etc/file`, `cat /etc/passwd`, `adb push /etc/passwd /sdcard/`), по-прежнему спрашивается. Явные правила `deny` в паттернах остаются приоритетнее флага.

#### Запрос разрешения у пользователя

При действии `ask` агент показывает диалог с тремя вариантами:

- **Allow** — разрешить один раз;
- **Always allow** — запомнить префикс команды (например `git *` для `git log --oneline`) до конца сессии и больше не спрашивать;
- **Deny** — запретить.

Запоминание работает через `Approve`, который добавляет правило `allow` в правила текущей сессии.

#### Как это работает (кратко)

1. Для `shell_execute` команда разбивается на подкоманды (`pkg/permission.ScanCommand`).
2. Для каждой подкоманды определяется паттерн-префикс (`pkg/permission.Prefix`, например `git log`).
3. Каждый паттерн оценивается правилами `bash` (`pkg/permission.Evaluate`, последнее подходящее правило выигрывает).
4. Если все разрешены — команда выполняется, если хотя бы одна запрещена — блокируется, иначе спрашиваем пользователя.

### Usage

Send a message with `#agent_name` prefix to route to a specific agent:

```
#developer напиши тесты для модуля
#reviewer проверь код на ошибки
#qa запусти тесты и отчитайся
```

The lead agent (`#lead`) orchestrates the full pipeline: delegate to developer → reviewer → qa.

### Prompt files

Agent prompt files (`.md`) contain **only the system prompt** — no frontmatter. All configuration (description, mode, permissions) lives in `config.json`. Example `agents/developer.md`:

```markdown
You are a Developer. Implement the task using available tools.

## Instructions
1. Implement the task completely.
2. Verify your code: run the build command.
3. Return the complete result.
```

## Bot Commands

| Command | Description |
|---------|-------------|
| `/clear` | Clear conversation history (working dir is preserved) |
| `/newsession [path]` | Reset session and change working dir |
| `/status` | Show session info and working dir |
| `/help` | Show command list |
| `/restart` | Restart the agent without rebuilding (handled by the restarter process) |
| `/update` | `git pull`, rebuild and restart the agent (handled by the restarter process) |

Commands starting with `/` are handled by the bot and never sent to the model.

## Testing

Подробное руководство по тестированию: **[TESTS.md](TESTS.md)** — мокирование LLM, scripted-серверы, сценарные тесты, mock-инструменты, чек-лист.

### Scenario-based integration tests

Orchestrator tests use predefined LLM responses (no llama-server required). Each scenario is a directory under `pkg/agentloop/testdata/scenarios/`:

```
testdata/scenarios/<name>/
├── prompt.txt              # User's task prompt
├── 000_coordinator.txt     # Coordinator response
├── 001_developer.txt       # Developer response
├── 002_reviewer.txt        # Reviewer response (XML approve/revise)
├── 003_reviewer_result.txt # Reviewer follow-up after tool call
└── assert.txt              # Assertions: "contains: ..." / "not_contains: ..."
```

Run all scenarios:
```bash
go test -v -run "TestScenario" ./pkg/agentloop/
```

Run a specific scenario:
```bash
go test -v -run "TestScenario_RevisionCycle" ./pkg/agentloop/
```

To add a new scenario, create a folder with `prompt.txt`, numbered step files, and optional `assert.txt`, then add one line to `run_scenario_test.go`:
```go
func TestScenario_MyCase(t *testing.T) { runScenario(t, "my_case") }
```

## Project Structure

```
.                         # Main AI agent (main.go)
cmd/vk-gateway-restarter/  # Restarter for remote updates
pkg/agent/                   # AI Agent: streaming, function calling
pkg/agentloop/               # Conversation orchestration
pkg/tools/                   # 11 tool implementations
pkg/vk/                      # VK Bot API client + handler
session/                     # Session memory with persistence
system_prompt.txt            # System prompt for the AI model
config.json                  # Configuration
```

## Restarter Commands

These commands are handled by `vk-gateway-restarter` via VK:

| Command | Description |
|---------|-------------|
| `/update` | Git pull, rebuild, restart agent |
| `/b <branch>` | Force checkout branch, pull, rebuild, restart |
| `/restart` | Restart agent without rebuild |
| `/status` | Show agent status and current branch |
| `/help` | Show command list |
