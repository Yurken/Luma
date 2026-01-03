from abc import ABC, abstractmethod
from typing import Tuple

from models import Action, Context


class Policy(ABC):
    name = "base"
    # TODO: Add contextual bandit policy and explainable reasoning hooks.

    @abstractmethod
    def decide(self, context: Context) -> Tuple[Action, str, str]:
        raise NotImplementedError
