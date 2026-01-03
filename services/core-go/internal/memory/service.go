package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"luma/core/internal/models"
)

type Service struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewService(db *sql.DB, logger *slog.Logger) *Service {
	return &Service{
		db:     db,
		logger: logger,
	}
}

// Profile represents a user preference or trait
type Profile struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	UpdatedAt  int64   `json:"updated_at_ms"`
}

type MemoryEvent struct {
	EventType   string  `json:"event_type"`
	Summary     string  `json:"summary"`
	CreatedAtMs int64   `json:"created_at_ms"`
	Importance  float64 `json:"importance"`
}

// GetProfileSummary returns a natural language summary of user profiles
func (s *Service) GetProfileSummary() string {
	rows, err := s.db.Query("SELECT key, value, confidence, updated_at_ms FROM profiles")
	if err != nil {
		s.logger.Error("failed to query profiles", slog.Any("error", err))
		return ""
	}
	defer rows.Close()

	var summaries []string
	for rows.Next() {
		var key, value string
		var confidence float64
		var updatedAtMs int64
		if err := rows.Scan(&key, &value, &confidence, &updatedAtMs); err != nil {
			continue
		}
		effectiveConfidence := decayConfidence(confidence, updatedAtMs)
		if effectiveConfidence < 0.5 {
			continue
		}
		// Simple formatting, can be enhanced later
		summaries = append(summaries, fmt.Sprintf("- %s: %s", key, value))
	}

	if len(summaries) == 0 {
		return ""
	}
	return strings.Join(summaries, "\n")
}

func decayConfidence(confidence float64, updatedAtMs int64) float64 {
	if updatedAtMs <= 0 {
		return confidence
	}
	ageMs := time.Now().UnixMilli() - updatedAtMs
	if ageMs <= 0 {
		return confidence
	}
	const halfLifeDays = 21.0
	ageDays := float64(ageMs) / (24 * 60 * 60 * 1000)
	decay := math.Pow(0.5, ageDays/halfLifeDays)
	return confidence * decay
}

// GetRecentEvents returns recent memory events as strings
func (s *Service) GetRecentEvents(limit int) string {
	rows, err := s.db.Query("SELECT summary FROM memory_events ORDER BY created_at_ms DESC LIMIT ?", limit)
	if err != nil {
		s.logger.Error("failed to query events", slog.Any("error", err))
		return ""
	}
	defer rows.Close()

	var events []string
	for rows.Next() {
		var summary string
		if err := rows.Scan(&summary); err != nil {
			continue
		}
		events = append(events, fmt.Sprintf("- %s", summary))
	}

	if len(events) == 0 {
		return ""
	}
	return strings.Join(events, "\n")
}

// AddEvent adds a new memory event
func (s *Service) AddEvent(eventType, summary string, importance float64) error {
	_, err := s.db.Exec(
		"INSERT INTO memory_events (event_type, summary, created_at_ms, importance) VALUES (?, ?, ?, ?)",
		eventType, summary, time.Now().UnixMilli(), importance,
	)
	return err
}

// SetProfile updates or inserts a profile
func (s *Service) SetProfile(key, value string, confidence float64) error {
	_, err := s.db.Exec(
		`INSERT INTO profiles (key, value, confidence, updated_at_ms) 
		 VALUES (?, ?, ?, ?) 
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, confidence=excluded.confidence, updated_at_ms=excluded.updated_at_ms`,
		key, value, confidence, time.Now().UnixMilli(),
	)
	return err
}

func (s *Service) ListProfiles() ([]Profile, error) {
	rows, err := s.db.Query("SELECT key, value, confidence, updated_at_ms FROM profiles ORDER BY updated_at_ms DESC")
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		var profile Profile
		if err := rows.Scan(&profile.Key, &profile.Value, &profile.Confidence, &profile.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("profile rows: %w", err)
	}
	return profiles, nil
}

