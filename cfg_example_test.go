package main

import "testing"

func TestExampleConfig(t *testing.T) {
	if _, err := LoadConfig("config.example.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestFilterDevices(t *testing.T) {
	load := func() *Config {
		cfg, err := LoadConfig("config.example.yaml")
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	names := func(c *Config) []string {
		var out []string
		for _, d := range c.Devices {
			out = append(out, d.Name)
		}
		return out
	}

	all := len(load().Devices)
	c := load()
	if err := c.FilterDevices(""); err != nil || len(c.Devices) != all {
		t.Fatalf("leer: %v %d", err, len(c.Devices))
	}
	c = load()
	// Name (ohne Beachtung der Groß-/Kleinschreibung) und unit_id, doppelt genannt
	if err := c.FilterDevices(" wärmepumpe , 2,Wärmepumpe"); err != nil {
		t.Fatal(err)
	}
	if got := names(c); len(got) != 2 || got[0] != "Wärmepumpe" || got[1] != "Spülmaschine" {
		t.Fatalf("gefiltert: %v", got)
	}
	if err := load().FilterDevices("gibtsnicht"); err == nil {
		t.Fatal("unbekanntes Gerät hätte einen Fehler liefern müssen")
	}
}
