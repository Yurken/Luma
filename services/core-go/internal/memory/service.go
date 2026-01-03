package memory

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
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
}

// GetProfileSummary returns a natural language summary of user profiles
func (s *Service) GetProfileSummary() string {
	rows, err := s.db.Query("SELECT key, value FROM profiles WHERE confidence > 0.5")
	if err != nil {
		s.logger.Error("failed to query profiles", slog.Any("error", err))
		return ""
	}
	defer rows.Close()

	var summaries []string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
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

// ProcessFeedback analyzes user feedback and updates memory
func (s *Service) ProcessFeedback(requestID, feedback string) error {
	// TODO: Incorporate implicit feedback signals and decay profiles over time.
	// TODO: Add user-controlled reset to clear learned preferences and memory.
	// TODO: Learn preferred frequency, time-of-day tolerance, and suggestion-type acceptance.
	// 1. Get the original action from event_logs
	var finalActionJSON string
	err := s.db.QueryRow("SELECT final_action_json FROM event_logs WHERE request_id = ?", requestID).Scan(&finalActionJSON)
	if err != nil {
		return fmt.Errorf("find event log: %w", err)
	}

	// 2. Parse action (simple string search for now to avoid importing models)
	// In a real app, we should decode JSON properly
	actionType := "UNKNOWN"
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

	// 3. Create a memory event
	summary := fmt.Sprintf("User provided feedback '%s' for action '%s'", feedback, actionType)

	// 4. Update Profile if strong signal (e.g. DISLIKE)
	if feedback == "DISLIKE" {
		// Example: If user dislikes REST_REMINDER, maybe they don't like being told to rest
		if actionType == "REST_REMINDER" {
			s.SetProfile("preference_rest_reminder", "User dislikes frequent rest reminders", 0.8)
		}
	}

	return s.AddEvent("feedback", summary, 0.5)
}
