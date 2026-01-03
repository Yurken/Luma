package models

import "time"

type Mode string

const (
	ModeSilent Mode = "SILENT"
	ModeLight  Mode = "LIGHT"
	ModeActive Mode = "ACTIVE"
)

type FeedbackType string

const (
	FeedbackLike    FeedbackType = "LIKE"
	FeedbackDislike FeedbackType = "DISLIKE"
	FeedbackAdopted FeedbackType = "ADOPTED"
	FeedbackIgnored FeedbackType = "IGNORED"
	FeedbackClosed  FeedbackType = "CLOSED"
	FeedbackOpen    FeedbackType = "OPEN_PANEL"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "LOW"
	RiskMedium RiskLevel = "MEDIUM"
	RiskHigh   RiskLevel = "HIGH"
)

type ActionType string

const (
	ActionDoNotDisturb  ActionType = "DO_NOT_DISTURB"
	ActionEncourage     ActionType = "ENCOURAGE"
	ActionTaskBreakdown ActionType = "TASK_BREAKDOWN"
	ActionRestReminder  ActionType = "REST_REMINDER"
	ActionReframe       ActionType = "REFRAME"
)

type GatewayDecisionType string

const (
	GatewayAllow    GatewayDecisionType = "ALLOW"
	GatewayDeny     GatewayDecisionType = "DENY"
	GatewayOverride GatewayDecisionType = "OVERRIDE"
)

type Context struct {
	UserText       string            `json:"user_text"`
	Timestamp      int64             `json:"timestamp"`
	Mode           Mode              `json:"mode"`
	Signals        map[string]string `json:"signals"`
	HistorySummary string            `json:"history_summary"`
	ProfileSummary string            `json:"profile_summary"`
	MemorySummary  string            `json:"memory_summary"`
	FocusState     string            `json:"focus_state,omitempty"`
	SwitchCount    int               `json:"switch_count,omitempty"`
}

type Action struct {
	ActionType ActionType `json:"action_type"`
	Message    string     `json:"message"`
	Confidence float64    `json:"confidence"`
	Cost       float64    `json:"cost"`
	RiskLevel  RiskLevel  `json:"risk_level"`
	Reason     string     `json:"reason,omitempty"`
	State      string     `json:"state,omitempty"`
}

type DecisionRequest struct {
	RequestID string  `json:"request_id,omitempty"`
	Context   Context `json:"context"`
}

type GatewayDecision struct {
	Decision             GatewayDecisionType `json:"decision"`
	Reason               string              `json:"reason"`
	OverriddenActionType ActionType          `json:"overridden_action_type,omitempty"`
}

type DecisionResponse struct {
	RequestID       string          `json:"request_id"`
	Context         Context         `json:"context"`
	Action          Action          `json:"action"`
	PolicyVersion   string          `json:"policy_version"`
	ModelVersion    string          `json:"model_version"`
	LatencyMs       int64           `json:"latency_ms"`
	CreatedAt       time.Time       `json:"created_at,omitempty"`
	CreatedAtMs     int64           `json:"created_at_ms"`
	GatewayDecision GatewayDecision `json:"gateway_decision"`
}

type FeedbackRequest struct {
	RequestID    string       `json:"request_id"`
	Feedback     FeedbackType `json:"feedback"`
	FeedbackText string       `json:"feedback_text,omitempty"`
	Context      Context      `json:"context,omitempty"` // Context for generating reply
}

type DecisionLogEntry struct {
	RequestID       string
	Context         Context
	RawAction       Action
	FinalAction     Action
	GatewayDecision GatewayDecision
	PolicyVersion   string
	ModelVersion    string
	LatencyMs       int64
	CreatedAt       time.Time
	CreatedAtMs     int64
}

type EventLog struct {
	RequestID       string          `json:"request_id"`
	Context         Context         `json:"context"`
	Action          Action          `json:"action"`
	RawAction       Action          `json:"raw_action"`
	FinalAction     Action          `json:"final_action"`
	GatewayDecision GatewayDecision `json:"gateway_decision"`
	PolicyVersion   string          `json:"policy_version"`
	ModelVersion    string          `json:"model_version"`
	LatencyMs       int64           `json:"latency_ms"`
	UserFeedback    string          `json:"user_feedback,omitempty"`
	CreatedAt       time.Time       `json:"created_at,omitempty"`
	CreatedAtMs     int64           `json:"created_at_ms"`
	ContextJSON     string          `json:"context_json,omitempty"`
	ActionJSON      string          `json:"action_json,omitempty"`
}

type ExportRecord struct {
	RequestID       string          `json:"request_id"`
	Context         Context         `json:"context"`
	RawAction       Action          `json:"raw_action"`
	FinalAction     Action          `json:"final_action"`
	GatewayDecision GatewayDecision `json:"gateway_decision"`
	UserFeedback    string          `json:"user_feedback,omitempty"`
	PolicyVersion   string          `json:"policy_version"`
	ModelVersion    string          `json:"model_version"`
	LatencyMs       int64           `json:"latency_ms"`
	CreatedAtMs     int64           `json:"created_at_ms"`
}

type SettingItem struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	UpdatedAtMs int64  `json:"updated_at_ms"`
}

type SettingRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type BudgetUsage struct {
	DailyUsed  float64 `json:"daily_used"`
	DailyDay   string  `json:"daily_day"`
	HourlyUsed float64 `json:"hourly_used"`
	HourlyHour string  `json:"hourly_hour"`
}

type FocusStateSnapshot struct {
	TsMs         int64   `json:"ts_ms"`
	FocusState   string  `json:"focus_state"`
	SwitchCount  int     `json:"switch_count"`
	NoProgressMs int64   `json:"no_progress_ms"`
	FocusMinutes float64 `json:"focus_minutes"`
	AppName      string  `json:"app_name,omitempty"`
	WindowTitle  string  `json:"window_title,omitempty"`
}

type FocusMetrics struct {
	WindowMs     int64   `json:"window_ms"`
	SwitchCount  int     `json:"switch_count"`
	FocusMinutes float64 `json:"focus_minutes"`
}

type FocusEvent struct {
	ID          int64  `json:"id"`
	TsMs        int64  `json:"ts_ms"`
	AppName     string `json:"app_name"`
	BundleID    string `json:"bundle_id,omitempty"`
	PID         int    `json:"pid,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	WindowTitle string `json:"window_title,omitempty"`
}

type FocusCurrent struct {
	TsMs         int64   `json:"ts_ms"`
	AppName      string  `json:"app_name"`
	BundleID     string  `json:"bundle_id,omitempty"`
	PID          int     `json:"pid,omitempty"`
	WindowTitle  string  `json:"window_title,omitempty"`
	FocusMinutes float64 `json:"focus_minutes"`
}
