package main

import (
	"path/filepath"
	"testing"
)

func importWh(d *Device) float64 {
	vals, _, _ := d.Read(40137, 2)
	return f32(append(make([]uint16, 137), vals...), 40137)
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	cfg := &DeviceConfig{Name: "wp", Serial: "WP1", Phase: "L1", Channels: []*ChannelConfig{{}}}

	d := NewDevice(cfg)
	d.Update([]Reading{{Power: 100, ImportWh: 5000}})
	d.Update([]Reading{{Power: 100, ImportWh: 20}}) // Shelly zurückgesetzt
	if got := importWh(d); got != 5000 {
		t.Fatalf("nach Reset: %v, erwartet 5000", got)
	}
	w := &stateWriter{path: path}
	if err := w.save([]*Device{d}); err != nil {
		t.Fatal(err)
	}

	// Neustart
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	d2 := NewDevice(cfg)
	if !d2.Restore(st["WP1"]) {
		t.Fatal("Restore fehlgeschlagen")
	}
	d2.Update([]Reading{{Power: 100, ImportWh: 120}}) // +100 Wh seit dem Reset
	if got := importWh(d2); got != 5100 {
		t.Fatalf("nach Neustart: %v, erwartet 5100", got)
	}

	// falsche Kanalanzahl wird verworfen
	d3 := NewDevice(&DeviceConfig{Serial: "WP1", Channels: make([]*ChannelConfig, 3)})
	if d3.Restore(st["WP1"]) {
		t.Fatal("Restore mit 3 Kanälen hätte fehlschlagen müssen")
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), "gibts-nicht.json"))
	if err != nil || len(st) != 0 {
		t.Fatalf("st=%v err=%v", st, err)
	}
}
