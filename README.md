# Miui Proxy Service (OpenAI + Claude Compatible)

This service provides OpenAI `/v1/chat/completions`, OpenAI `/v1/responses`, and Claude `/v1/messages` compatible APIs, backed by the MIUI DOUBAO upstream.

**Key Behavior**
- Model is always forced to `DOUBAO` regardless of request value.
- `Authorization` header is treated as the user identifier.
- `ConversationId` header is treated as the user-facing session id.
- Upstream MIUI conversation id is internal and derived from OAID + timestamp.
- In-memory cache persists after 30 seconds and is evicted after 60 seconds of inactivity.
- SQLite uses WAL with a single write queue to reduce lock contention.

**Endpoints**
1. `POST /v1/chat/completions`
2. `POST /v1/responses`
3. `POST /v1/messages`
4. `GET /health`

**Headers**
1. `Authorization: Bearer <token>` or any string
2. `ConversationId: <custom-session-id>`
3. Optional: `X-Deep-Thinking: true`
4. Optional: `X-Online-Search: true`
5. Optional: `X-Disable-Search: true`

**Quick Start**
1. `go mod tidy`
2. `go run .`

**Environment Variables**
- `PORT` - Server port (default: `8080`)
- `DB_PATH` - SQLite database path (default: `./miui.db`)

**Quick Start (Custom Port & DB)**
1. `PORT=9090 DB_PATH=./data/my.db go run .`

**Build (Local)**
1. `./build.sh`

**Docker Build**
1. `docker build -t miui-proxy .`

**Docker Run (Default)**
```bash
docker run -p 8080:8080 --name miui-proxy miui-proxy
```

**Docker Run (Persist SQLite)**
```bash
mkdir -p ./data
docker run -p 8080:8080 -v $(pwd)/data:/app --name miui-proxy miui-proxy
```

**Docker Run (Custom Port & DB)**
```bash
docker run -p 9090:9090 \
  -e PORT=9090 \
  -e DB_PATH=/data/custom.db \
  -v $(pwd)/data:/data \
  --name miui-proxy miui-proxy
```

**Docker Compose (Optional)**
```yaml
services:
  miui-proxy:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app
    environment:
      - PORT=8080
      - DB_PATH=/app/miui.db
```

**Docker Notes**
1. Default environment: `PORT=8080`, `DB_PATH=/app/miui.db`
2. Mount `/app` or set `DB_PATH` to persist SQLite data
3. Health check enabled: `GET /health`

**OpenAI Chat Completions (non-stream)**
```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer demo-user" \
  -H "ConversationId: session-a" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      {"role":"system","content":"你是一个专业助手"},
      {"role":"user","content":"介绍一下你自己"}
    ],
    "stream": false,
    "online_search": true,
    "deep_thinking": false
  }'
```

**OpenAI Chat Completions (stream)**
```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer demo-user" \
  -H "ConversationId: session-a" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role":"user","content":"给我一段简短的励志话"}],
    "stream": true
  }'
```

**OpenAI Responses (non-stream)**
```bash
curl -X POST http://localhost:8080/v1/responses \
  -H "Authorization: Bearer demo-user" \
  -H "ConversationId: session-b" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "input": "用一句话解释量子纠缠",
    "stream": false
  }'
```

**OpenAI Responses (stream)**
```bash
curl -N http://localhost:8080/v1/responses \
  -H "Authorization: Bearer demo-user" \
  -H "ConversationId: session-b" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "input": "写一段 50 字的产品介绍",
    "stream": true
  }'
```

**Claude Messages (non-stream)**
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer demo-user" \
  -H "ConversationId: session-c" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "system": "你是一个严谨的助手",
    "messages": [{"role":"user","content":"解释一下递归"}],
    "stream": false
  }'
```

**Claude Messages (stream)**
```bash
curl -N http://localhost:8080/v1/messages \
  -H "Authorization: Bearer demo-user" \
  -H "ConversationId: session-c" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "system": "你是一个严谨的助手",
    "messages": [{"role":"user","content":"给我一个 2 段式总结"}],
    "stream": true
  }'
```

**Notes**
1. `Authorization` is treated as a plain user key. If missing, a random user is created.
2. `ConversationId` is optional. If missing, a default session is used per user.
3. Request fields are accepted for compatibility. Only a subset is used.
4. Streaming matches OpenAI SSE and Claude event formats as specified.
5. Default behavior enables deep thinking and search unless explicitly disabled in the request.
6. Model suffix rules (apply to any model name):
   - `-thinking` enables deep thinking and disables search
   - `-search` enables search and disables deep thinking
   - `-thinking-search` enables both
