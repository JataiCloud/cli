package config

import (
	"encoding/json"
	"os"
	"time"
)

// State guarda a última checagem de versão (no máximo uma por dia).
type State struct {
	LastUpdateCheck time.Time `json:"last_update_check"`
	LatestVersion   string    `json:"latest_version"`
}

func LoadState(p Paths) State {
	var s State
	if !p.known() {
		return s
	}
	if data, err := os.ReadFile(p.StateFile()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func SaveState(p Paths, s State) error {
	if !p.known() {
		return ErrNoHome
	}
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p.StateFile(), data, 0o600)
}
