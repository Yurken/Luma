import json
import logging
import os
import time
from typing import Optional, Tuple

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from models import Action, ActionType, DecideRequest, DecideResponse, FeedbackRequest, RiskLevel
from policy import get_policy

app = FastAPI(title="Luma AI Service")

LOG_DIR = os.path.join(os.path.dirname(__file__), "logs")
LOG_PATH = os.path.join(LOG_DIR, "ai_feedback.jsonl")

os.makedirs(LOG_DIR, exist_ok=True)

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("luma-ai")

policy_name = os.getenv("LUMA_POLICY", "ollama")
policy = get_policy(policy_name)


def parse_bool(value: Optional[str]) -> Optional[bool]:
    if value is None:
        return None
    normalized = value.strip().lower()
    if normalized in {"1", "true", "yes", "on"}:
        return True
    if normalized in {"0", "false", "no", "off"}:
        return False
    return None


def env_bool(name: str, default: bool) -> bool:
    parsed = parse_bool(os.getenv(name))
    return parsed if parsed is not None else default


def resolve_agent_settings(context) -> Tuple[bool, bool]:
    agent_enabled = env_bool("LUMA_AGENT_ENABLED", True)
    rule_only = env_bool("LUMA_RULE_ONLY", False)
    signals = context.signals or {}
    signal_agent = parse_bool(signals.get("agent_enabled"))
    if signal_agent is not None:
        agent_enabled = signal_agent
    signal_rule = parse_bool(signals.get("rule_only_mode") or signals.get("rule_only"))
    if signal_rule is not None:
        rule_only = signal_rule
    return agent_enabled, rule_only


@app.post("/ai/decide", response_model=DecideResponse)
async def decide(payload: DecideRequest, request: Request) -> DecideResponse:
    request_id = payload.request_id or request.headers.get("X-Request-ID", "")
    agent_enabled, rule_only = resolve_agent_settings(payload.context)
    if not agent_enabled:
        action = Action(
            action_type=ActionType.DO_NOT_DISTURB,
            message="Agent 已关闭，当前不生成提示。",
            confidence=1.0,
            cost=0.0,
            risk_level=RiskLevel.LOW,
            reason="agent_disabled",
            state=payload.context.focus_state or payload.context.signals.get("focus_state", ""),
        )
        return DecideResponse(action=action, policy_version="agent_disabled", model_version="n/a")
    if rule_only:
        action = Action(
            action_type=ActionType.DO_NOT_DISTURB,
            message="规则模式已开启，已暂停 AI 提示。",
            confidence=1.0,
            cost=0.0,
            risk_level=RiskLevel.LOW,
            reason="rule_only",
            state=payload.context.focus_state or payload.context.signals.get("focus_state", ""),
        )
        return DecideResponse(action=action, policy_version="rule_only", model_version="n/a")
    action, policy_version, model_version = policy.decide(payload.context)
    policy.record_decision(request_id, payload.context, action)
    logger.info("decide request_id=%s policy=%s", request_id, policy_version)
    return DecideResponse(
        action=action,
        policy_version=policy_version,
        model_version=model_version,
    )


@app.post("/ai/feedback")
async def feedback(payload: FeedbackRequest, request: Request) -> JSONResponse:
    request_id = payload.request_id or request.headers.get("X-Request-ID", "")
    entry = {
        "request_id": request_id,
        "feedback": payload.feedback,
        "timestamp": int(time.time() * 1000),
    }
    logger.info("feedback: %s", json.dumps(entry, ensure_ascii=True))
    with open(LOG_PATH, "a", encoding="utf-8") as f:
        f.write(json.dumps(entry, ensure_ascii=True) + "\n")
    policy.record_feedback(request_id, payload.feedback)
    return JSONResponse({"status": "ok"})
