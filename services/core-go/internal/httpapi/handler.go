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
)

var allowedSettings = map[string]bool{
	settingQuietHours:         true,
	settingInterventionBudget: true,
	settingFocusMonitor:       true,
	settingOllamaModel:        true,
}

// TODO: Add settings for agent_enabled, per-mode budgets, daily/hourly caps, and rule-only mode.

type Handler struct {
	store   *db.Store
	ai      *ai.Client
	focus   *focus.Monitor
	memory  *memory.Service
	gateway *gateway.Gateway
	logger  *slog.Logger
}

func NewHandler(store *db.Store, aiClient *ai.Client, focusMonitor *focus.Monitor, memoryService *memory.Service, logger *slog.Logger) *Handler {
	gw := gateway.New(logger)
	return &Handler{store: store, ai: aiClient, focus: focusMonitor, memory: memoryService, gateway: gw, logger: logger}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(corsMiddleware)
	r.Use(h.loggingMiddleware)
	r.Post("/v1/decision", h.handleDecision)
	r.Post("/v1/feedback", h.handleFeedback)
	r.Get("/v1/logs", h.handleLogs)
	r.Get("/v1/focus/current", h.handleFocusCurrent)
	r.Get("/v1/focus/recent", h.handleFocusRecent)
	r.Get("/v1/export", h.handleExport)
	r.Get("/v1/ollama/models", h.handleOllamaModels)
	r.Get("/v1/settings", h.handleSettingsGet)
	r.Post("/v1/settings", h.handleSettingsPost)
	// TODO: Add endpoints for profile summary, learning explanations, and state history.
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
	// TODO: If agent is disabled or within quiet hours, return DO_NOT_DISTURB without calling AI.
	// TODO: For auto-suggestions (empty user_text), enforce max-1-per-window and budget checks pre-AI.

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
	// TODO: Accept implicit feedback types (IGNORED/CLOSED/OPEN_PANEL) and persist them.
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
	// TODO: Add time-range filters and aggregated history for UI timelines.
	logs, err := h.store.ListLogs(limit)
	if err != nil {
		h.logger.Error("list logs failed", slog.Any("error", err))
		respondError(w, http.StatusInternalServerError, "db error")
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

func parseInt(val string) (int, error) {
	return strconv.Atoi(val)
}

func parseInt64(val string) (int64, error) {
	return strconv.ParseInt(val, 10, 64)
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
	// TODO: Compute app switch counts in a rolling window and derive rule-based focus state.

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
