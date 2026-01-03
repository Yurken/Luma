package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"luma/core/internal/models"
)

const schema = `
CREATE TABLE IF NOT EXISTS event_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL UNIQUE,
  context_json TEXT NOT NULL,
  action_json TEXT NOT NULL,
  raw_action_json TEXT NOT NULL,
  final_action_json TEXT NOT NULL,
  gateway_decision_json TEXT NOT NULL,
  policy_version TEXT NOT NULL,
  model_version TEXT NOT NULL,
  latency_ms INTEGER NOT NULL,
  user_feedback TEXT,
  created_at TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS feedback_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL,
  feedback TEXT NOT NULL,
  created_at TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS implicit_feedback_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT,
  feedback_type TEXT NOT NULL,
  feedback_text TEXT,
  created_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS budget_usage (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  daily_day TEXT NOT NULL,
  daily_used REAL NOT NULL,
  hourly_hour TEXT NOT NULL,
  hourly_used REAL NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS focus_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts_ms INTEGER NOT NULL,
  app_name TEXT NOT NULL,
  bundle_id TEXT,
  pid INTEGER,
  window_title TEXT,
  duration_ms INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_event_logs_request_id ON event_logs (request_id);
CREATE INDEX IF NOT EXISTS idx_event_logs_created_at_ms ON event_logs (created_at_ms);
CREATE INDEX IF NOT EXISTS idx_feedback_logs_request_id ON feedback_logs (request_id);
CREATE INDEX IF NOT EXISTS idx_focus_events_ts_ms ON focus_events (ts_ms);

CREATE TABLE IF NOT EXISTS profiles (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  confidence REAL DEFAULT 1.0,
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_type TEXT NOT NULL,
  summary TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  importance REAL DEFAULT 0.5
);

CREATE TABLE IF NOT EXISTS focus_state_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts_ms INTEGER NOT NULL,
  focus_state TEXT NOT NULL,
  switch_count INTEGER NOT NULL,
  no_progress_ms INTEGER NOT NULL,
  focus_minutes REAL NOT NULL,
  app_name TEXT,
  window_title TEXT
);

CREATE INDEX IF NOT EXISTS idx_memory_events_type ON memory_events (event_type);
CREATE INDEX IF NOT EXISTS idx_memory_events_created ON memory_events (created_at_ms);
CREATE INDEX IF NOT EXISTS idx_focus_state_snapshots_ts_ms ON focus_state_snapshots (ts_ms);
`

const budgetUsageKey = "budget_usage"

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	if err := applyMigrations(db); err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func applyMigrations(db *sql.DB) error {
	// Check if this is a fresh database by checking if event_logs table is empty
	var tableExists int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='event_logs'").Scan(&tableExists)
	if err != nil || tableExists == 0 {
		// Fresh database, no migration needed
		return nil
	}

	// Check if migration is needed (old schema without created_at_ms)
	var hasColumn int
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('event_logs') WHERE name='created_at_ms'").Scan(&hasColumn)
	if err != nil {
		return fmt.Errorf("check column existence: %w", err)
	}

	if hasColumn == 0 {
		// Old schema detected, apply migrations
		columns := []string{
			"raw_action_json TEXT",
			"final_action_json TEXT",
			"gateway_decision_json TEXT",
			"model_version TEXT",
			"created_at_ms INTEGER",
		}
		for _, column := range columns {
			if err := addColumnIfMissing(db, "event_logs", column); err != nil {
				return err
			}
		}
		if err := backfillEventLogs(db); err != nil {
			return err
		}
	}
	if err := addColumnIfMissing(db, "focus_events", "window_title TEXT"); err != nil {
		return err
	}
	return nil
}

