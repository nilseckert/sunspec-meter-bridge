package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnDemandPolling(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var hits [2]atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "1" {
			hits[1].Add(1)
		} else {
			hits[0].Add(1)
		}
		io.WriteString(w, `{"apower":`+map[string]string{"1": "200"}[id]+`100,"voltage":230,"aenergy":{"total":1000}}`)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cfg := &Config{PollInterval: 100 * time.Millisecond, Devices: []*DeviceConfig{
		{Name: "a", UnitID: 10, Channels: []*ChannelConfig{{Host: host, Type: "shelly_switch", ID: 0}}},
		{Name: "b", UnitID: 11, Channels: []*ChannelConfig{{Host: host, Type: "shelly_switch", ID: 1}}},
	}}
	if err := cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	devs := []*Device{NewDevice(cfg.Devices[0]), NewDevice(cfg.Devices[1])}
	poller := NewPoller(devs, true, cfg.IdleAfter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poller.Run(ctx, &http.Client{Timeout: time.Second}, cfg.PollInterval)

	ms := &ModbusServer{devices: map[uint8]*Device{10: devs[0], 11: devs[1]}, staleAfter: cfg.StaleAfter, poller: poller}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go ms.Serve(ln)
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(300 * time.Millisecond)
	if hits[0].Load() != 0 || hits[1].Load() != 0 {
		t.Fatalf("vor der ersten Modbus-Anfrage darf nichts abgefragt werden: %d/%d", hits[0].Load(), hits[1].Load())
	}

	// erste Anfrage liefert direkt Werte (wartet auf die angestoßene Abfrage)
	r, ex := readRegs(t, conn, 10, 40097, 2)
	if ex != 0 {
		t.Fatalf("exception %x", ex)
	}
	if got := f32(append(make([]uint16, 97), r...), 40097); got != 100 {
		t.Fatalf("W = %v", got)
	}

	time.Sleep(350 * time.Millisecond)
	if hits[0].Load() < 2 {
		t.Errorf("aktives Gerät wird nicht regelmäßig abgefragt: %d", hits[0].Load())
	}
	if hits[1].Load() != 0 {
		t.Errorf("nicht angefragtes Gerät wurde abgefragt: %d", hits[1].Load())
	}
	if poller.Active(devs[1]) {
		t.Error("Gerät b sollte idle sein")
	}

	// Modus always: alle Geräte aktiv
	if !NewPoller(devs, false, time.Minute).Active(devs[1]) {
		t.Error("always: Gerät b sollte aktiv sein")
	}
}
