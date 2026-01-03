package gateway

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"luma/core/internal/models"
)

const (
	ReasonBudgetExhausted = "budget_exhausted"
	ReasonCooldownActive  = "cooldown_active"
)

const (
	settingInterventionBudget = "intervention_budget"
	settingBudgetSilent       = "budget_silent"
	settingBudgetLight        = "budget_light"
	settingBudgetActive       = "budget_active"
	settingDailyBudgetCap     = "daily_budget_cap"
	settingHourlyBudgetCap    = "hourly_budget_cap"
	settingCooldownSeconds    = "cooldown_seconds"
)

type Config struct {
	ModeBudgets     map[models.Mode]float64
	RecoveryRate    float64 // points per minute
	CooldownSeconds float64
	HourlyCap       float64
	DailyCap        float64
}

type SettingsStore interface {
	GetSetting(key string) (string, bool, error)
	GetBudgetUsage() (models.BudgetUsage, error)
	SetBudgetUsage(models.BudgetUsage) error
}

type Gateway struct {
	mu               sync.Mutex
	logger           *slog.Logger
	store            SettingsStore
	config           Config
	currentBudget    map[models.Mode]float64
	lastIntervention time.Time
	lastUpdate       map[models.Mode]time.Time
	dailyUsed        float64
	hourlyUsed       float64
	dayBucket        string
	hourBucket       string
	usageLoaded      bool
}

func New(logger *slog.Logger, store SettingsStore) *Gateway {
	cfg := Config{
		ModeBudgets:     defaultModeBudgets(),
		RecoveryRate:    0.5, // Recover 1 point every 2 mins
		CooldownSeconds: 300, // 5 minutes cooldown
	}
	now := time.Now()
	current := map[models.Mode]float64{}
	lastUpdate := map[models.Mode]time.Time{}
	for mode, max := range cfg.ModeBudgets {
		current[mode] = max
		lastUpdate[mode] = now
	}
	return &Gateway{
		logger:        logger,
		store:         store,
		config:        cfg,
		currentBudget: current,
		lastUpdate:    lastUpdate,
	}
}

func defaultModeBudgets() map[models.Mode]float64 {
	return map[models.Mode]float64{
		models.ModeSilent: 2.0,
		models.ModeLight:  6.0,
		models.ModeActive: 10.0,
	}
}

func (g *Gateway) refreshConfigLocked() {
	cfg := Config{
		ModeBudgets:     defaultModeBudgets(),
		RecoveryRate:    g.config.RecoveryRate,
		CooldownSeconds: g.config.CooldownSeconds,
		HourlyCap:       g.config.HourlyCap,
		DailyCap:        g.config.DailyCap,
	}

	if g.store != nil {
		if value, ok, err := g.store.GetSetting(settingInterventionBudget); err == nil && ok {
			applyInterventionBudget(cfg.ModeBudgets, value)
		}
		if value, ok, err := g.store.GetSetting(settingBudgetSilent); err == nil && ok {
			if parsed, ok := parseFloatSetting(value); ok {
				cfg.ModeBudgets[models.ModeSilent] = parsed
			}
		}
		if value, ok, err := g.store.GetSetting(settingBudgetLight); err == nil && ok {
			if parsed, ok := parseFloatSetting(value); ok {
				cfg.ModeBudgets[models.ModeLight] = parsed
			}
		}
		if value, ok, err := g.store.GetSetting(settingBudgetActive); err == nil && ok {
			if parsed, ok := parseFloatSetting(value); ok {
				cfg.ModeBudgets[models.ModeActive] = parsed
			}
		}
		if value, ok, err := g.store.GetSetting(settingHourlyBudgetCap); err == nil && ok {
			if parsed, ok := parseFloatSetting(value); ok {
				cfg.HourlyCap = parsed
			}
		}
		if value, ok, err := g.store.GetSetting(settingDailyBudgetCap); err == nil && ok {
			if parsed, ok := parseFloatSetting(value); ok {
				cfg.DailyCap = parsed
			}
		}
		if value, ok, err := g.store.GetSetting(settingCooldownSeconds); err == nil && ok {
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && parsed >= 0 {
				cfg.CooldownSeconds = float64(parsed)
			}
		}
	}

	g.config = cfg
	for mode, maxBudget := range g.config.ModeBudgets {
		if _, ok := g.currentBudget[mode]; !ok {
			g.currentBudget[mode] = maxBudget
		}
		if g.currentBudget[mode] > maxBudget {
			g.currentBudget[mode] = maxBudget
		}
		if _, ok := g.lastUpdate[mode]; !ok {
			g.lastUpdate[mode] = time.Now()
		}
	}
}

func applyInterventionBudget(budgets map[models.Mode]float64, value string) {
	factor := 1.0
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low":
		factor = 0.7
	case "high":
		factor = 1.3
	}
	for mode, budget := range budgets {
		budgets[mode] = budget * factor
	}
}

