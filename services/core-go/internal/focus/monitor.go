package focus

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"luma/core/internal/db"
	"luma/core/internal/models"
)

const (
	defaultPollInterval   = time.Second
	defaultSwitchWindow   = 10 * time.Minute
	defaultNoProgressHold = 45 * time.Minute
)

var ErrUnsupported = errors.New("focus monitor unsupported")

const settingFocusMonitorEnabled = "focus_monitor_enabled"

type FocusSnapshot struct {
	TsMs        int64
	AppName     string
	BundleID    string
	PID         int
	WindowTitle string
}

type provider interface {
	Current() (FocusSnapshot, error)
}

type Monitor struct {
	store    *db.Store
	logger   *slog.Logger
	interval time.Duration
	provider provider

	enabled atomic.Bool

	mu              sync.RWMutex
	last            models.FocusEvent
	hasLast         bool
	lastWindowTitle string
	switchWindow    time.Duration
	switches        []int64
	lastTitleChange int64
	noProgressHold  time.Duration
	noProgress      bool
}

func NewMonitor(store *db.Store, logger *slog.Logger, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	prov, err := newProvider(logger)
	if err != nil {
		logger.Warn("focus provider unavailable", slog.Any("error", err))
	}
	return &Monitor{
		store:          store,
		logger:         logger,
		interval:       interval,
		provider:       prov,
		switchWindow:   defaultSwitchWindow,
		noProgressHold: defaultNoProgressHold,
	}
}

func (m *Monitor) Start() {
	if m.provider == nil {
		return
	}
	enabled, err := m.loadEnabledSetting()
	if err != nil {
		m.logger.Error("load focus setting failed", slog.Any("error", err))
	}
	m.enabled.Store(enabled)
	if enabled {
		m.loadLastEvent()
	}
	go m.loop()
}

func (m *Monitor) Enabled() bool {
	return m.provider != nil && m.enabled.Load()
}

func (m *Monitor) SetEnabled(enabled bool) error {
	if m.provider == nil {
		m.enabled.Store(false)
		return ErrUnsupported
	}
	previous := m.enabled.Swap(enabled)
	if previous && !enabled {
		m.closeCurrentEvent()
	}
	if enabled {
		m.clearLast()
		m.loadLastEvent()
	}
	return nil
}

func (m *Monitor) Current() (models.FocusCurrent, bool, error) {
	if !m.Enabled() {
		return models.FocusCurrent{}, false, nil
	}
	event, ok, err := m.store.LatestFocusEvent()
	if err != nil {
		return models.FocusCurrent{}, false, err
	}
	if !ok || event.AppName == "" {
		return models.FocusCurrent{}, false, nil
	}
	focusMs := event.DurationMs
	if focusMs == 0 {
		focusMs = time.Now().UnixMilli() - event.TsMs
	}
	if focusMs < 0 {
		focusMs = 0
	}
	title := m.lastWindowTitle
	if title == "" {
		title = event.WindowTitle
	}
	return models.FocusCurrent{
		TsMs:         event.TsMs,
		AppName:      event.AppName,
		BundleID:     event.BundleID,
		PID:          event.PID,
		WindowTitle:  title,
		FocusMinutes: float64(focusMs) / 60000,
	}, true, nil
}

func (m *Monitor) loop() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for range ticker.C {
		if !m.Enabled() {
			continue
		}
		snapshot, err := m.provider.Current()
		if err != nil {
			m.logger.Warn("focus poll failed", slog.Any("error", err))
			continue
		}
		if snapshot.AppName == "" {
			continue
		}
		m.handleSnapshot(snapshot)
	}
}

