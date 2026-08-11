# AI Provider Configuration

SeekerStudio supports any AI provider through 4 environment variables. No code changes needed.

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `AI_PROTOCOL` | Yes | `"anthropic"` or `"openai"` — determines request format |
| `AI_BASE_URL` | Yes | API endpoint URL |
| `AI_API_KEY` | Yes | Your API key |
| `AI_MODEL` | Yes | Model identifier |

## Protocol Selection

Use `AI_PROTOCOL` to tell SeekerStudio which request format to use:

- **`anthropic`** — Uses Anthropic Messages API format (`/v1/messages`, `x-api-key` header, `content` blocks)
- **`openai`** — Uses OpenAI Chat Completions format (`/chat/completions`, `Authorization: Bearer` header, `tool_calls`)

## Provider Examples

### OpenRouter (recommended for testing)

```bash
AI_PROTOCOL=openai
AI_BASE_URL=https://openrouter.ai/api/v1
AI_API_KEY=sk-or-v1-...
AI_MODEL=deepseek/deepseek-v4-flash
```

### AgentRouter

```bash
AI_PROTOCOL=anthropic
AI_BASE_URL=https://agentrouter.org
AI_API_KEY=sk-your-key
AI_MODEL=claude-opus-5
```

### Anthropic Direct

```bash
AI_PROTOCOL=anthropic
AI_BASE_URL=https://api.anthropic.com
AI_API_KEY=sk-ant-...
AI_MODEL=claude-sonnet-4-20250514
```

### Any OpenAI-compatible API

```bash
AI_PROTOCOL=openai
AI_BASE_URL=https://your-provider.com/v1
AI_API_KEY=your-key
AI_MODEL=model-name
```

## How to Switch

Edit `backend/.env` and restart the backend:

```bash
cd ~/Desktop/Projects/SeekerStudio/backend
# Edit .env with your new values
export $(cat .env | xargs) && go run ./cmd/server/
```

## How It Works

1. `AI_PROTOCOL` determines which code path runs:
   - `anthropic` → `chatAnthropic()`, `streamAnthropic()`, `chatWithToolsAnthropic()`
   - `openai` → `chatOpenAI()`, `streamOpenRouter()`, `chatWithToolsOpenRouter()`

2. Auth headers are set automatically:
   - `anthropic` → `x-api-key: <key>`
   - `openai` → `Authorization: Bearer <key>`

3. Endpoint paths are set automatically:
   - `anthropic` → `<base_url>/v1/messages`
   - `openai` → `<base_url>/chat/completions`