func parseFloatSetting(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func (g *Gateway) loadUsageLocked(now time.Time) {
	if g.store != nil && !g.usageLoaded {
		usage, err := g.store.GetBudgetUsage()
		if err == nil {
			g.dailyUsed = usage.DailyUsed
			g.hourlyUsed = usage.HourlyUsed
			g.dayBucket = usage.DailyDay
			g.hourBucket = usage.HourlyHour
			g.usageLoaded = true
		} else {
			g.logger.Warn("load budget usage failed", slog.Any("error", err))
		}
	}
	g.resetUsageBucketsLocked(now)
}

func (g *Gateway) resetUsageBucketsLocked(now time.Time) {
	currentDay := now.Format("2006-01-02")
	currentHour := now.Format("2006-01-02-15")
	changed := false

	if g.dayBucket != currentDay {
		g.dayBucket = currentDay
		g.dailyUsed = 0
		changed = true
	}
	if g.hourBucket != currentHour {
		g.hourBucket = currentHour
		g.hourlyUsed = 0
		changed = true
	}
	if changed {
		g.persistUsageLocked()
	}
}

func (g *Gateway) persistUsageLocked() {
	if g.store == nil {
		return
	}
	usage := models.BudgetUsage{
		DailyUsed:  g.dailyUsed,
		DailyDay:   g.dayBucket,
		HourlyUsed: g.hourlyUsed,
		HourlyHour: g.hourBucket,
	}
	if err := g.store.SetBudgetUsage(usage); err != nil {
		g.logger.Warn("persist budget usage failed", slog.Any("error", err))
	}
}

func (g *Gateway) modeMaxBudget(mode models.Mode) float64 {
	if budget, ok := g.config.ModeBudgets[mode]; ok {
		return budget
	}
	return g.config.ModeBudgets[models.ModeLight]
}

func (g *Gateway) Evaluate(ctx models.Context, action models.Action) (models.Action, models.GatewayDecision) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	g.refreshConfigLocked()
	g.loadUsageLocked(now)
	g.replenishBudgetLocked(ctx.Mode, now)

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
		cost := calculateCost(action)

		// Check Cooldown
		if g.config.CooldownSeconds > 0 && time.Since(g.lastIntervention).Seconds() < g.config.CooldownSeconds {
			g.logger.Info("gateway cooldown active",
				slog.Float64("since_last", time.Since(g.lastIntervention).Seconds()),
				slog.Float64("cooldown", g.config.CooldownSeconds))
			return overrideAction(original, models.GatewayOverride, ReasonCooldownActive)
		}

		// Check Budget Caps
		if g.config.HourlyCap > 0 && g.hourlyUsed+cost > g.config.HourlyCap {
			g.logger.Info("gateway hourly cap reached",
				slog.Float64("used", g.hourlyUsed),
				slog.Float64("cap", g.config.HourlyCap))
			return overrideAction(original, models.GatewayOverride, ReasonBudgetExhausted)
		}
		if g.config.DailyCap > 0 && g.dailyUsed+cost > g.config.DailyCap {
			g.logger.Info("gateway daily cap reached",
				slog.Float64("used", g.dailyUsed),
				slog.Float64("cap", g.config.DailyCap))
			return overrideAction(original, models.GatewayOverride, ReasonBudgetExhausted)
		}

		// Check Budget (per mode)
		if g.currentBudget[ctx.Mode] < cost {
			g.logger.Info("gateway budget exhausted",
				slog.Float64("current", g.currentBudget[ctx.Mode]),
				slog.Float64("cost", cost))
			return overrideAction(original, models.GatewayOverride, ReasonBudgetExhausted)
		}

		// Apply Cost
		g.currentBudget[ctx.Mode] -= cost
		g.lastIntervention = now
		g.hourlyUsed += cost
		g.dailyUsed += cost
		g.persistUsageLocked()
		g.logger.Info("gateway intervention allowed",
			slog.Float64("cost", cost),
			slog.Float64("remaining", g.currentBudget[ctx.Mode]))
	}

	return action, decision
}

func (g *Gateway) CanIntervene(ctx models.Context, cost float64) (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	g.refreshConfigLocked()
	g.loadUsageLocked(now)
	g.replenishBudgetLocked(ctx.Mode, now)

	if g.config.CooldownSeconds > 0 && time.Since(g.lastIntervention).Seconds() < g.config.CooldownSeconds {
		return false, ReasonCooldownActive
	}
	if g.config.HourlyCap > 0 && g.hourlyUsed+cost > g.config.HourlyCap {
		return false, ReasonBudgetExhausted
	}
	if g.config.DailyCap > 0 && g.dailyUsed+cost > g.config.DailyCap {
		return false, ReasonBudgetExhausted
	}
	if g.currentBudget[ctx.Mode] < cost {
		return false, ReasonBudgetExhausted
	}
	return true, "allow"
}

func MaxActionCost() float64 {
	return 3.0
}

func (g *Gateway) replenishBudgetLocked(mode models.Mode, now time.Time) {
	lastUpdate, ok := g.lastUpdate[mode]
	if !ok {
		g.lastUpdate[mode] = now
		g.currentBudget[mode] = g.modeMaxBudget(mode)
		return
	}
	elapsedMinutes := now.Sub(lastUpdate).Minutes()
	if elapsedMinutes <= 0 {
		return
	}

	recovered := elapsedMinutes * g.config.RecoveryRate
	g.currentBudget[mode] += recovered
	maxBudget := g.modeMaxBudget(mode)
	if g.currentBudget[mode] > maxBudget {
		g.currentBudget[mode] = maxBudget
	}
	g.lastUpdate[mode] = now
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
