package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Standard-Port für alle Geräte ohne eigenen Port.
	ListenPort int `yaml:"listen_port"`
	// Abfrageintervall der Shellys.
	PollInterval time.Duration `yaml:"poll_interval"`
	// HTTP-Timeout pro Shelly-Request.
	HTTPTimeout time.Duration `yaml:"http_timeout"`
	// Ab diesem Alter der Messwerte antwortet ein Gerät mit einer Modbus-Exception,
	// damit der Wechselrichter keine eingefrorenen Werte übernimmt.
	StaleAfter time.Duration `yaml:"stale_after"`
	// Datei für die Zählerstände (leer = keine Persistenz).
	StateFile string `yaml:"state_file"`
	// Wie oft die Zählerstände geschrieben werden (zusätzlich beim Beenden).
	StateInterval time.Duration   `yaml:"state_interval"`
	Devices       []*DeviceConfig `yaml:"devices"`
}

type DeviceConfig struct {
	Name   string `yaml:"name"`
	Port   int    `yaml:"port"`
	UnitID uint8  `yaml:"unit_id"`
	// SunSpec Common Model
	Manufacturer string `yaml:"manufacturer"`
	Model        string `yaml:"model"`
	Version      string `yaml:"version"`
	Serial       string `yaml:"serial"`
	// Bei genau einem Kanal: auf welcher Phase (L1/L2/L3) er gemeldet wird.
	Phase    string           `yaml:"phase"`
	Channels []*ChannelConfig `yaml:"channels"`
}

type ChannelConfig struct {
	Host string `yaml:"host"`
	// shelly_switch | shelly_pm1 | shelly_em1 | shelly_em | shelly_gen1_relay | shelly_gen1_emeter | goe
	// (ohne "shelly_"-Präfix weiterhin gültig)
	Type string `yaml:"type"`
	ID   int    `yaml:"id"`
	// Phase der Quelle (a|b|c, 1|2|3 oder L1|L2|L3):
	// Pflicht bei type=shelly_em, optional bei type=goe (ohne Angabe: Summe aller Phasen).
	SourcePhase string `yaml:"phase"`
	// Veralteter Alias für phase bei type=shelly_em.
	EMPhase string `yaml:"em_phase"`
	// Vorzeichen umdrehen (Bezug <-> Einspeisung)
	Invert bool `yaml:"invert"`

	// intern: 0..2, -1 = keine Phase angegeben
	phaseIdx int
	// intern: Anteil am Gesamtzähler, wenn die Quelle nur einen Zähler für alle Phasen hat (go-e)
	energyShare float64
}

var validTypes = map[string]bool{
	"shelly_switch": true, "shelly_pm1": true, "shelly_em1": true, "shelly_em": true,
	"shelly_gen1_relay": true, "shelly_gen1_emeter": true, "goe": true,
}

// Typnamen aus v0.1/v0.2 ohne Herstellerpräfix bleiben gültig.
var typeAliases = map[string]string{
	"switch": "shelly_switch", "pm1": "shelly_pm1", "em1": "shelly_em1", "em": "shelly_em",
	"gen1_relay": "shelly_gen1_relay", "gen1_emeter": "shelly_gen1_emeter",
}

func parsePhase(s string) (int, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "":
		return -1, true
	case "A", "1", "L1":
		return 0, true
	case "B", "2", "L2":
		return 1, true
	case "C", "3", "L3":
		return 2, true
	}
	return -1, false
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	dec := yaml.NewDecoder(strings.NewReader(os.ExpandEnv(string(raw))))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config parsen: %w", err)
	}
	return cfg, cfg.applyDefaults()
}

func (c *Config) applyDefaults() error {
	if c.ListenPort == 0 {
		c.ListenPort = 502
	}
	if c.PollInterval == 0 {
		c.PollInterval = 2 * time.Second
	}
	if c.HTTPTimeout == 0 {
		c.HTTPTimeout = 2 * time.Second
	}
	if c.StaleAfter == 0 {
		c.StaleAfter = 30 * time.Second
	}
	if c.StateInterval == 0 {
		c.StateInterval = time.Minute
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("keine devices konfiguriert")
	}

	seen := map[string]string{}
	serials := map[string]string{}
	for i, d := range c.Devices {
		if d.Name == "" {
			d.Name = fmt.Sprintf("device-%d", i+1)
		}
		if d.Port == 0 {
			d.Port = c.ListenPort
		}
		if d.UnitID == 0 {
			d.UnitID = 1
		}
		if d.Manufacturer == "" {
			d.Manufacturer = "Fronius"
		}
		if d.Model == "" {
			d.Model = "Smart Meter TS 65A-3"
		}
		if d.Version == "" {
			d.Version = "1.0"
		}
		if d.Serial == "" {
			d.Serial = fmt.Sprintf("SFM%05d%03d", d.Port, d.UnitID)
		}
		key := fmt.Sprintf("%d/%d", d.Port, d.UnitID)
		if other, ok := seen[key]; ok {
			return fmt.Errorf("device %q: port %d / unit_id %d bereits von %q belegt", d.Name, d.Port, d.UnitID, other)
		}
		seen[key] = d.Name
		if other, ok := serials[d.Serial]; ok {
			return fmt.Errorf("device %q: serial %q bereits von %q verwendet", d.Name, d.Serial, other)
		}
		serials[d.Serial] = d.Name

		if n := len(d.Channels); n != 1 && n != 3 {
			return fmt.Errorf("device %q: 1 oder 3 channels erwartet, %d konfiguriert", d.Name, n)
		}
		d.Phase = strings.ToUpper(d.Phase)
		switch d.Phase {
		case "":
			d.Phase = "L1"
		case "L1", "L2", "L3":
		default:
			return fmt.Errorf("device %q: phase muss L1, L2 oder L3 sein", d.Name)
		}
		for j, ch := range d.Channels {
			if ch.Host == "" {
				return fmt.Errorf("device %q channel %d: host fehlt", d.Name, j+1)
			}
			ch.Type = strings.ToLower(ch.Type)
			if t, ok := typeAliases[ch.Type]; ok {
				ch.Type = t
			}
			if !validTypes[ch.Type] {
				return fmt.Errorf("device %q channel %d: unbekannter type %q", d.Name, j+1, ch.Type)
			}
			if ch.SourcePhase == "" {
				ch.SourcePhase = ch.EMPhase
			}
			idx, ok := parsePhase(ch.SourcePhase)
			if !ok {
				return fmt.Errorf("device %q channel %d: ungültige phase %q", d.Name, j+1, ch.SourcePhase)
			}
			ch.phaseIdx = idx
			if ch.Type == "shelly_em" && idx < 0 {
				return fmt.Errorf("device %q channel %d: type shelly_em braucht phase a|b|c", d.Name, j+1)
			}
		}

		// go-e liefert nur einen Gesamtzähler; er wird gleichmäßig auf die
		// Kanäle desselben Geräts verteilt, damit die Summe stimmt.
		goeCount := map[string]int{}
		for _, ch := range d.Channels {
			ch.energyShare = 1
			if ch.Type == "goe" {
				goeCount[ch.Host]++
			}
		}
		for _, ch := range d.Channels {
			if n := goeCount[ch.Host]; ch.Type == "goe" && n > 1 {
				ch.energyShare = 1 / float64(n)
			}
		}
	}
	return nil
}
