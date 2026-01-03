import json
import os
import random
from typing import Dict, Tuple

from models import Action, ActionType, Context, RiskLevel
from policy.base import Policy


class BanditPolicy(Policy):
    name = "bandit_v0"

    def __init__(self) -> None:
        self.stats_path = os.path.join(os.path.dirname(__file__), "..", "data", "bandit_stats.json")
        self.stats = self._load_stats()
        self.pending: Dict[str, Tuple[str, ActionType]] = {}
        self.epsilon = 0.15

    def decide(self, context: Context) -> Tuple[Action, str, str]:
        bucket = self._bucket_key(context)
        bucket_stats = self.stats.setdefault("buckets", {}).setdefault(bucket, self._init_bucket())

        if context.mode == "SILENT":
            chosen = ActionType.DO_NOT_DISTURB
            exploring = False
        else:
            chosen, exploring = self._select_action(bucket_stats)

        message = self._build_message(chosen, context)
        reason = self._build_reason(bucket, bucket_stats, chosen, exploring)

        action = Action(
            action_type=chosen,
            message=message,
            confidence=0.6,
            cost=self._action_cost(chosen),
            risk_level=RiskLevel.LOW,
            reason=reason,
        )
        return action, self.name, "bandit_local"

    def record_decision(self, request_id: str, context: Context, action: Action) -> None:
        if not request_id:
            return
        bucket = self._bucket_key(context)
        self.pending[request_id] = (bucket, action.action_type)

    def record_feedback(self, request_id: str, feedback: str) -> None:
        if not request_id:
            return
        entry = self.pending.pop(request_id, None)
        if not entry:
            return
        bucket, action_type = entry
        reward = self._feedback_reward(feedback)
        if reward == 0:
            return
        buckets = self.stats.setdefault("buckets", {})
        bucket_stats = buckets.setdefault(bucket, self._init_bucket())
        action_stats = bucket_stats.setdefault(action_type.value, {"count": 0, "reward": 0.0})
        action_stats["count"] += 1
        action_stats["reward"] += reward
        self._save_stats()

    def _bucket_key(self, context: Context) -> str:
        hour = context.signals.get("hour_of_day", "") if context.signals else ""
        focus_state = context.signals.get("focus_state", "UNKNOWN") if context.signals else "UNKNOWN"
        return f"{context.mode}|{hour}|{focus_state}"

    def _select_action(self, bucket_stats: Dict[str, Dict[str, float]]) -> Tuple[ActionType, bool]:
        actions = [a for a in ActionType]
        if random.random() < self.epsilon:
            return random.choice(actions), True

        best_actions = []
        best_score = None
        for action in actions:
            stats = bucket_stats.get(action.value, {"count": 0, "reward": 0.0})
            count = stats.get("count", 0)
            reward = stats.get("reward", 0.0)
            score = reward / count if count else 0.0
            if best_score is None or score > best_score:
                best_score = score
                best_actions = [action]
            elif score == best_score:
                best_actions.append(action)

        if not best_actions:
            return ActionType.ENCOURAGE, False
        return random.choice(best_actions), False

    def _build_message(self, action: ActionType, context: Context) -> str:
        focus_minutes = context.signals.get("focus_minutes", "0") if context.signals else "0"
        app_name = context.signals.get("focus_app", "") if context.signals else ""
        if action == ActionType.REST_REMINDER:
            return f"You've focused for {focus_minutes} minutes. Want a short break?"
        if action == ActionType.TASK_BREAKDOWN:
            return "If the task feels large, try breaking it into 2-3 small steps."
        if action == ActionType.REFRAME:
            return "Maybe try a different angle. Start with the easiest part?"
        if action == ActionType.ENCOURAGE:
            if app_name:
                return f"Nice pace in {app_name}. Keep it up."
            return "Good progress. Keep this pace."
        return "I'll stay quiet. Tap me if you need anything."

    def _build_reason(
        self,
        bucket: str,
        bucket_stats: Dict[str, Dict[str, float]],
        action: ActionType,
        exploring: bool,
    ) -> str:
        stats = bucket_stats.get(action.value, {"count": 0, "reward": 0.0})
        count = stats.get("count", 0)
        reward = stats.get("reward", 0.0)
        avg = reward / count if count else 0.0
        mode = "explore" if exploring else "exploit"
        return f"bandit:{mode} bucket={bucket} avg_reward={avg:.2f} count={count}"

    def _action_cost(self, action: ActionType) -> float:
        if action == ActionType.DO_NOT_DISTURB:
            return 0.0
        if action == ActionType.REST_REMINDER:
            return 2.0
        if action == ActionType.ENCOURAGE:
            return 1.5
        if action == ActionType.TASK_BREAKDOWN:
            return 3.0
        if action == ActionType.REFRAME:
            return 2.5
        return 1.0

    def _feedback_reward(self, feedback: str) -> float:
        feedback_type = feedback.split(":", 1)[0].strip().upper()
        if feedback_type in {"LIKE", "ADOPTED", "OPEN_PANEL"}:
            return 1.0
        if feedback_type in {"DISLIKE", "IGNORED", "CLOSED"}:
            return -1.0
        return 0.0

    def _init_bucket(self) -> Dict[str, Dict[str, float]]:
        return {action.value: {"count": 0, "reward": 0.0} for action in ActionType}

    def _load_stats(self) -> Dict[str, Dict[str, Dict[str, float]]]:
        try:
            if os.path.exists(self.stats_path):
                with open(self.stats_path, "r", encoding="utf-8") as f:
                    return json.load(f)
        except Exception:
            return {"buckets": {}}
        return {"buckets": {}}

    def _save_stats(self) -> None:
        os.makedirs(os.path.dirname(self.stats_path), exist_ok=True)
        try:
            with open(self.stats_path, "w", encoding="utf-8") as f:
                json.dump(self.stats, f, ensure_ascii=False, indent=2)
        except Exception:
            return
