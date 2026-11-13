package main

import (
	"context"
	"net/http"
	"time"
)

// Poller fragt die Quellen der Geräte ab.
//
// Im Modus on_demand werden nur Geräte abgefragt, die innerhalb von idleAfter per
// Modbus angefragt wurden. So können mehrere Instanzen (je eine IP) dieselbe
// vollständige Config laden, ohne alle Shellys/Wallboxen mehrfach abzufragen:
// Jede Instanz pollt nur die Geräte, deren Modbus-Adresse bei ihr angefragt wird.
type Poller struct {
	devices   []*Device
	onDemand  bool
	idleAfter time.Duration
	wake      chan struct{}
}

func NewPoller(devices []*Device, onDemand bool, idleAfter time.Duration) *Poller {
	return &Poller{devices: devices, onDemand: onDemand, idleAfter: idleAfter, wake: make(chan struct{}, 1)}
}

// Active: Gerät wird abgefragt (immer im Modus always).
func (p *Poller) Active(d *Device) bool {
	if !p.onDemand {
		return true
	}
	last := d.lastRequest.Load()
	return last != 0 && time.Since(time.Unix(0, last)) <= p.idleAfter
}

// Touch vermerkt eine Modbus-Anfrage und stößt bei Bedarf sofort eine Abfrage an.
func (p *Poller) Touch(d *Device, staleAfter time.Duration) {
	wasActive := p.Active(d)
	d.lastRequest.Store(time.Now().UnixNano())
	if !wasActive || d.Age() > staleAfter {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// WaitFresh wartet bis zu max, bis das Gerät aktuelle Werte hat.
func (p *Poller) WaitFresh(d *Device, staleAfter, max time.Duration) {
	deadline := time.Now().Add(max)
	for d.Age() > staleAfter && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

func (p *Poller) activeDevices() []*Device {
	out := make([]*Device, 0, len(p.devices))
	for _, d := range p.devices {
		if p.Active(d) {
			out = append(out, d)
		}
	}
	return out
}

// Run pollt im Intervall und zusätzlich sofort, wenn ein Gerät aktiv wird.
func (p *Poller) Run(ctx context.Context, client *http.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if active := p.activeDevices(); len(active) > 0 {
			poll(ctx, client, active)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.wake:
		}
	}
}
