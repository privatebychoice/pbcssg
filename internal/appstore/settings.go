package appstore

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// runtime_settings keys and their baked-in defaults. These live in app.db (not the authoring
// content.db) because the PUBLIC origin must read them at request time and never opens
// content.db. The creator Settings UI — which has both stores — writes them.
const (
	// KeyAliasDailyCap bounds how many times an account may change its display name per day
	// (anti-churn / anti-grief). 0 or negative disables the cap.
	KeyAliasDailyCap = "alias.dailyCap"

	// DefaultAliasDailyCap is the per-account alias changes allowed per day when unset.
	DefaultAliasDailyCap = 3
)

// Setting returns a runtime setting's value; ok is false (nil error) when unset.
func (s *Store) Setting(key string) (value string, ok bool, err error) {
	switch err := s.db.QueryRow(`SELECT value FROM runtime_settings WHERE key = ?`, key).Scan(&value); {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("appstore: setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting upserts a runtime setting.
func (s *Store) SetSetting(key, value string) error {
	if _, err := s.db.Exec(
		`INSERT INTO runtime_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value,
	); err != nil {
		return fmt.Errorf("appstore: set setting %q: %w", key, err)
	}
	return nil
}

// settingInt reads an integer setting, falling back to def when unset or malformed.
func (s *Store) settingInt(key string, def int) int {
	if v, ok, err := s.Setting(key); err == nil && ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// AliasDailyCap returns the configured per-account daily alias-change cap (default when unset).
// A value <= 0 means the cap is disabled (unlimited changes).
func (s *Store) AliasDailyCap() int { return s.settingInt(KeyAliasDailyCap, DefaultAliasDailyCap) }

// SetAliasDailyCap persists the daily alias-change cap.
func (s *Store) SetAliasDailyCap(n int) error {
	return s.SetSetting(KeyAliasDailyCap, strconv.Itoa(n))
}
