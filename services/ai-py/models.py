from enum import Enum
from typing import Dict, Optional
from pydantic import BaseModel, Field


class Mode(str, Enum):
    SILENT = "SILENT"
    LIGHT = "LIGHT"
    ACTIVE = "ACTIVE"


class RiskLevel(str, Enum):
    LOW = "LOW"
    MEDIUM = "MEDIUM"
    HIGH = "HIGH"


class ActionType(str, Enum):
    DO_NOT_DISTURB = "DO_NOT_DISTURB"
    ENCOURAGE = "ENCOURAGE"
    TASK_BREAKDOWN = "TASK_BREAKDOWN"
    REST_REMINDER = "REST_REMINDER"
    REFRAME = "REFRAME"


class Context(BaseModel):
    user_text: str
    timestamp: int
    mode: Mode
    signals: Dict[str, str] = Field(default_factory=dict)
    history_summary: Optional[str] = ""
    profile_summary: Optional[str] = ""
    memory_summary: Optional[str] = ""


class Action(BaseModel):
    action_type: ActionType
    message: str
    confidence: float = Field(ge=0, le=1)
    cost: float
    risk_level: RiskLevel
    # TODO: Add optional reason/explanation fields for transparency.


class DecideRequest(BaseModel):
    context: Context
    request_id: Optional[str] = None


class DecideResponse(BaseModel):
    action: Action
    policy_version: str
    model_version: str


class FeedbackRequest(BaseModel):
    request_id: str
    feedback: str