func (m *Monitor) handleSnapshot(snapshot FocusSnapshot) {
	nowMs := snapshot.TsMs
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}

	m.mu.Lock()
	last := m.last
	hasLast := m.hasLast
	prevTitle := m.lastWindowTitle
	snapshotTitle := snapshot.WindowTitle
	currentTitle := snapshotTitle
	if currentTitle == "" {
		currentTitle = prevTitle
	}
	titleChanged := currentTitle != "" && currentTitle != prevTitle
	if currentTitle != "" {
		m.lastWindowTitle = currentTitle
	}
	if titleChanged || m.lastTitleChange == 0 {
		m.lastTitleChange = nowMs
		m.noProgress = false
	}

	same := hasLast && sameApp(snapshot, last)
	var updateTitleID int64
	var updateTitle string
	if titleChanged && same && last.ID != 0 {
		updateTitleID = last.ID
		updateTitle = currentTitle
		last.WindowTitle = currentTitle
		m.last = last
	}
	if hasLast && !same {
		m.switches = append(m.switches, nowMs)
		m.pruneSwitchesLocked(nowMs)
		m.lastTitleChange = nowMs
		m.noProgress = false
	}
	if same && !titleChanged && m.lastTitleChange > 0 {
		elapsed := nowMs - m.lastTitleChange
		if elapsed >= m.noProgressHold.Milliseconds() {
			m.noProgress = true
		}
	}
	m.mu.Unlock()

	if updateTitleID != 0 {
		if err := m.store.UpdateFocusWindowTitle(updateTitleID, updateTitle); err != nil {
			m.logger.Error("update focus window title failed", slog.Any("error", err))
		}
	}

	if hasLast && same {
		return
	}

	if hasLast && last.ID != 0 {
		duration := nowMs - last.TsMs
		if duration < 0 {
			duration = 0
		}
		if err := m.store.UpdateFocusDuration(last.ID, duration); err != nil {
			m.logger.Error("update focus duration failed", slog.Any("error", err))
		}
	}

	newEvent := models.FocusEvent{
		TsMs:        nowMs,
		AppName:     snapshot.AppName,
		BundleID:    snapshot.BundleID,
		PID:         snapshot.PID,
		WindowTitle: snapshotTitle,
		DurationMs:  0,
	}
	id, err := m.store.InsertFocusEvent(newEvent)
	if err != nil {
		m.logger.Error("insert focus event failed", slog.Any("error", err))
		return
	}
	newEvent.ID = id

	m.mu.Lock()
	m.last = newEvent
	m.hasLast = true
	m.mu.Unlock()
}

func (m *Monitor) SwitchCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.switches)
}

func (m *Monitor) NoProgress() (bool, time.Duration) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.noProgress || m.lastTitleChange == 0 {
		return false, 0
	}
	elapsedMs := time.Now().UnixMilli() - m.lastTitleChange
	if elapsedMs < 0 {
		elapsedMs = 0
	}
	return true, time.Duration(elapsedMs) * time.Millisecond
}

func (m *Monitor) pruneSwitchesLocked(nowMs int64) {
	if len(m.switches) == 0 {
		return
	}
	cutoff := nowMs - m.switchWindow.Milliseconds()
	idx := 0
	for idx < len(m.switches) && m.switches[idx] < cutoff {
		idx++
	}
	if idx > 0 {
		m.switches = m.switches[idx:]
	}
}

func (m *Monitor) loadEnabledSetting() (bool, error) {
	value, ok, err := m.store.GetSetting(settingFocusMonitorEnabled)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return value == "true", nil
}

func (m *Monitor) loadLastEvent() {
	event, ok, err := m.store.LatestFocusEvent()
	if err != nil {
		m.logger.Error("load last focus event failed", slog.Any("error", err))
		return
	}
	if !ok {
		return
	}
	m.mu.Lock()
	m.last = event
	m.hasLast = true
	if event.WindowTitle != "" {
		m.lastWindowTitle = event.WindowTitle
	}
	m.mu.Unlock()
}

func (m *Monitor) clearLast() {
	m.mu.Lock()
	m.last = models.FocusEvent{}
	m.hasLast = false
	m.mu.Unlock()
}

func (m *Monitor) closeCurrentEvent() {
	m.mu.RLock()
	last := m.last
	hasLast := m.hasLast
	m.mu.RUnlock()

	if !hasLast || last.ID == 0 || last.DurationMs > 0 {
		m.clearLast()
		return
	}

	endMs := time.Now().UnixMilli()
	duration := endMs - last.TsMs
	if duration < 0 {
		duration = 0
	}
	if err := m.store.UpdateFocusDuration(last.ID, duration); err != nil {
		m.logger.Error("close focus event failed", slog.Any("error", err))
	}
	m.clearLast()
}

func sameApp(snapshot FocusSnapshot, event models.FocusEvent) bool {
	if snapshot.AppName != event.AppName {
		return false
	}
	if snapshot.BundleID != event.BundleID {
		return false
	}
	if snapshot.PID != event.PID {
		return false
	}
	return true
}
