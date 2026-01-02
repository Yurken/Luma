package gateway

import (
	"log/slog"
	"sync"
	"time"

	"luma/core/internal/models"
)

const (
	ReasonBudgetExhausted = "budget_exhausted"
	ReasonCooldownActive  = "cooldown_active"
)

type Config struct {
	MaxBudget       float64
	RecoveryRate    float64 // points per minute
	CooldownSeconds float64
}

type Gateway struct {
	mu               sync.Mutex
	logger           *slog.Logger
	config           Config
	currentBudget    float64
	lastIntervention time.Time
	lastUpdate       time.Time
}

func New(logger *slog.Logger) *Gateway {
	// Default config
	cfg := Config{
		MaxBudget:       10.0,
		RecoveryRate:    0.5, // Recover 1 point every 2 mins
		CooldownSeconds: 300, // 5 minutes cooldown
	}

	return &Gateway{
		logger:        logger,
		config:        cfg,
		currentBudget: cfg.MaxBudget, // Start with full budget
		lastUpdate:    time.Now(),
	}
}

func (g *Gateway) Evaluate(ctx models.Context, action models.Action) (models.Action, models.GatewayDecision) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.replenishBudget()

	original := action
	decision := models.GatewayDecision{Decision: models.GatewayAllow, Reason: "allow"}

	// 1. Static Rules (Stateless)
	if reason, invalid := ruleInvalidAction(action); invalid {
		return overrideAction(original, models.GatewayOverride, reason)
	}
	if ruleHighRisk(action) {
		return overrideAction(original, models.GatewayDeny, ReasonHighRiskBlocked)
	}
	if ruleLowQuality(action) {
		return overrideAction(original, models.GatewayOverride, ReasonLowQualityAction)
	}
	if ruleSilentOverride(ctx, action) {
		return overrideAction(original, models.GatewayOverride, ReasonModeSilentOverride)
	}

	// 2. Dynamic Rules (Stateful) - Only check if action is NOT DoNotDisturb
	if action.ActionType != models.ActionDoNotDisturb {
		// Check Cooldown
		if time.Since(g.lastIntervention).Seconds() < g.config.CooldownSeconds {
			g.logger.Info("gateway cooldown active",
				slog.Float64("since_last", time.Since(g.lastIntervention).Seconds()),
				slog.Float64("cooldown", g.config.CooldownSeconds))
			return overrideAction(original, models.GatewayOverride, ReasonCooldownActive)
		}

		// Check Budget
		cost := calculateCost(action)
		if g.currentBudget < cost {
			g.logger.Info("gateway budget exhausted",
				slog.Float64("current", g.currentBudget),
				slog.Float64("cost", cost))
			return overrideAction(original, models.GatewayOverride, ReasonBudgetExhausted)
		}

		// Apply Cost
		g.currentBudget -= cost
		g.lastIntervention = time.Now()
		g.logger.Info("gateway intervention allowed",
			slog.Float64("cost", cost),
			slog.Float64("remaining", g.currentBudget))
	}

	return action, decision
}

func (g *Gateway) replenishBudget() {
	now := time.Now()
	elapsedMinutes := now.Sub(g.lastUpdate).Minutes()
	if elapsedMinutes <= 0 {
		return
	}

	recovered := elapsedMinutes * g.config.RecoveryRate
	g.currentBudget += recovered
	if g.currentBudget > g.config.MaxBudget {
		g.currentBudget = g.config.MaxBudget
	}
	g.lastUpdate = now
}

// ClearCooldown resets the cooldown timer to allow immediate interaction
func (g *Gateway) ClearCooldown() {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Set lastIntervention to a time in the past to bypass cooldown
	g.lastIntervention = time.Now().Add(-time.Duration(g.config.CooldownSeconds+1) * time.Second)
	g.logger.Info("gateway cooldown cleared, interaction enabled")
}

func calculateCost(action models.Action) float64 {
	// Base cost by action type
	// Can be enhanced to use action.Cost from AI if reliable
	switch action.ActionType {
	case models.ActionDoNotDisturb:
		return 0
	case models.ActionRestReminder:
		return 2.0
	case models.ActionEncourage:
		return 1.5
	case models.ActionTaskBreakdown:
		return 3.0
	case models.ActionReframe:
		return 2.5
	default:
		return 1.0
	}
}

func overrideAction(original models.Action, decisionType models.GatewayDecisionType, reason string) (models.Action, models.GatewayDecision) {
	final := models.Action{
		ActionType: models.ActionDoNotDisturb,
		Message:    defaultOverrideMessage(reason),
		Confidence: 1,
		Cost:       0,
		RiskLevel:  models.RiskLow,
	}

	decision := models.GatewayDecision{
		Decision:             decisionType,
		Reason:               reason,
		OverriddenActionType: original.ActionType,
	}

	return final, decision
}

func defaultOverrideMessage(reason string) string {
	switch reason {
	case ReasonModeSilentOverride:
		return "当前为静默模式，已降级为勿扰模式。"
	case ReasonLowQualityAction:
		return "当前建议质量不足，已降级为勿扰模式。"
	case ReasonHighRiskBlocked:
		return "高风险动作已被权限网关拦截。"
	case ReasonInvalidActionType, ReasonInvalidRiskLevel, ReasonInvalidConfidence:
		return "动作不合法，已降级为勿扰模式。"
	case ReasonBudgetExhausted:
		return "干预预算不足，已降级为勿扰模式。"
	case ReasonCooldownActive:
		return "处于冷却期，已降级为勿扰模式。"
	default:
		return "已降级为勿扰模式。"
	}
}
