from abc import ABC, abstractmethod
from typing import Tuple

from models import Action, Context


class Policy(ABC):
    name = "base"

    @abstractmethod
    def decide(self, context: Context) -> Tuple[Action, str, str]:
        raise NotImplementedError

    def record_decision(self, _request_id: str, _context: Context, _action: Action) -> None:
        return

    def record_feedback(self, _request_id: str, _feedback: str) -> None:
        return
