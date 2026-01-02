from policy.base import Policy
from policy.rule_v0 import RuleV0Policy
from policy.ollama import OllamaPolicy

_DEFAULT_POLICY = RuleV0Policy()

_POLICIES = {
    "rule_v0": _DEFAULT_POLICY,
    "ollama": OllamaPolicy(),
}


def get_policy(name: str) -> Policy:
    key = (name or "").strip().lower()
    if not key:
        return _DEFAULT_POLICY
    return _POLICIES.get(key, _DEFAULT_POLICY)