func addColumnIfMissing(db *sql.DB, table, columnDef string) error {
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, columnDef))
	if err != nil {
		if isDuplicateColumnErr(err) || isMissingTableErr(err) {
			return nil
		}
		return fmt.Errorf("add column %s to %s: %w", columnDef, table, err)
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

func isMissingTableErr(err error) bool {
	return strings.Contains(err.Error(), "no such table")
}

func backfillEventLogs(db *sql.DB) error {
	_, err := db.Exec(`
		UPDATE event_logs
		SET raw_action_json = action_json
		WHERE raw_action_json IS NULL OR raw_action_json = ''
	`)
	if err != nil {
		return fmt.Errorf("backfill raw_action_json: %w", err)
	}
	_, err = db.Exec(`
		UPDATE event_logs
		SET final_action_json = action_json
		WHERE final_action_json IS NULL OR final_action_json = ''
	`)
	if err != nil {
		return fmt.Errorf("backfill final_action_json: %w", err)
	}
	legacyDecision := models.GatewayDecision{Decision: models.GatewayAllow, Reason: "legacy_import"}
	legacyDecisionJSON, _ := json.Marshal(legacyDecision)
	_, err = db.Exec(`
		UPDATE event_logs
		SET gateway_decision_json = ?
		WHERE gateway_decision_json IS NULL OR gateway_decision_json = ''
	`, string(legacyDecisionJSON))
	if err != nil {
		return fmt.Errorf("backfill gateway_decision_json: %w", err)
	}
	_, err = db.Exec(`
		UPDATE event_logs
		SET model_version = 'stub'
		WHERE model_version IS NULL OR model_version = ''
	`)
	if err != nil {
		return fmt.Errorf("backfill model_version: %w", err)
	}

	rows, err := db.Query(`
		SELECT request_id, created_at
		FROM event_logs
		WHERE created_at_ms IS NULL OR created_at_ms = 0
	`)
	if err != nil {
		return fmt.Errorf("select created_at rows: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var requestID, createdAt string
		if err := rows.Scan(&requestID, &createdAt); err != nil {
			return fmt.Errorf("scan created_at: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			parsed = time.Now()
		}
		if _, err := db.Exec(
			`UPDATE event_logs SET created_at_ms = ? WHERE request_id = ?`,
			parsed.UnixMilli(),
			requestID,
		); err != nil {
			return fmt.Errorf("update created_at_ms: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	return nil
}

func (s *Store) InsertDecision(entry models.DecisionLogEntry) error {
	ctxJSON, err := json.Marshal(entry.Context)
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}
	rawActionJSON, err := json.Marshal(entry.RawAction)
	if err != nil {
		return fmt.Errorf("marshal raw action: %w", err)
	}
	finalActionJSON, err := json.Marshal(entry.FinalAction)
	if err != nil {
		return fmt.Errorf("marshal final action: %w", err)
	}
	gatewayDecisionJSON, err := json.Marshal(entry.GatewayDecision)
	if err != nil {
		return fmt.Errorf("marshal gateway decision: %w", err)
	}

	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	createdAtMs := entry.CreatedAtMs
	if createdAtMs == 0 {
		createdAtMs = createdAt.UnixMilli()
	}
	policyVersion := entry.PolicyVersion
	if policyVersion == "" {
		policyVersion = "policy_v0"
	}
	modelVersion := entry.ModelVersion
	if modelVersion == "" {
		modelVersion = "stub"
	}

	_, err = s.db.Exec(
		`INSERT INTO event_logs (request_id, context_json, action_json, raw_action_json, final_action_json, gateway_decision_json, policy_version, model_version, latency_ms, created_at, created_at_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.RequestID,
		string(ctxJSON),
		string(finalActionJSON),
		string(rawActionJSON),
		string(finalActionJSON),
		string(gatewayDecisionJSON),
		policyVersion,
		modelVersion,
		entry.LatencyMs,
		createdAt.Format(time.RFC3339Nano),
		createdAtMs,
	)
	if err != nil {
		return fmt.Errorf("insert event log: %w", err)
	}
	return nil
}

func (s *Store) DecisionExists(reqID string) (bool, error) {
	row := s.db.QueryRow(`SELECT 1 FROM event_logs WHERE request_id = ? LIMIT 1`, reqID)
	var exists int
	if err := row.Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check request_id: %w", err)
	}
	return true, nil
}

func (s *Store) RecordFeedback(reqID, feedback string) error {
	_, err := s.db.Exec(
		`UPDATE event_logs SET user_feedback = ? WHERE request_id = ?`,
		feedback,
		reqID,
	)
	if err != nil {
		return fmt.Errorf("update feedback: %w", err)
	}
	createdAt := time.Now()
	_, err = s.db.Exec(
		`INSERT INTO feedback_logs (request_id, feedback, created_at, created_at_ms) VALUES (?, ?, ?, ?)`,
		reqID,
		feedback,
		createdAt.Format(time.RFC3339Nano),
		createdAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("insert feedback log: %w", err)
	}
	return nil
}

func (s *Store) RecordImplicitFeedback(reqID string, feedbackType string, feedbackText string) error {
	createdAtMs := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO implicit_feedback_events (request_id, feedback_type, feedback_text, created_at_ms)
		 VALUES (?, ?, ?, ?)`,
		reqID,
		feedbackType,
		feedbackText,
		createdAtMs,
	)
	if err != nil {
		return fmt.Errorf("insert implicit feedback: %w", err)
	}
	return nil
}

func (s *Store) ListLogs(limit int) ([]models.EventLog, error) {
	return s.ListLogsRange(limit, 0, 0)
}

func (s *Store) ListLogsRange(limit int, sinceMs int64, untilMs int64) ([]models.EventLog, error) {
	if limit <= 0 {
		limit = 50
	}
	if sinceMs < 0 {
		sinceMs = 0
	}
	where := []string{}
	args := []any{}
	if sinceMs > 0 {
		where = append(where, "created_at_ms >= ?")
		args = append(args, sinceMs)
	}
	if untilMs > 0 {
		where = append(where, "created_at_ms <= ?")
		args = append(args, untilMs)
	}

	query := `SELECT request_id, context_json, action_json, raw_action_json, final_action_json, gateway_decision_json, policy_version, model_version, latency_ms, COALESCE(user_feedback, ''), created_at, created_at_ms FROM event_logs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at_ms DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var logs []models.EventLog
	for rows.Next() {
		var entry models.EventLog
		var createdAt string
		var rawActionJSON string
		var finalActionJSON string
		var gatewayDecisionJSON string
		if err := rows.Scan(
			&entry.RequestID,
			&entry.ContextJSON,
			&entry.ActionJSON,
			&rawActionJSON,
			&finalActionJSON,
			&gatewayDecisionJSON,
			&entry.PolicyVersion,
			&entry.ModelVersion,
			&entry.LatencyMs,
			&entry.UserFeedback,
			&createdAt,
			&entry.CreatedAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}

		entry.CreatedAt = parseCreatedAt(createdAt, entry.CreatedAtMs)
		entry.Context = decodeContext(entry.ContextJSON)
		entry.RawAction = decodeAction(rawActionJSON)
		entry.FinalAction = decodeAction(finalActionJSON)
		if entry.FinalAction.ActionType == "" {
			entry.FinalAction = decodeAction(entry.ActionJSON)
		}
		if entry.RawAction.ActionType == "" {
			entry.RawAction = entry.FinalAction
		}
		entry.Action = entry.FinalAction
		entry.GatewayDecision = decodeGatewayDecision(gatewayDecisionJSON)

		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return logs, nil
}

func (s *Store) ExportRecords(limit int, sinceMs int64) ([]models.ExportRecord, error) {
	if limit <= 0 {
		limit = 1000
	}
	if sinceMs < 0 {
		sinceMs = 0
	}
	rows, err := s.db.Query(
		`SELECT request_id, context_json, raw_action_json, final_action_json, gateway_decision_json, policy_version, model_version, latency_ms, COALESCE(user_feedback, ''), created_at, created_at_ms
		 FROM event_logs WHERE created_at_ms >= ?
		 ORDER BY created_at_ms ASC LIMIT ?`,
		sinceMs,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query export: %w", err)
	}
	defer rows.Close()

	var records []models.ExportRecord
	for rows.Next() {
		var record models.ExportRecord
		var contextJSON, rawActionJSON, finalActionJSON, gatewayDecisionJSON, createdAt string
		if err := rows.Scan(
			&record.RequestID,
			&contextJSON,
			&rawActionJSON,
			&finalActionJSON,
			&gatewayDecisionJSON,
			&record.PolicyVersion,
			&record.ModelVersion,
			&record.LatencyMs,
			&record.UserFeedback,
			&createdAt,
			&record.CreatedAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan export: %w", err)
		}
		record.Context = decodeContext(contextJSON)
		record.RawAction = decodeAction(rawActionJSON)
		record.FinalAction = decodeAction(finalActionJSON)
		record.GatewayDecision = decodeGatewayDecision(gatewayDecisionJSON)
		if record.RawAction.ActionType == "" {
			record.RawAction = record.FinalAction
		}
		if record.CreatedAtMs == 0 {
			record.CreatedAtMs = parseCreatedAt(createdAt, 0).UnixMilli()
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return records, nil
}

func (s *Store) ListSettings() ([]models.SettingItem, error) {
	rows, err := s.db.Query(`SELECT key, value, updated_at_ms FROM user_settings ORDER BY key ASC`)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()

	var settings []models.SettingItem
	for rows.Next() {
		var item models.SettingItem
		if err := rows.Scan(&item.Key, &item.Value, &item.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		settings = append(settings, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return settings, nil
}

func (s *Store) UpsertSetting(key, value string) error {
	updatedAt := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO user_settings (key, value, updated_at_ms)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at_ms = excluded.updated_at_ms`,
		key,
		value,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert setting: %w", err)
	}
	return nil
}

func (s *Store) GetSetting(key string) (string, bool, error) {
	row := s.db.QueryRow(`SELECT value FROM user_settings WHERE key = ?`, key)
	var value string
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get setting: %w", err)
	}
	return value, true, nil
}

func (s *Store) GetBudgetUsage() (models.BudgetUsage, error) {
	row := s.db.QueryRow(
		`SELECT daily_day, daily_used, hourly_hour, hourly_used FROM budget_usage WHERE id = 1`,
	)
	var usage models.BudgetUsage
	if err := row.Scan(&usage.DailyDay, &usage.DailyUsed, &usage.HourlyHour, &usage.HourlyUsed); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return models.BudgetUsage{}, fmt.Errorf("query budget usage: %w", err)
		}
		legacy, err := s.loadLegacyBudgetUsage()
		if err != nil {
			return models.BudgetUsage{}, err
		}
		if legacy.DailyDay != "" || legacy.HourlyHour != "" {
			_ = s.SetBudgetUsage(legacy)
			return legacy, nil
		}
		return models.BudgetUsage{}, nil
	}
	return usage, nil
}

func (s *Store) SetBudgetUsage(usage models.BudgetUsage) error {
	updatedAt := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO budget_usage (id, daily_day, daily_used, hourly_hour, hourly_used, updated_at_ms)
		 VALUES (1, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   daily_day = excluded.daily_day,
		   daily_used = excluded.daily_used,
		   hourly_hour = excluded.hourly_hour,
		   hourly_used = excluded.hourly_used,
		   updated_at_ms = excluded.updated_at_ms`,
		usage.DailyDay,
		usage.DailyUsed,
		usage.HourlyHour,
		usage.HourlyUsed,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert budget usage: %w", err)
	}
	return nil
}

func (s *Store) loadLegacyBudgetUsage() (models.BudgetUsage, error) {
	value, ok, err := s.GetSetting(budgetUsageKey)
	if err != nil {
		return models.BudgetUsage{}, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		return models.BudgetUsage{}, nil
	}
	var usage models.BudgetUsage
	if err := json.Unmarshal([]byte(value), &usage); err != nil {
		return models.BudgetUsage{}, fmt.Errorf("decode legacy budget usage: %w", err)
	}
	return usage, nil
}

func (s *Store) InsertFocusEvent(event models.FocusEvent) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO focus_events (ts_ms, app_name, bundle_id, pid, window_title, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		event.TsMs,
		event.AppName,
		event.BundleID,
		event.PID,
		event.WindowTitle,
		event.DurationMs,
	)
	if err != nil {
		return 0, fmt.Errorf("insert focus event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("focus event id: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateFocusDuration(id int64, durationMs int64) error {
	_, err := s.db.Exec(
		`UPDATE focus_events SET duration_ms = ? WHERE id = ?`,
		durationMs,
		id,
	)
	if err != nil {
		return fmt.Errorf("update focus duration: %w", err)
	}
	return nil
}

func (s *Store) UpdateFocusWindowTitle(id int64, title string) error {
	_, err := s.db.Exec(
		`UPDATE focus_events SET window_title = ? WHERE id = ?`,
		title,
		id,
	)
	if err != nil {
		return fmt.Errorf("update focus window title: %w", err)
	}
	return nil
}

func (s *Store) LatestFocusEvent() (models.FocusEvent, bool, error) {
	row := s.db.QueryRow(
		`SELECT id, ts_ms, app_name, COALESCE(bundle_id, ''), COALESCE(pid, 0), COALESCE(window_title, ''), duration_ms
		 FROM focus_events ORDER BY ts_ms DESC, id DESC LIMIT 1`,
	)
	var event models.FocusEvent
	if err := row.Scan(&event.ID, &event.TsMs, &event.AppName, &event.BundleID, &event.PID, &event.WindowTitle, &event.DurationMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.FocusEvent{}, false, nil
		}
		return models.FocusEvent{}, false, fmt.Errorf("latest focus event: %w", err)
	}
	return event, true, nil
}

func (s *Store) ListFocusEvents(limit int) ([]models.FocusEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT id, ts_ms, app_name, COALESCE(bundle_id, ''), COALESCE(pid, 0), COALESCE(window_title, ''), duration_ms
		 FROM focus_events ORDER BY ts_ms DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list focus events: %w", err)
	}
	defer rows.Close()

	var events []models.FocusEvent
	for rows.Next() {
		var event models.FocusEvent
		if err := rows.Scan(&event.ID, &event.TsMs, &event.AppName, &event.BundleID, &event.PID, &event.WindowTitle, &event.DurationMs); err != nil {
			return nil, fmt.Errorf("scan focus event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("focus rows: %w", err)
	}
	return events, nil
}

func (s *Store) FocusMetrics(windowMs int64) (models.FocusMetrics, error) {
	if windowMs <= 0 {
		windowMs = int64((10 * time.Minute).Milliseconds())
	}
	sinceMs := time.Now().UnixMilli() - windowMs
	if sinceMs < 0 {
		sinceMs = 0
	}
	rows, err := s.db.Query(
		`SELECT ts_ms, duration_ms FROM focus_events WHERE ts_ms >= ? ORDER BY ts_ms ASC`,
		sinceMs,
	)
	if err != nil {
		return models.FocusMetrics{}, fmt.Errorf("query focus metrics: %w", err)
	}
	defer rows.Close()

	type focusRow struct {
		tsMs       int64
		durationMs int64
	}
	var events []focusRow
	for rows.Next() {
		var row focusRow
		if err := rows.Scan(&row.tsMs, &row.durationMs); err != nil {
			return models.FocusMetrics{}, fmt.Errorf("scan focus metrics: %w", err)
		}
		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		return models.FocusMetrics{}, fmt.Errorf("focus metrics rows: %w", err)
	}

	var totalMs int64
	nowMs := time.Now().UnixMilli()
	for i, event := range events {
		if event.durationMs > 0 {
			totalMs += event.durationMs
			continue
		}
		if i+1 < len(events) {
			delta := events[i+1].tsMs - event.tsMs
			if delta > 0 {
				totalMs += delta
			}
			continue
		}
		delta := nowMs - event.tsMs
		if delta > 0 {
			totalMs += delta
		}
	}

	switchCount := 0
	if len(events) > 1 {
		switchCount = len(events) - 1
	}
	return models.FocusMetrics{
		WindowMs:     windowMs,
		SwitchCount:  switchCount,
		FocusMinutes: float64(totalMs) / 60000,
	}, nil
}

func (s *Store) InsertFocusStateSnapshot(snapshot models.FocusStateSnapshot) error {
	_, err := s.db.Exec(
		`INSERT INTO focus_state_snapshots (ts_ms, focus_state, switch_count, no_progress_ms, focus_minutes, app_name, window_title)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snapshot.TsMs,
		snapshot.FocusState,
		snapshot.SwitchCount,
		snapshot.NoProgressMs,
		snapshot.FocusMinutes,
		snapshot.AppName,
		snapshot.WindowTitle,
	)
	if err != nil {
		return fmt.Errorf("insert focus state snapshot: %w", err)
	}
	return nil
}

func (s *Store) ListFocusStateSnapshots(limit int, sinceMs int64, untilMs int64) ([]models.FocusStateSnapshot, error) {
	if limit <= 0 {
		limit = 200
	}
	if sinceMs < 0 {
		sinceMs = 0
	}

	where := []string{}
	args := []any{}
	if sinceMs > 0 {
		where = append(where, "ts_ms >= ?")
		args = append(args, sinceMs)
	}
	if untilMs > 0 {
		where = append(where, "ts_ms <= ?")
		args = append(args, untilMs)
	}

	query := `SELECT ts_ms, focus_state, switch_count, no_progress_ms, focus_minutes, COALESCE(app_name, ''), COALESCE(window_title, '') FROM focus_state_snapshots`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY ts_ms DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query focus state snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []models.FocusStateSnapshot
	for rows.Next() {
		var snapshot models.FocusStateSnapshot
		if err := rows.Scan(
			&snapshot.TsMs,
			&snapshot.FocusState,
			&snapshot.SwitchCount,
			&snapshot.NoProgressMs,
			&snapshot.FocusMinutes,
			&snapshot.AppName,
			&snapshot.WindowTitle,
		); err != nil {
			return nil, fmt.Errorf("scan focus state snapshot: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("focus state snapshot rows: %w", err)
	}
	return snapshots, nil
}

func parseCreatedAt(createdAt string, createdAtMs int64) time.Time {
	if createdAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			return parsed
		}
	}
	if createdAtMs > 0 {
		return time.UnixMilli(createdAtMs)
	}
	return time.Now()
}

func decodeContext(raw string) models.Context {
	var ctx models.Context
	if raw == "" {
		return ctx
	}
	_ = json.Unmarshal([]byte(raw), &ctx)
	return ctx
}

func decodeAction(raw string) models.Action {
	var action models.Action
	if raw == "" {
		return action
	}
	_ = json.Unmarshal([]byte(raw), &action)
	return action
}

func decodeGatewayDecision(raw string) models.GatewayDecision {
	var decision models.GatewayDecision
	if raw == "" {
		decision.Decision = models.GatewayAllow
		decision.Reason = "unknown"
		return decision
	}
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		decision.Decision = models.GatewayAllow
		decision.Reason = "unknown"
	}
	return decision
}
