package main

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"eto":6849764,"nrg":[230,231,232,0,16,16,16,3680,3696,3712,0,11088,99,98,97,0]}`)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cfg := &Config{Devices: []*DeviceConfig{
		{Name: "wb3", UnitID: 1, Channels: []*ChannelConfig{
			{Host: host, Type: "goe", SourcePhase: "1"},
			{Host: host, Type: "goe", SourcePhase: "2"},
			{Host: host, Type: "goe", SourcePhase: "3"},
		}},
		{Name: "wb1", UnitID: 2, Channels: []*ChannelConfig{{Host: host, Type: "goe"}}},
	}}
	if err := cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	devs := []*Device{NewDevice(cfg.Devices[0]), NewDevice(cfg.Devices[1])}
	poll(context.Background(), &http.Client{Timeout: time.Second}, devs)

	for _, tc := range []struct {
		dev        *Device
		w, wL2, wh float64
	}{
		{devs[0], 11088, 3696, 6849764},
		{devs[1], 11088, 0, 6849764},
	} {
		vals, ready, _ := tc.dev.Read(RegBase, totalRegs)
		if !ready {
			t.Fatalf("%s: keine Werte", tc.dev.cfg.Name)
		}
		if got := f32(vals, 40097); got != tc.w {
			t.Errorf("%s: W = %v, erwartet %v", tc.dev.cfg.Name, got, tc.w)
		}
		if got := f32(vals, 40101); got != tc.wL2 {
			t.Errorf("%s: W L2 = %v, erwartet %v", tc.dev.cfg.Name, got, tc.wL2)
		}
		if got := f32(vals, 40137); math.Abs(got-tc.wh) > 1 { // float32-Genauigkeit
			t.Errorf("%s: TotWhImp = %v, erwartet %v", tc.dev.cfg.Name, got, tc.wh)
		}
	}
	if got := f32(mustRead(t, devs[0]), 40125); math.Abs(got-0.98) > 0.001 {
		t.Errorf("PF L2 = %v", got)
	}
}

func mustRead(t *testing.T, d *Device) []uint16 {
	vals, _, _ := d.Read(RegBase, totalRegs)
	return vals
}