func (s *Service) ListEvents(limit int) ([]MemoryEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		"SELECT event_type, summary, created_at_ms, importance FROM memory_events ORDER BY created_at_ms DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list memory events: %w", err)
	}
	defer rows.Close()

	var events []MemoryEvent
	for rows.Next() {
		var event MemoryEvent
		if err := rows.Scan(&event.EventType, &event.Summary, &event.CreatedAtMs, &event.Importance); err != nil {
			return nil, fmt.Errorf("scan memory event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory event rows: %w", err)
	}
	return events, nil
}

func (s *Service) Reset() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reset: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM profiles"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear profiles: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM memory_events"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear memory_events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}
	return nil
}

// ProcessFeedback analyzes user feedback and updates memory
func (s *Service) ProcessFeedback(requestID, feedback string) error {
	// 1. Get the original action from event_logs
	var finalActionJSON string
	var contextJSON string
	err := s.db.QueryRow("SELECT final_action_json, context_json FROM event_logs WHERE request_id = ?", requestID).Scan(&finalActionJSON, &contextJSON)
	if err != nil {
		return fmt.Errorf("find event log: %w", err)
	}

	actionType := "UNKNOWN"
	var action models.Action
	if err := json.Unmarshal([]byte(finalActionJSON), &action); err == nil && action.ActionType != "" {
		actionType = string(action.ActionType)
	} else {
		if strings.Contains(finalActionJSON, "DO_NOT_DISTURB") {
			actionType = "DO_NOT_DISTURB"
		} else if strings.Contains(finalActionJSON, "ENCOURAGE") {
			actionType = "ENCOURAGE"
		} else if strings.Contains(finalActionJSON, "TASK_BREAKDOWN") {
			actionType = "TASK_BREAKDOWN"
		} else if strings.Contains(finalActionJSON, "REST_REMINDER") {
			actionType = "REST_REMINDER"
		} else if strings.Contains(finalActionJSON, "REFRAME") {
			actionType = "REFRAME"
		}
	}

	feedbackType, feedbackText := normalizeFeedback(feedback)
	positive := feedbackType == "LIKE" || feedbackType == "ADOPTED" || feedbackType == "OPEN_PANEL"
	negative := feedbackType == "DISLIKE" || feedbackType == "IGNORED" || feedbackType == "CLOSED"

	// 3. Create a memory event
	eventType := "feedback"
	if feedbackType == "IGNORED" || feedbackType == "CLOSED" || feedbackType == "OPEN_PANEL" {
		eventType = "implicit_feedback"
	}
	summary := fmt.Sprintf("Feedback '%s' for action '%s'", feedbackType, actionType)
	if feedbackText != "" {
		summary = summary + ": " + feedbackText
	}

	// 4. Update profiles for acceptance and frequency
	if actionType != "UNKNOWN" && actionType != "DO_NOT_DISTURB" {
		if negative {
			_ = s.SetProfile("accepts_action_"+strings.ToLower(actionType), "false", 0.7)
		} else if positive {
			_ = s.SetProfile("accepts_action_"+strings.ToLower(actionType), "true", 0.6)
		}
	}
	if negative {
		_ = s.SetProfile("preferred_intervention_budget", "low", 0.6)
	} else if positive {
		_ = s.SetProfile("preferred_intervention_budget", "high", 0.5)
	}

	// 5. Learn time-of-day tolerance if we have context timestamp
	var ctx models.Context
	if err := json.Unmarshal([]byte(contextJSON), &ctx); err == nil && ctx.Timestamp > 0 {
		hour := time.UnixMilli(ctx.Timestamp).Hour()
		if hour >= 22 || hour < 7 {
			if negative {
				_ = s.SetProfile("tolerance_night_intervention", "low", 0.7)
			} else if positive {
				_ = s.SetProfile("tolerance_night_intervention", "high", 0.5)
			}
		}
	}

	return s.AddEvent(eventType, summary, 0.5)
}

func normalizeFeedback(raw string) (string, string) {
	parts := strings.SplitN(raw, ":", 2)
	feedbackType := strings.ToUpper(strings.TrimSpace(parts[0]))
	feedbackText := ""
	if len(parts) > 1 {
		feedbackText = strings.TrimSpace(parts[1])
	}
	if feedbackType == "" {
		feedbackType = "UNKNOWN"
	}
	return feedbackType, feedbackText
}
