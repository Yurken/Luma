import json
import logging
import os
import time
from typing import Optional, Tuple

import requests
from models import Action, Context
from .base import Policy

logger = logging.getLogger("luma-ai")

class OllamaPolicy(Policy):
    name = "ollama_v0"

    def __init__(self):
        self.model = os.getenv("OLLAMA_MODEL", "llama3.1:8b")
        self.api_url = os.getenv("OLLAMA_URL", "http://localhost:11434/api/generate")

    def decide(self, context: Context) -> Tuple[Action, str, str]:
        prompt = self._build_prompt(context)
        model = self.model
        if context.signals:
            override_model = context.signals.get("ollama_model", "").strip()
            if override_model:
                model = override_model
        precheck_action = self._precheck(context)
        if precheck_action is not None:
            return precheck_action, self.name, "precheck"
        
        try:
            logger.info(f"🤖 Calling Ollama model={model}")
            response = requests.post(
                self.api_url,
                json={
                    "model": model,
                    "prompt": prompt,
                    "stream": False,
                    "format": "json"
                },
                timeout=60
            )
            response.raise_for_status()
            data = response.json()
            content = data.get("response", "")
            
            logger.info(f"📥 Ollama raw response: {content}")
            
            action_data = json.loads(content)
            logger.info(f"✅ Parsed action: {json.dumps(action_data, ensure_ascii=False)}")
            
            reason = action_data.get("reason") or self._fallback_reason(context)
            state = action_data.get("state") or context.focus_state or context.signals.get("focus_state", "")
            action = Action(
                action_type=action_data.get("action_type", "DO_NOT_DISTURB"),
                message=action_data.get("message", "无法生成建议"),
                confidence=float(action_data.get("confidence", 0.5)),
                cost=float(action_data.get("cost", 0.0)),
                risk_level=action_data.get("risk_level", "LOW"),
                reason=reason,
                state=state,
            )
            return action, self.name, model
            
        except Exception as e:
            logger.error(f"Ollama call failed: {e}")
            return Action(
                action_type="DO_NOT_DISTURB",
                message="AI 服务暂时不可用",
                confidence=1.0,
                cost=0.0,
                risk_level="LOW",
                reason="ollama_error",
            ), self.name, "error"

    def _build_prompt(self, context: Context) -> str:
        app_name = context.signals.get("focus_app", "Unknown")
        window_title = context.signals.get("focus_window_title", "")
        focus_minutes = context.signals.get("focus_minutes", "0")
        if focus_minutes == "0":
            focus_minutes = context.signals.get("focus_minutes_window", "0")
        switch_count = context.signals.get("switch_count", "")
        if not switch_count:
            switch_count = str(context.switch_count or 0)
        no_progress_minutes = context.signals.get("no_progress_minutes", "0")
        focus_state = context.focus_state or context.signals.get("focus_state", "UNKNOWN")
        hour_of_day = context.signals.get("hour_of_day", "")
        user_text = context.user_text
        mode = context.mode
        
        profile_section = ""
        if context.profile_summary:
            profile_section = f"\nUser Profile (Preferences & Traits):\n{context.profile_summary}\n"
            
        memory_section = ""
        if context.memory_summary:
            memory_section = f"\nRecent Memory Events:\n{context.memory_summary}\n"

        return f"""
You are Luma, an intelligent desktop companion.
Your goal is to offer gentle, non-intrusive support without judging or commanding the user.
{profile_section}{memory_section}
Current Context:
- Mode: {mode} (SILENT: minimize disturbance, LIGHT: gentle reminders, ACTIVE: proactive)
- Focus State: {focus_state}
- App Switch Count: {switch_count}
- No-Progress Minutes: {no_progress_minutes}
- Focus Duration Minutes: {focus_minutes}
- Current App: {app_name}
- Window Title: {window_title}
- Hour of Day: {hour_of_day}
- User Input: "{user_text}"

Task:
Only use the explicit signals listed above. Do NOT infer screen content, keyboard content, or the user's task beyond those signals.
Always prioritize the user's input text. If input is present and meaningful, respond directly with a concise, helpful reply.
If input is empty or signals are weak, prefer DO_NOT_DISTURB instead of forcing a suggestion.
Use non-judgmental language; avoid commands and absolute judgments. Use gentle suggestions ("也许/可以/要不要").
Keep interventions low-frequency; if unsure, choose DO_NOT_DISTURB.
If late night (hour 23-5), you may offer quiet companionship or a short reflection prompt, but do not push tasks.
Use the User Profile and Recent Memory to personalize without sounding like monitoring.

Output Format (JSON only):
{{
  "action_type": "DO_NOT_DISTURB" | "ENCOURAGE" | "TASK_BREAKDOWN" | "REST_REMINDER" | "REFRAME",
  "message": "A short, friendly message to the user (in Chinese)",
  "confidence": 0.0 to 1.0,
  "cost": 0.0 to 1.0 (interruption cost),
  "risk_level": "LOW" | "MEDIUM" | "HIGH",
  "reason": "One short sentence citing concrete signals (e.g., focus_state=FOCUSED, switch_count=1)",
  "state": "FOCUSED" | "LIGHT" | "DISTRACTED" | "NO_PROGRESS" | "UNKNOWN"
}}
"""

    def _precheck(self, context: Context) -> Optional[Action]:
        if context.user_text and context.user_text.strip():
            return None
        signals = context.signals or {}
        now_ms = int(time.time() * 1000)

        if self._truthy(signals.get("cooldown_active")):
            return self._precheck_action("处于冷却期，暂不提示。", "cooldown_active", context)
        if self._truthy(signals.get("budget_exhausted")):
            return self._precheck_action("干预预算不足，暂不提示。", "budget_exhausted", context)

        cooldown_until = self._parse_int(signals.get("cooldown_until_ms"))
        if cooldown_until and cooldown_until > now_ms:
            return self._precheck_action("处于冷却期，暂不提示。", "cooldown_until", context)

        remaining_budget = self._parse_float(signals.get("budget_remaining"))
        if remaining_budget is not None and remaining_budget <= 0:
            return self._precheck_action("干预预算不足，暂不提示。", "budget_remaining", context)
        return None

    def _precheck_action(self, message: str, reason: str, context: Context) -> Action:
        state = context.focus_state or (context.signals.get("focus_state", "") if context.signals else "")
        return Action(
            action_type="DO_NOT_DISTURB",
            message=message,
            confidence=1.0,
            cost=0.0,
            risk_level="LOW",
            reason=f"precheck:{reason}",
            state=state or "UNKNOWN",
        )

    def _fallback_reason(self, context: Context) -> str:
        signals = context.signals or {}
        parts = []
        focus_state = context.focus_state or signals.get("focus_state")
        if focus_state:
            parts.append(f"focus_state={focus_state}")
        switch_count = signals.get("switch_count")
        if not switch_count and context.switch_count:
            switch_count = str(context.switch_count)
        if switch_count:
            parts.append(f"switch_count={switch_count}")
        focus_minutes = signals.get("focus_minutes") or signals.get("focus_minutes_window")
        if focus_minutes:
            parts.append(f"focus_minutes={focus_minutes}")
        if parts:
            return ", ".join(parts)
        return "model_no_reason"

    @staticmethod
    def _truthy(value: Optional[str]) -> bool:
        if value is None:
            return False
        return value.strip().lower() in {"1", "true", "yes", "on"}

    @staticmethod
    def _parse_int(value: Optional[str]) -> Optional[int]:
        if value is None:
            return None
        try:
            return int(value.strip())
        except ValueError:
            return None

    @staticmethod
    def _parse_float(value: Optional[str]) -> Optional[float]:
        if value is None:
            return None
        try:
            return float(value.strip())
        except ValueError:
            return None
