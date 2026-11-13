package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
)

// ChannelState ist der persistierte Zählerstand eines Kanals.
// Gemeldeter Zählerstand = Shelly-Zähler + Offset; Last ist der zuletzt gemeldete Wert.
type ChannelState struct {
	LastImportWh   float64 `json:"last_import_wh"`
	LastExportWh   float64 `json:"last_export_wh"`
	OffsetImportWh float64 `json:"offset_import_wh"`
	OffsetExportWh float64 `json:"offset_export_wh"`
}

type DeviceState struct {
	Name     string         `json:"name"`
	Channels []ChannelState `json:"channels"`
}

// State ist der Inhalt der State-Datei, Schlüssel ist die Seriennummer des Geräts.
type State map[string]DeviceState

func LoadState(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return nil, err
	}
	st := State{}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// SaveState schreibt atomar (temporäre Datei + rename), damit ein Absturz
// beim Schreiben die alte Datei nicht zerstört.
func SaveState(path string, st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func collectState(devices []*Device) State {
	st := State{}
	for _, d := range devices {
		st[d.cfg.Serial] = d.State()
	}
	return st
}

// stateWriter speichert nur, wenn sich etwas geändert hat.
type stateWriter struct {
	path string
	last State
}

func (w *stateWriter) save(devices []*Device) error {
	st := collectState(devices)
	if reflect.DeepEqual(st, w.last) {
		return nil
	}
	if err := SaveState(w.path, st); err != nil {
		return err
	}
	w.last = st
	return nil
}
