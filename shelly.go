package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Reading ist ein Messwert eines Shelly-Kanals.
type Reading struct {
	Power     float64 // W, positiv = Bezug/Verbrauch
	Voltage   float64 // V
	Current   float64 // A
	PF        float64 // 0 = unbekannt
	Freq      float64 // Hz, 0 = unbekannt
	ImportWh  float64
	ExportWh  float64
	HasExport bool
}

// fetcher holt jede URL pro Poll-Zyklus nur einmal (mehrere Kanäle desselben Shellys).
type fetcher struct {
	client *http.Client
	mu     sync.Mutex
	cache  map[string]*fetchResult
}

type fetchResult struct {
	once sync.Once
	data map[string]any
	err  error
}

func newFetcher(client *http.Client) *fetcher {
	return &fetcher{client: client, cache: map[string]*fetchResult{}}
}

func (f *fetcher) get(ctx context.Context, url string) (map[string]any, error) {
	f.mu.Lock()
	r, ok := f.cache[url]
	if !ok {
		r = &fetchResult{}
		f.cache[url] = r
	}
	f.mu.Unlock()

	r.once.Do(func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			r.err = err
			return
		}
		resp, err := f.client.Do(req)
		if err != nil {
			r.err = err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			r.err = fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
			return
		}
		r.err = json.NewDecoder(resp.Body).Decode(&r.data)
	})
	return r.data, r.err
}

func readChannel(ctx context.Context, f *fetcher, ch *ChannelConfig) (Reading, error) {
	base := "http://" + ch.Host
	var r Reading

	switch ch.Type {
	case "switch", "pm1":
		method := "Switch"
		if ch.Type == "pm1" {
			method = "PM1"
		}
		s, err := f.get(ctx, fmt.Sprintf("%s/rpc/%s.GetStatus?id=%d", base, method, ch.ID))
		if err != nil {
			return r, err
		}
		p, ok := num(s, "apower")
		if !ok {
			return r, fmt.Errorf("%s: kein apower – Kanal ohne Leistungsmessung?", ch.Host)
		}
		r.Power = p
		r.Voltage, _ = num(s, "voltage")
		r.Current, _ = num(s, "current")
		r.PF, _ = num(s, "pf")
		r.Freq, _ = num(s, "freq")
		r.ImportWh, _ = num(sub(s, "aenergy"), "total")
		r.ExportWh, r.HasExport = num(sub(s, "ret_aenergy"), "total")

	case "em1":
		s, err := f.get(ctx, fmt.Sprintf("%s/rpc/EM1.GetStatus?id=%d", base, ch.ID))
		if err != nil {
			return r, err
		}
		e, err := f.get(ctx, fmt.Sprintf("%s/rpc/EM1Data.GetStatus?id=%d", base, ch.ID))
		if err != nil {
			return r, err
		}
		r.Power, _ = num(s, "act_power")
		r.Voltage, _ = num(s, "voltage")
		r.Current, _ = num(s, "current")
		r.PF, _ = num(s, "pf")
		r.Freq, _ = num(s, "freq")
		r.ImportWh, _ = num(e, "total_act_energy")
		r.ExportWh, r.HasExport = num(e, "total_act_ret_energy")

	case "em":
		s, err := f.get(ctx, fmt.Sprintf("%s/rpc/EM.GetStatus?id=%d", base, ch.ID))
		if err != nil {
			return r, err
		}
		e, err := f.get(ctx, fmt.Sprintf("%s/rpc/EMData.GetStatus?id=%d", base, ch.ID))
		if err != nil {
			return r, err
		}
		p := string("abc"[ch.phaseIdx]) + "_"
		r.Power, _ = num(s, p+"act_power")
		r.Voltage, _ = num(s, p+"voltage")
		r.Current, _ = num(s, p+"current")
		r.PF, _ = num(s, p+"pf")
		r.Freq, _ = num(s, p+"freq")
		r.ImportWh, _ = num(e, p+"total_act_energy")
		r.ExportWh, r.HasExport = num(e, p+"total_act_ret_energy")

	case "gen1_relay":
		s, err := f.get(ctx, base+"/status")
		if err != nil {
			return r, err
		}
		m, err := arrayItem(s, "meters", ch.ID)
		if err != nil {
			return r, fmt.Errorf("%s: %w", ch.Host, err)
		}
		r.Power, _ = num(m, "power")
		total, _ := num(m, "total") // Wattminuten
		r.ImportWh = total / 60

	case "gen1_emeter":
		s, err := f.get(ctx, base+"/status")
		if err != nil {
			return r, err
		}
		m, err := arrayItem(s, "emeters", ch.ID)
		if err != nil {
			return r, fmt.Errorf("%s: %w", ch.Host, err)
		}
		r.Power, _ = num(m, "power")
		r.Voltage, _ = num(m, "voltage")
		r.Current, _ = num(m, "current")
		r.PF, _ = num(m, "pf")
		r.ImportWh, _ = num(m, "total")
		r.ExportWh, r.HasExport = num(m, "total_returned")

	case "goe":
		var err error
		if r, err = readGoe(ctx, f, ch); err != nil {
			return r, err
		}
	}

	r.ImportWh *= ch.energyShare
	r.ExportWh *= ch.energyShare

	if ch.Invert {
		r.Power = -r.Power
		r.ImportWh, r.ExportWh = r.ExportWh, r.ImportWh
	}
	return r, nil
}

func sub(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func num(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key].(float64)
	return v, ok
}

func arrayItem(m map[string]any, key string, idx int) (map[string]any, error) {
	arr, ok := m[key].([]any)
	if !ok || idx < 0 || idx >= len(arr) {
		return nil, fmt.Errorf("%s[%d] nicht vorhanden", key, idx)
	}
	item, ok := arr[idx].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s[%d] ungültig", key, idx)
	}
	return item, nil
}
