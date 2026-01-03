package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"luma/core/internal/ai"
	"luma/core/internal/db"
	"luma/core/internal/focus"
	"luma/core/internal/gateway"
	"luma/core/internal/memory"
	"luma/core/internal/models"
)

const (
	settingQuietHours         = "quiet_hours"
	settingInterventionBudget = "intervention_budget"
	settingFocusMonitor       = "focus_monitor_enabled"
	settingOllamaModel        = "ollama_model"
	settingAgentEnabled       = "agent_enabled"
	settingRuleOnlyMode       = "rule_only_mode"
	settingBudgetSilent       = "budget_silent"
	settingBudgetLight        = "budget_light"
	settingBudgetActive       = "budget_active"
	settingDailyBudgetCap     = "daily_budget_cap"
	settingHourlyBudgetCap    = "hourly_budget_cap"
	settingCooldownSeconds    = "cooldown_seconds"
	settingLastAutoSuggestMs  = "last_auto_suggestion_ms"
)

var allowedSettings = map[string]bool{
	settingQuietHours:         true,
	settingInterventionBudget: true,
	settingFocusMonitor:       true,
	settingOllamaModel:        true,
	settingAgentEnabled:       true,
	settingRuleOnlyMode:       true,
	settingBudgetSilent:       true,
	settingBudgetLight:        true,
	settingBudgetActive:       true,
	settingDailyBudgetCap:     true,
	settingHourlyBudgetCap:    true,
	settingCooldownSeconds:    true,
}

const autoSuggestionWindow = 10 * time.Minute

type Handler struct {
	store   *db.Store
	ai      *ai.Client
	focus   *focus.Monitor
	memory  *memory.Service
	gateway *gateway.Gateway
	started time.Time
	logger  *slog.Logger
}

func NewHandler(store *db.Store, aiClient *ai.Client, focusMonitor *focus.Monitor, memoryService *memory.Service, started time.Time, logger *slog.Logger) *Handler {
	gw := gateway.New(logger, store)
	return &Handler{
		store:   store,
		ai:      aiClient,
		focus:   focusMonitor,
		memory:  memoryService,
		gateway: gw,
		started: started,
		logger:  logger,
	}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(corsMiddleware)
	r.Use(h.loggingMiddleware)
	r.Get("/v1/health", h.handleHealth)
	r.Post("/v1/decision", h.handleDecision)
	r.Post("/v1/feedback", h.handleFeedback)
	r.Post("/v1/memory/reset", h.handleMemoryReset)
	r.Get("/v1/logs", h.handleLogs)
	r.Get("/v1/focus/current", h.handleFocusCurrent)
	r.Get("/v1/focus/recent", h.handleFocusRecent)
	r.Get("/v1/export", h.handleExport)
	r.Get("/v1/ollama/models", h.handleOllamaModels)
	r.Get("/v1/settings", h.handleSettingsGet)
	r.Post("/v1/settings", h.handleSettingsPost)
	r.Get("/v1/profile", h.handleProfile)
	r.Get("/v1/learning/explanations", h.handleLearningExplanations)
	r.Get("/v1/state/history", h.handleStateHistory)
	return r
}

