# Luma MVP (Local Silent Companion Agent)

Luma is a local-first, desktop companion agent that decides when to gently intervene. This MVP focuses on clean architecture, data closure, and safety gates.

## Architecture (MVP)

```
+--------------------+        HTTP        +---------------------+
| Electron + Vue UI  | <----------------> | Go Core Service     |
| apps/desktop       |                    | services/core-go    |
+--------------------+                    +----------+----------+
                                                      |
                                                      | HTTP (strict validation + retry)
                                                      v
                                            +---------------------+
                                            | Python AI Service   |
                                            | services/ai-py      |
                                            +---------------------+
                                                      |
                                                      v
                                               SQLite (local)
```

## Goals
- Local-only execution, no cloud dependency.
- AI outputs only Action; system operations are blocked by a permission gateway.
- All decisions/feedback are logged and auditable in SQLite + JSONL export.
- Policy/model versions are explicit for rollback and offline evaluation.
- Focus monitoring is local-only, opt-in, and stores only frontmost app metadata.

## Services & Ports
- Desktop UI: Vite dev server `http://localhost:5173`
- Core Go API: `http://127.0.0.1:8081`
- AI Service: `http://127.0.0.1:8788`

## Quick Start

### 1) Start all services
```
make dev
```

Or:
```
./scripts/dev.sh
```

### 2) Open the desktop UI
Electron launches automatically via `npm run dev`.

## Make Targets
- `make dev`: start all services.
- `make fmt`: go fmt + frontend lint (if present) + python ruff format.
- `make test`: go test + python import check + frontend build.
- `make proto`: placeholder for future gRPC stub generation.

## API

### POST /v1/decision
Request:
```json
{
  "context": {
    "user_text": "I am rushing a paper and feel stressed",
    "timestamp": 1710000000000,
    "mode": "LIGHT",
    "signals": {
      "hour_of_day": "21",
      "session_minutes": "40"
    },
    "history_summary": ""
  }
}
```

Response (example):
```json
{
  "request_id": "b0f2c78e-1aa5-4d4c-9c77-3d7b41b3e8bd",
  "context": {
    "user_text": "I am rushing a paper and feel stressed",
    "timestamp": 1710000000000,
    "mode": "LIGHT",
    "signals": {
      "hour_of_day": "21",
      "session_minutes": "40",
      "quiet_hours": "23:30-08:00",
      "intervention_budget": "2"
    },
    "history_summary": ""
  },
  "action": {
    "action_type": "TASK_BREAKDOWN",
    "message": "Try listing the next three smallest steps to reduce pressure.",
    "confidence": 0.78,
    "cost": 0.3,
    "risk_level": "LOW"
  },
  "policy_version": "policy_v0",
  "model_version": "stub",
  "latency_ms": 34,
  "created_at_ms": 1710000000123,
  "gateway_decision": {
    "decision": "ALLOW",
    "reason": "allow"
  }
}
```

### POST /v1/feedback
```json
{
  "request_id": "b0f2c78e-1aa5-4d4c-9c77-3d7b41b3e8bd",
  "feedback": "LIKE"
}
```

### GET /v1/logs?limit=50
Returns the latest decision logs.

### GET /v1/settings
Returns all user settings.

### POST /v1/settings
```json
{
  "key": "quiet_hours",
  "value": "23:30-08:00"
}
```
Notes:
- Supported keys: `quiet_hours`, `intervention_budget`, `focus_monitor_enabled`.
- `intervention_budget` accepts `low|medium|high` and is mapped to `1|2|3` in `signals`.
- `quiet_hours` uses `HH:MM-HH:MM` (e.g., `23:30-08:00`).
- `focus_monitor_enabled` accepts `true|false`.
- Python formatting uses `ruff` (installed via `services/ai-py/requirements.txt`).

### GET /v1/focus/current
```json
{
  "ts_ms": 1710000000123,
  "app_name": "Safari",
  "bundle_id": "com.apple.Safari",
  "pid": 12345,
  "focus_minutes": 12.3
}
```

### GET /v1/focus/recent?limit=200
Returns the most recent focus events.

### GET /v1/export?limit=1000&since_ms=...
Returns `application/x-ndjson` (JSONL) for offline replay.

