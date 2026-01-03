from typing import Tuple

from models import Action, ActionType, Context, RiskLevel
from policy.base import Policy


class UnavailablePolicy(Policy):
    name = "unavailable"

    def decide(self, context: Context) -> Tuple[Action, str, str]:
        return (
            Action(
                action_type=ActionType.DO_NOT_DISTURB,
                message="无可用环境",
                confidence=1.0,
                cost=0.0,
                risk_level=RiskLevel.LOW,
            ),
            self.name,
            "n/a",
        )