func (h *Handler) handleDecision(w http.ResponseWriter, r *http.Request) {
	var req models.DecisionRequest
	if err := decodeJSON(r, &req); err != nil {
		h.logger.Error("decode request failed", slog.Any("error", err))
		respondError(w, http.StatusBadRequest, "invalid json")
		return
	}

	// Log incoming request
	if reqJSON, err := json.Marshal(req); err == nil {
		h.logger.Info("📥 收到请求", slog.String("endpoint", "/v1/decision"), slog.String("body", string(reqJSON)))
	}
	if req.RequestID != "" {
		if _, err := uuid.Parse(req.RequestID); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request_id")
			return
		}
	}
	if req.Context.Timestamp == 0 {
		req.Context.Timestamp = time.Now().UnixMilli()
	}
	if req.Context.Signals == nil {
		req.Context.Signals = map[string]string{}
	}
	if err := validateContext(req.Context); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	requestID := req.RequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}

	// If user actively inputs text, clear cooldown to allow conversation
	if req.Context.UserText != "" {
		h.gateway.ClearCooldown()
		h.logger.Info("user text detected, cooldown cleared for conversation")
	}

	if err := enrichSignals(h.store, h.focus, &req.Context); err != nil {
		h.logger.Error("settings read failed", slog.String("request_id", requestID), slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "settings error")
		return
	}
	// Inject Memory
	req.Context.ProfileSummary = h.memory.GetProfileSummary()
	req.Context.MemorySummary = h.memory.GetRecentEvents(5)

	decisionSettings, err := loadDecisionSettings(h.store)
	if err != nil {
		h.logger.Error("settings read failed", slog.String("request_id", requestID), slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "settings error")
		return
	}
	if !decisionSettings.AgentEnabled || decisionSettings.RuleOnly {
		action := models.Action{
			ActionType: models.ActionDoNotDisturb,
			Message:    decisionSettings.disabledMessage(),
			Confidence: 1,
			Cost:       0,
			RiskLevel:  models.RiskLow,
		}
		h.respondWithAction(w, requestID, req.Context, action, decisionSettings.policyVersion(), "n/a", 0)
		return
	}

	quietHours := req.Context.Signals["quiet_hours"]
	if quietHours == "" {
		if value, ok, err := h.store.GetSetting(settingQuietHours); err == nil && ok {
			quietHours = value
		}
	}
	if quietHours != "" && withinQuietHours(time.Now(), quietHours) {
		action := models.Action{
			ActionType: models.ActionDoNotDisturb,
			Message:    "安静时段内，已暂停提示。",
			Confidence: 1,
			Cost:       0,
			RiskLevel:  models.RiskLow,
		}
		h.respondWithAction(w, requestID, req.Context, action, "quiet_hours", "n/a", 0)
		return
	}

	if req.Context.UserText == "" {
		allowed, reason, err := h.shouldAllowAutoSuggestion(req.Context)
		if err != nil {
			h.logger.Error("auto suggestion check failed", slog.String("request_id", requestID), slog.Any("error", err))
			respondError(w, http.StatusInternalServerError, "auto suggestion error")
			return
		}
		if !allowed {
			action := models.Action{
				ActionType: models.ActionDoNotDisturb,
				Message:    autoSuggestionMessage(reason),
				Confidence: 1,
				Cost:       0,
				RiskLevel:  models.RiskLow,
			}
			h.respondWithAction(w, requestID, req.Context, action, "auto_guard", "n/a", 0)
			return
		}
	}

	start := time.Now()
	rawAction, policyVersion, modelVersion, err := h.ai.Decide(req.Context, requestID)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		h.logger.Error("ai decide failed", slog.String("request_id", requestID), slog.Any("error", err))
		respondError(w, http.StatusBadGateway, "ai service unavailable")
		return
	}

	finalAction, gatewayDecision := h.gateway.Evaluate(req.Context, rawAction)
	createdAt := time.Now()

	resp := models.DecisionResponse{
		RequestID:       requestID,
		Context:         req.Context,
		Action:          finalAction,
		PolicyVersion:   policyVersion,
		ModelVersion:    modelVersion,
		LatencyMs:       latency,
		CreatedAt:       createdAt,
		CreatedAtMs:     createdAt.UnixMilli(),
		GatewayDecision: gatewayDecision,
	}

	logEntry := models.DecisionLogEntry{
		RequestID:       requestID,
		Context:         req.Context,
		RawAction:       rawAction,
		FinalAction:     finalAction,
		GatewayDecision: gatewayDecision,
		PolicyVersion:   policyVersion,
		ModelVersion:    modelVersion,
		LatencyMs:       latency,
		CreatedAt:       createdAt,
		CreatedAtMs:     createdAt.UnixMilli(),
	}

	if err := h.store.InsertDecision(logEntry); err != nil {
		h.logger.Error("insert decision failed", slog.String("request_id", requestID), slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}

	h.logger.Info(
		"decision",
		slog.String("request_id", requestID),
		slog.Int64("latency_ms", latency),
		slog.String("policy_version", policyVersion),
		slog.String("model_version", modelVersion),
		slog.String("gateway_decision", string(gatewayDecision.Decision)),
	)

	// Log outgoing response
	if respJSON, err := json.Marshal(resp); err == nil {
		h.logger.Info("📤 发送响应", slog.String("request_id", requestID), slog.String("body", string(respJSON)))
	}

	respondJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var req models.FeedbackRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateFeedback(req); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	exists, err := h.store.DecisionExists(req.RequestID)
	if err != nil {
		h.logger.Error("check request_id failed", slog.String("request_id", req.RequestID), slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	if !exists {
		respondError(w, http.StatusNotFound, "request_id not found")
		return
	}

	feedbackValue := string(req.Feedback)
	if req.FeedbackText != "" {
		feedbackValue = string(req.Feedback) + ": " + req.FeedbackText
	}

	if err := h.store.RecordFeedback(req.RequestID, feedbackValue); err != nil {
		h.logger.Error("record feedback failed", slog.String("request_id", req.RequestID), slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	if isImplicitFeedback(req.Feedback) {
		if err := h.store.RecordImplicitFeedback(req.RequestID, string(req.Feedback), req.FeedbackText); err != nil {
			h.logger.Error("record implicit feedback failed", slog.String("request_id", req.RequestID), slog.Any("error", err))
		}
	}
	if err := h.ai.Feedback(req.RequestID, feedbackValue); err != nil {
		h.logger.Error("forward feedback failed", slog.String("request_id", req.RequestID), slog.Any("error", err))
	}

	// Update Memory
	if err := h.memory.ProcessFeedback(req.RequestID, feedbackValue); err != nil {
		h.logger.Error("process feedback failed", slog.String("request_id", req.RequestID), slog.Any("error", err))
	}

	// Clear gateway cooldown to allow continued interaction after user feedback
	h.gateway.ClearCooldown()

	h.logger.Info("feedback recorded",
		slog.String("request_id", req.RequestID),
		slog.String("type", string(req.Feedback)),
		slog.String("text", req.FeedbackText),
	)

	// If feedback has text, generate AI response for conversation
	if req.FeedbackText != "" && req.Context.Mode != "" {
		// Use feedback text as user input for new decision
		req.Context.UserText = req.FeedbackText
		if req.Context.Timestamp == 0 {
			req.Context.Timestamp = time.Now().UnixMilli()
		}
		if req.Context.Signals == nil {
			req.Context.Signals = map[string]string{}
		}

		// Enrich context
		if err := enrichSignals(h.store, h.focus, &req.Context); err != nil {
			h.logger.Warn("failed to enrich signals for reply", slog.Any("error", err))
		}
		req.Context.ProfileSummary = h.memory.GetProfileSummary()
		req.Context.MemorySummary = h.memory.GetRecentEvents(5)

		// Generate reply
		newRequestID := uuid.NewString()
		start := time.Now()
		rawAction, policyVersion, modelVersion, err := h.ai.Decide(req.Context, newRequestID)
		latency := time.Since(start).Milliseconds()

		if err != nil {
			h.logger.Error("failed to generate reply", slog.String("request_id", newRequestID), slog.Any("error", err))
			respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}

		finalAction, gatewayDecision := h.gateway.Evaluate(req.Context, rawAction)
		createdAt := time.Now()

		resp := models.DecisionResponse{
			RequestID:       newRequestID,
			Context:         req.Context,
			Action:          finalAction,
			PolicyVersion:   policyVersion,
			ModelVersion:    modelVersion,
			LatencyMs:       latency,
			CreatedAt:       createdAt,
			CreatedAtMs:     createdAt.UnixMilli(),
			GatewayDecision: gatewayDecision,
		}

		logEntry := models.DecisionLogEntry{
			RequestID:       newRequestID,
			Context:         req.Context,
			RawAction:       rawAction,
			FinalAction:     finalAction,
			GatewayDecision: gatewayDecision,
			PolicyVersion:   policyVersion,
			ModelVersion:    modelVersion,
			LatencyMs:       latency,
			CreatedAt:       createdAt,
			CreatedAtMs:     createdAt.UnixMilli(),
		}

		if err := h.store.InsertDecision(logEntry); err != nil {
			h.logger.Error("insert reply decision failed", slog.String("request_id", newRequestID), slog.Any("error", err))
		}

		h.logger.Info("reply generated",
			slog.String("reply_request_id", newRequestID),
			slog.Int64("latency_ms", latency),
		)

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"reply":  resp,
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := parseInt(l); err == nil {
			limit = parsed
		}
	}
	var sinceMs int64
	if s := r.URL.Query().Get("since_ms"); s != "" {
		if parsed, err := parseInt64(s); err == nil {
			sinceMs = parsed
		}
	}
	var untilMs int64
	if s := r.URL.Query().Get("until_ms"); s != "" {
		if parsed, err := parseInt64(s); err == nil {
			untilMs = parsed
		}
	}
	aggregate := r.URL.Query().Get("aggregate")
	logs, err := h.store.ListLogsRange(limit, sinceMs, untilMs)
	if err != nil {
		h.logger.Error("list logs failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	if aggregate == "1" || strings.EqualFold(aggregate, "true") {
		respondJSON(w, http.StatusOK, map[string]any{
			"logs":      logs,
			"aggregate": aggregateLogs(logs),
		})
		return
	}
	respondJSON(w, http.StatusOK, logs)
}

func (h *Handler) handleFocusCurrent(w http.ResponseWriter, r *http.Request) {
	if h.focus == nil || !h.focus.Enabled() {
		respondJSON(w, http.StatusOK, models.FocusCurrent{})
		return
	}
	current, ok, err := h.focus.Current()
	if err != nil {
		h.logger.Error("focus current failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "focus error")
		return
	}
	if !ok {
		respondJSON(w, http.StatusOK, models.FocusCurrent{})
		return
	}
	respondJSON(w, http.StatusOK, current)
}

func (h *Handler) handleFocusRecent(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := parseInt(l); err == nil {
			limit = parsed
		}
	}
	events, err := h.store.ListFocusEvents(limit)
	if err != nil {
		h.logger.Error("focus recent failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	respondJSON(w, http.StatusOK, events)
}

func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request) {
	limit := 1000
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := parseInt(l); err == nil {
			limit = parsed
		}
	}
	var sinceMs int64
	if s := r.URL.Query().Get("since_ms"); s != "" {
		if parsed, err := parseInt64(s); err == nil {
			sinceMs = parsed
		}
	}

	records, err := h.store.ExportRecords(limit, sinceMs)
	if err != nil {
		h.logger.Error("export logs failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	writer := bufio.NewWriter(w)
	encoder := json.NewEncoder(writer)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			h.logger.Error("export encode failed", slog.Any("error", err))
			break
		}
	}
	_ = writer.Flush()
}

func (h *Handler) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.ListSettings()
	if err != nil {
		h.logger.Error("list settings failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	respondJSON(w, http.StatusOK, settings)
}

func (h *Handler) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	var req models.SettingRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		respondError(w, http.StatusBadRequest, "key required")
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		respondError(w, http.StatusBadRequest, "value required")
		return
	}
	if !allowedSettings[req.Key] {
		respondError(w, http.StatusBadRequest, "unsupported setting key")
		return
	}
	normalizedValue, err := normalizeSettingValue(req.Key, req.Value)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Value = normalizedValue

	if err := h.store.UpsertSetting(req.Key, req.Value); err != nil {
		h.logger.Error("update setting failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	if req.Key == settingFocusMonitor && h.focus != nil {
		enabled := req.Value == "true"
		if err := h.focus.SetEnabled(enabled); err != nil && !errors.Is(err, focus.ErrUnsupported) {
			h.logger.Error("focus toggle failed", slog.Any("error", err))
		}
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	respondJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"started_at": h.started.Format(time.RFC3339Nano),
		"uptime_ms":  now.Sub(h.started).Milliseconds(),
	})
}

func (h *Handler) handleMemoryReset(w http.ResponseWriter, _ *http.Request) {
	if err := h.memory.Reset(); err != nil {
		h.logger.Error("memory reset failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "memory reset failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleProfile(w http.ResponseWriter, _ *http.Request) {
	profiles, err := h.memory.ListProfiles()
	if err != nil {
		h.logger.Error("list profiles failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "profiles error")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"summary":  h.memory.GetProfileSummary(),
		"profiles": profiles,
	})
}

func (h *Handler) handleLearningExplanations(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := parseInt(l); err == nil {
			limit = parsed
		}
	}
	profiles, err := h.memory.ListProfiles()
	if err != nil {
		h.logger.Error("list profiles failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "profiles error")
		return
	}
	events, err := h.memory.ListEvents(limit)
	if err != nil {
		h.logger.Error("list memory events failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "memory events error")
		return
	}
	explanations := buildLearningExplanations(profiles)
	respondJSON(w, http.StatusOK, map[string]any{
		"summary":      h.memory.GetProfileSummary(),
		"explanations": explanations,
		"profiles":     profiles,
		"events":       events,
	})
}

func (h *Handler) handleStateHistory(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := parseInt(l); err == nil {
			limit = parsed
		}
	}
	var sinceMs int64
	if s := r.URL.Query().Get("since_ms"); s != "" {
		if parsed, err := parseInt64(s); err == nil {
			sinceMs = parsed
		}
	}
	var untilMs int64
	if s := r.URL.Query().Get("until_ms"); s != "" {
		if parsed, err := parseInt64(s); err == nil {
			untilMs = parsed
		}
	}
	snapshots, err := h.store.ListFocusStateSnapshots(limit, sinceMs, untilMs)
	if err != nil {
		h.logger.Error("list state history failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "state history error")
		return
	}
	respondJSON(w, http.StatusOK, snapshots)
}

func (h *Handler) handleOllamaModels(w http.ResponseWriter, r *http.Request) {
	tagsURL := ollamaTagsURL()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, tagsURL, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "ollama request error")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		respondError(w, http.StatusBadGateway, "ollama unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respondError(w, http.StatusBadGateway, "ollama unavailable")
		return
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadGateway, "ollama invalid response")
		return
	}

	models := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		models = append(models, name)
	}
	respondJSON(w, http.StatusOK, map[string]any{"models": models})
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return
	}
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) respondWithAction(w http.ResponseWriter, requestID string, ctx models.Context, rawAction models.Action, policyVersion string, modelVersion string, latency int64) {
	finalAction, gatewayDecision := h.gateway.Evaluate(ctx, rawAction)
	createdAt := time.Now()
	resp := models.DecisionResponse{
		RequestID:       requestID,
		Context:         ctx,
		Action:          finalAction,
		PolicyVersion:   policyVersion,
		ModelVersion:    modelVersion,
		LatencyMs:       latency,
		CreatedAt:       createdAt,
		CreatedAtMs:     createdAt.UnixMilli(),
		GatewayDecision: gatewayDecision,
	}
	logEntry := models.DecisionLogEntry{
		RequestID:       requestID,
		Context:         ctx,
		RawAction:       rawAction,
		FinalAction:     finalAction,
		GatewayDecision: gatewayDecision,
		PolicyVersion:   policyVersion,
		ModelVersion:    modelVersion,
		LatencyMs:       latency,
		CreatedAt:       createdAt,
		CreatedAtMs:     createdAt.UnixMilli(),
	}
	if err := h.store.InsertDecision(logEntry); err != nil {
		h.logger.Error("insert decision failed", slog.String("request_id", requestID), slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func parseInt(val string) (int, error) {
	return strconv.Atoi(val)
}

func parseInt64(val string) (int64, error) {
	return strconv.ParseInt(val, 10, 64)
}

type logAggregateBucket struct {
	Bucket     string         `json:"bucket"`
	Total      int            `json:"total"`
	ByAction   map[string]int `json:"by_action"`
	ByDecision map[string]int `json:"by_decision"`
}

func aggregateLogs(logs []models.EventLog) []logAggregateBucket {
	buckets := map[string]*logAggregateBucket{}
	for _, entry := range logs {
		ts := entry.CreatedAtMs
		if ts == 0 {
			ts = entry.CreatedAt.UnixMilli()
		}
		bucketKey := time.UnixMilli(ts).Format("2006-01-02 15:00")
		bucket := buckets[bucketKey]
		if bucket == nil {
			bucket = &logAggregateBucket{
				Bucket:     bucketKey,
				ByAction:   map[string]int{},
				ByDecision: map[string]int{},
			}
			buckets[bucketKey] = bucket
		}
		bucket.Total++
		if entry.Action.ActionType != "" {
			bucket.ByAction[string(entry.Action.ActionType)]++
		}
		if entry.GatewayDecision.Decision != "" {
			bucket.ByDecision[string(entry.GatewayDecision.Decision)]++
		}
	}

	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]logAggregateBucket, 0, len(keys))
	for _, key := range keys {
		result = append(result, *buckets[key])
	}
	return result
}

func ollamaTagsURL() string {
	raw := os.Getenv("OLLAMA_URL")
	if raw == "" {
		return "http://localhost:11434/api/tags"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "http://localhost:11434/api/tags"
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if strings.HasSuffix(path, "/api/tags") {
		return parsed.String()
	}
	if strings.HasSuffix(path, "/api/generate") {
		path = strings.TrimSuffix(path, "/generate")
	} else if !strings.HasSuffix(path, "/api") {
		path = path + "/api"
	}
	parsed.Path = path + "/tags"
	return parsed.String()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.logger.Info("📥 收到HTTP请求",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote", r.RemoteAddr),
		)
		next.ServeHTTP(w, r)
		h.logger.Info("✅ 请求处理完成",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

func decodeJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

func validateContext(ctx models.Context) error {
	validModes := map[models.Mode]bool{
		models.ModeSilent: true,
		models.ModeLight:  true,
		models.ModeActive: true,
	}
	// user_text is optional - empty string means auto-suggestion request
	if !validModes[ctx.Mode] {
		return fmt.Errorf("invalid mode")
	}
	if ctx.Timestamp < 1_000_000_000_000 || ctx.Timestamp > 10_000_000_000_000 {
		return fmt.Errorf("timestamp must be milliseconds")
	}
	for key, value := range ctx.Signals {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("signals key required")
		}
		if value == "" {
			continue
		}
	}
	return nil
}

func validateFeedback(req models.FeedbackRequest) error {
	if req.RequestID == "" {
		return fmt.Errorf("request_id required")
	}
	if _, err := uuid.Parse(req.RequestID); err != nil {
		return fmt.Errorf("invalid request_id")
	}
	valid := map[models.FeedbackType]bool{
		models.FeedbackLike:    true,
		models.FeedbackDislike: true,
		models.FeedbackAdopted: true,
		models.FeedbackIgnored: true,
		models.FeedbackClosed:  true,
		models.FeedbackOpen:    true,
	}
	if !valid[req.Feedback] {
		return fmt.Errorf("invalid feedback")
	}
	return nil
}

func enrichSignals(store *db.Store, focusMonitor *focus.Monitor, payload *models.Context) error {
	payload.Signals["hour_of_day"] = strconv.Itoa(time.Now().Hour())
	if _, ok := payload.Signals["session_minutes"]; !ok {
		payload.Signals["session_minutes"] = "0"
	}

	quietHours, ok, err := store.GetSetting(settingQuietHours)
	if err != nil {
		return err
	}
	if ok && quietHours != "" {
		payload.Signals["quiet_hours"] = quietHours
	}

	budgetSetting, ok, err := store.GetSetting(settingInterventionBudget)
	if err != nil {
		return err
	}
	if ok {
		budgetValue := normalizeBudget(budgetSetting)
		if budgetValue != "" {
			payload.Signals["intervention_budget"] = budgetValue
		}
	}

	modelSetting, ok, err := store.GetSetting(settingOllamaModel)
	if err != nil {
		return err
	}
	if ok && modelSetting != "" {
		payload.Signals["ollama_model"] = modelSetting
	}

	if focusMonitor != nil && focusMonitor.Enabled() {
		switchCount := focusMonitor.SwitchCount()
		payload.SwitchCount = switchCount
		payload.Signals["switch_count"] = strconv.Itoa(switchCount)
		noProgress, noProgressDuration := focusMonitor.NoProgress()
		if noProgress {
			payload.Signals["no_progress_minutes"] = fmt.Sprintf("%.1f", noProgressDuration.Minutes())
		}

		current, ok, err := focusMonitor.Current()
		if err != nil {
			return nil
		}
		if ok {
			if _, exists := payload.Signals["focus_app"]; !exists {
				payload.Signals["focus_app"] = current.AppName
			}
			if _, exists := payload.Signals["focus_bundle_id"]; !exists {
				payload.Signals["focus_bundle_id"] = current.BundleID
			}
			if _, exists := payload.Signals["focus_window_title"]; !exists {
				payload.Signals["focus_window_title"] = current.WindowTitle
			}
			if _, exists := payload.Signals["focus_minutes"]; !exists {
				payload.Signals["focus_minutes"] = fmt.Sprintf("%.1f", current.FocusMinutes)
			}
			focusState := deriveFocusState(current.FocusMinutes, switchCount, noProgress, noProgressDuration)
			payload.FocusState = focusState
			payload.Signals["focus_state"] = focusState
			_ = store.InsertFocusStateSnapshot(models.FocusStateSnapshot{
				TsMs:         time.Now().UnixMilli(),
				FocusState:   focusState,
				SwitchCount:  switchCount,
				NoProgressMs: noProgressDuration.Milliseconds(),
				FocusMinutes: current.FocusMinutes,
				AppName:      current.AppName,
				WindowTitle:  current.WindowTitle,
			})
		}
	} else {
		if metrics, err := store.FocusMetrics(int64((10 * time.Minute).Milliseconds())); err == nil {
			payload.SwitchCount = metrics.SwitchCount
			payload.Signals["switch_count"] = strconv.Itoa(metrics.SwitchCount)
			payload.Signals["focus_minutes_window"] = fmt.Sprintf("%.1f", metrics.FocusMinutes)
			focusState := deriveFocusState(metrics.FocusMinutes, metrics.SwitchCount, false, 0)
			payload.FocusState = focusState
			payload.Signals["focus_state"] = focusState
		}
	}
	return nil
}

func normalizeBudget(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low":
		return "1"
	case "medium":
		return "2"
	case "high":
		return "3"
	default:
		return ""
	}
}

func normalizeSettingValue(key, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	switch key {
	case settingInterventionBudget:
		normalized := strings.ToLower(trimmed)
		if normalized == "low" || normalized == "medium" || normalized == "high" {
			return normalized, nil
		}
		return "", fmt.Errorf("invalid intervention_budget")
	case settingQuietHours:
		if isValidQuietHours(trimmed) {
			return trimmed, nil
		}
		return "", fmt.Errorf("invalid quiet_hours")
	case settingAgentEnabled, settingRuleOnlyMode:
		switch strings.ToLower(trimmed) {
		case "true", "false":
			return strings.ToLower(trimmed), nil
		default:
			return "", fmt.Errorf("invalid %s", key)
		}
	case settingFocusMonitor:
		switch strings.ToLower(trimmed) {
		case "true", "false":
			return strings.ToLower(trimmed), nil
		default:
			return "", fmt.Errorf("invalid focus_monitor_enabled")
		}
	case settingOllamaModel:
		if trimmed == "" {
			return "", fmt.Errorf("invalid ollama_model")
		}
		return trimmed, nil
	case settingBudgetSilent, settingBudgetLight, settingBudgetActive, settingDailyBudgetCap, settingHourlyBudgetCap:
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || parsed < 0 {
			return "", fmt.Errorf("invalid %s", key)
		}
		return trimmed, nil
	case settingCooldownSeconds:
		parsed, err := strconv.Atoi(trimmed)
		if err != nil || parsed < 0 {
			return "", fmt.Errorf("invalid cooldown_seconds")
		}
		return strconv.Itoa(parsed), nil
	default:
		return trimmed, nil
	}
}

func isValidQuietHours(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if _, err := time.Parse("15:04", strings.TrimSpace(part)); err != nil {
			return false
		}
	}
	return true
}

func withinQuietHours(now time.Time, quietHours string) bool {
	parts := strings.Split(quietHours, "-")
	if len(parts) != 2 {
		return false
	}
	start, err := time.Parse("15:04", strings.TrimSpace(parts[0]))
	if err != nil {
		return false
	}
	end, err := time.Parse("15:04", strings.TrimSpace(parts[1]))
	if err != nil {
		return false
	}
	nowMinutes := now.Hour()*60 + now.Minute()
	startMinutes := start.Hour()*60 + start.Minute()
	endMinutes := end.Hour()*60 + end.Minute()
	if startMinutes == endMinutes {
		return false
	}
	if startMinutes < endMinutes {
		return nowMinutes >= startMinutes && nowMinutes < endMinutes
	}
	return nowMinutes >= startMinutes || nowMinutes < endMinutes
}

func (h *Handler) shouldAllowAutoSuggestion(ctx models.Context) (bool, string, error) {
	now := time.Now()
	lastRaw, ok, err := h.store.GetSetting(settingLastAutoSuggestMs)
	if err != nil {
		return false, "", err
	}
	if ok && lastRaw != "" {
		if lastMs, err := strconv.ParseInt(lastRaw, 10, 64); err == nil {
			if now.UnixMilli()-lastMs < autoSuggestionWindow.Milliseconds() {
				return false, "auto_window", nil
			}
		}
	}
	allowed, reason := h.gateway.CanIntervene(ctx, gateway.MaxActionCost())
	if !allowed {
		return false, reason, nil
	}
	if err := h.store.UpsertSetting(settingLastAutoSuggestMs, strconv.FormatInt(now.UnixMilli(), 10)); err != nil {
		return false, "", err
	}
	return true, "allow", nil
}

func autoSuggestionMessage(reason string) string {
	switch reason {
	case "auto_window":
		return "自动提示冷却中。"
	case gateway.ReasonCooldownActive:
		return "处于冷却期，已暂停自动提示。"
	case gateway.ReasonBudgetExhausted:
		return "干预预算不足，已暂停自动提示。"
	default:
		return "当前不生成自动提示。"
	}
}

func isImplicitFeedback(feedback models.FeedbackType) bool {
	switch feedback {
	case models.FeedbackIgnored, models.FeedbackClosed, models.FeedbackOpen:
		return true
	default:
		return false
	}
}

func deriveFocusState(focusMinutes float64, switchCount int, noProgress bool, noProgressDuration time.Duration) string {
	if noProgress && noProgressDuration >= 20*time.Minute {
		return "NO_PROGRESS"
	}
	if switchCount >= 8 {
		return "DISTRACTED"
	}
	if focusMinutes >= 25 {
		return "FOCUSED"
	}
	return "LIGHT"
}

func buildLearningExplanations(profiles []memory.Profile) []string {
	explanations := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Confidence < 0.4 {
			continue
		}
		key := strings.TrimSpace(profile.Key)
		value := strings.TrimSpace(profile.Value)
		switch {
		case key == "preferred_intervention_budget":
			explanations = append(explanations, fmt.Sprintf("提示频率偏好: %s", value))
		case key == "tolerance_night_intervention":
			explanations = append(explanations, fmt.Sprintf("夜间提示容忍度: %s", value))
		case strings.HasPrefix(key, "accepts_action_"):
			action := strings.TrimPrefix(key, "accepts_action_")
			actionLabel := describeActionType(action)
			explanations = append(explanations, fmt.Sprintf("对%s的接受度: %s", actionLabel, value))
		default:
			explanations = append(explanations, fmt.Sprintf("%s: %s", key, value))
		}
	}
	return explanations
}

func describeActionType(raw string) string {
	switch strings.ToUpper(raw) {
	case "REST_REMINDER":
		return "休息提醒"
	case "ENCOURAGE":
		return "鼓励"
	case "TASK_BREAKDOWN":
		return "任务拆解"
	case "REFRAME":
		return "换个角度"
	case "DO_NOT_DISTURB":
		return "勿扰"
	default:
		return raw
	}
}

type decisionSettings struct {
	AgentEnabled bool
	RuleOnly     bool
}

func loadDecisionSettings(store *db.Store) (decisionSettings, error) {
	settings := decisionSettings{
		AgentEnabled: true,
		RuleOnly:     false,
	}
	if value, ok, err := store.GetSetting(settingAgentEnabled); err != nil {
		return settings, err
	} else if ok {
		settings.AgentEnabled = value == "true"
	}
	if value, ok, err := store.GetSetting(settingRuleOnlyMode); err != nil {
		return settings, err
	} else if ok {
		settings.RuleOnly = value == "true"
	}
	return settings, nil
}

func (s decisionSettings) policyVersion() string {
	if !s.AgentEnabled {
		return "agent_disabled"
	}
	if s.RuleOnly {
		return "rule_only"
	}
	return "policy_v0"
}

func (s decisionSettings) disabledMessage() string {
	if !s.AgentEnabled {
		return "Agent 已关闭，当前不生成提示。"
	}
	if s.RuleOnly {
		return "规则模式已开启，已暂停 AI 提示。"
	}
	return "当前不生成提示。"
}