## JSONL Export
Each line contains one decision record for offline evaluation/replay:
```json
{"request_id":"b0f2c78e-1aa5-4d4c-9c77-3d7b41b3e8bd","context":{"user_text":"..."},"raw_action":{"action_type":"ENCOURAGE","message":"...","confidence":0.7,"cost":0.2,"risk_level":"LOW"},"final_action":{"action_type":"DO_NOT_DISTURB","message":"...","confidence":1,"cost":0,"risk_level":"LOW"},"gateway_decision":{"decision":"OVERRIDE","reason":"mode_silent_override","overridden_action_type":"ENCOURAGE"},"user_feedback":"LIKE","policy_version":"policy_v0","model_version":"stub","latency_ms":42,"created_at_ms":1710000000123}
```

## SQLite Logs
- DB path: `./data/luma.db`
- Tables:
  - `event_logs` (request_id, context_json, raw_action_json, final_action_json, gateway_decision_json, policy_version, model_version, latency_ms, created_at_ms, user_feedback)
  - `feedback_logs` (request_id, feedback, created_at_ms)
  - `user_settings` (key, value, updated_at_ms)
  - `focus_events` (ts_ms, app_name, bundle_id, pid, duration_ms)
- Example query:
```
sqlite3 ./data/luma.db "select request_id, policy_version, user_feedback, created_at_ms from event_logs order by created_at_ms desc limit 5;"
```

## Focus Monitoring (macOS)
- Runs locally and only captures frontmost app metadata (app name, bundle_id, pid).
- No screenshots, no keyboard input, no window contents.
- Window titles are not collected by default (reserved for future AX API support).
- Disabled by default. Enable via `focus_monitor_enabled` setting.
- Uses a local Swift helper at `cmd/focusd` (built on first run) and polls every 1s (configurable via `FOCUS_POLL_MS`).

## Signals Injection
When focus monitoring is enabled, `/v1/decision` auto-fills:
- `focus_app`
- `focus_bundle_id`
- `focus_minutes`

## Safety & Extensibility
- AI service only outputs Action; it never executes system operations.
- Any HIGH risk action is blocked by the gateway.
- Policy/model versions are logged for rollback and A/B experiments.
- The `policy/` module in `services/ai-py` is the entry point for contextual bandit and preference learning.
- Set `LUMA_POLICY=rule_v0` to select the AI policy (defaults to `rule_v0`).

## Curl Examples
Decision:
```
curl -s http://127.0.0.1:8081/v1/decision \
  -H "Content-Type: application/json" \
  -d '{"context":{"user_text":"Need focus","timestamp":1710000000000,"mode":"LIGHT","signals":{},"history_summary":""}}'
```

Feedback:
```
curl -s http://127.0.0.1:8081/v1/feedback \
  -H "Content-Type: application/json" \
  -d '{"request_id":"b0f2c78e-1aa5-4d4c-9c77-3d7b41b3e8bd","feedback":"LIKE"}'
```

Export:
```
curl -s "http://127.0.0.1:8081/v1/export?limit=10&since_ms=0"
```

Focus current:
```
curl -s "http://127.0.0.1:8081/v1/focus/current"
```

Focus recent:
```
curl -s "http://127.0.0.1:8081/v1/focus/recent?limit=5"
```

Enable focus monitor:
```
curl -s http://127.0.0.1:8081/v1/settings \
  -H "Content-Type: application/json" \
  -d '{"key":"focus_monitor_enabled","value":"true"}'
```

Decision with focus signals:
```
curl -s http://127.0.0.1:8081/v1/decision \
  -H "Content-Type: application/json" \
  -d '{"context":{"user_text":"Need focus","timestamp":1710000000000,"mode":"LIGHT","signals":{},"history_summary":""}}'
```

## Project Structure
```
apps/desktop        Electron + Vue
services/core-go    Go HTTP API + SQLite
services/ai-py      FastAPI AI service
proto               gRPC definitions (future use)
scripts             Dev scripts
```

## gRPC Protocol
The file `proto/luma.proto` defines Context/Action/Feedback plus Decision/EventLog/GatewayDecision for future gRPC communication. Current MVP uses HTTP with strict validation and retry in the Go client.
