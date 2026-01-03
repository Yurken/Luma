import json
import logging
import os
import time
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from models import DecideRequest, DecideResponse, FeedbackRequest
from policy import get_policy

app = FastAPI(title="Luma AI Service")

LOG_DIR = os.path.join(os.path.dirname(__file__), "logs")
LOG_PATH = os.path.join(LOG_DIR, "ai_feedback.jsonl")

os.makedirs(LOG_DIR, exist_ok=True)

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("luma-ai")

policy_name = os.getenv("LUMA_POLICY", "ollama")
policy = get_policy(policy_name)
# TODO: Support rule-only policy and a local "agent disabled" switch from core.


@app.post("/ai/decide", response_model=DecideResponse)
async def decide(payload: DecideRequest, request: Request) -> DecideResponse:
    request_id = payload.request_id or request.headers.get("X-Request-ID", "")
    action, policy_version, model_version = policy.decide(payload.context)
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
    return JSONResponse({"status": "ok"})
