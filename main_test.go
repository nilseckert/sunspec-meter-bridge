package main

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeShelly(t *testing.T) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc/EM.GetStatus", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"id":0,"a_act_power":1000,"a_voltage":231,"a_current":4.5,"a_pf":0.96,"a_freq":50.01,
			"b_act_power":500,"b_voltage":232,"b_current":2.3,"b_pf":0.94,
			"c_act_power":-200,"c_voltage":233,"c_current":1.0,"c_pf":0.9}`)
	})
	mux.HandleFunc("/rpc/EMData.GetStatus", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"id":0,"a_total_act_energy":10000,"a_total_act_ret_energy":10,
			"b_total_act_energy":5000,"b_total_act_ret_energy":20,"c_total_act_energy":2000,"c_total_act_ret_energy":300}`)
	})
	mux.HandleFunc("/rpc/Switch.GetStatus", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"id":0,"apower":123.4,"voltage":229.5,"current":0.6,"aenergy":{"total":4567.8}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func readRegs(t *testing.T, conn net.Conn, unit byte, addr, count uint16) ([]uint16, byte) {
	req := make([]byte, 12)
	binary.BigEndian.PutUint16(req[0:], 7)
	binary.BigEndian.PutUint16(req[4:], 6)
	req[6] = unit
	req[7] = 3
	binary.BigEndian.PutUint16(req[8:], addr)
	binary.BigEndian.PutUint16(req[10:], count)
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 7)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatal(err)
	}
	pdu := make([]byte, binary.BigEndian.Uint16(hdr[4:])-1)
	io.ReadFull(conn, pdu)
	if pdu[0]&0x80 != 0 {
		return nil, pdu[1]
	}
	out := make([]uint16, pdu[1]/2)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(pdu[2+2*i:])
	}
	return out, 0
}

func f32(r []uint16, reg int) float64 {
	i := reg - RegBase
	return float64(math.Float32frombits(uint32(r[i])<<16 | uint32(r[i+1])))
}

func TestEndToEnd(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	host := fakeShelly(t)
	cfg := &Config{Devices: []*DeviceConfig{
		{Name: "wp", UnitID: 1, Channels: []*ChannelConfig{
			{Host: host, Type: "em", EMPhase: "a"}, {Host: host, Type: "em", EMPhase: "b"}, {Host: host, Type: "em", EMPhase: "c"},
		}},
		{Name: "sw", UnitID: 2, Phase: "l2", Channels: []*ChannelConfig{{Host: host, Type: "switch"}}},
	}}
	if err := cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	devs := []*Device{NewDevice(cfg.Devices[0]), NewDevice(cfg.Devices[1])}
	srv := &ModbusServer{devices: map[uint8]*Device{1: devs[0], 2: devs[1]}, staleAfter: time.Minute}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go srv.Serve(ln)
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// vor dem ersten Poll: Exception
	if _, ex := readRegs(t, conn, 1, 40000, 2); ex != exGatewayNoTarget {
		t.Fatalf("erwartet Exception 0x0B, bekam %x", ex)
	}

	poll(context.Background(), &http.Client{Timeout: time.Second}, devs)

	a, ex := readRegs(t, conn, 1, 40000, 125)
	if ex != 0 {
		t.Fatalf("exception %x", ex)
	}
	b, _ := readRegs(t, conn, 1, 40125, 72)
	r := append(a, b...)

	if r[0] != 0x5375 || r[1] != 0x6e53 || r[69] != 213 || r[70] != 124 || r[195] != 0xFFFF {
		t.Fatalf("SunSpec-Header falsch: %v", r[:4])
	}
	if got := f32(r, 40097); got != 1300 {
		t.Errorf("W gesamt = %v", got)
	}
	if got := f32(r, 40103); got != -200 {
		t.Errorf("W L3 = %v", got)
	}
	if got := f32(r, 40137); got != 17000 {
		t.Errorf("TotWhImp = %v", got)
	}
	if got := f32(r, 40129); got != 330 {
		t.Errorf("TotWhExp = %v", got)
	}

	s, _ := readRegs(t, conn, 2, 40071, 124)
	if got := f32(append(make([]uint16, 71), s...), 40101); math.Abs(got-123.4) > 0.01 {
		t.Errorf("Switch W L2 = %v", got)
	}
	if got := f32(append(make([]uint16, 71), s...), 40097); math.Abs(got-123.4) > 0.01 {
		t.Errorf("Switch W gesamt = %v", got)
	}

	if _, ex := readRegs(t, conn, 9, 40000, 2); ex != exGatewayNoTarget {
		t.Errorf("unbekannte Unit: %x", ex)
	}
	if _, ex := readRegs(t, conn, 1, 40190, 10); ex != exIllegalAddress {
		t.Errorf("außerhalb: %x", ex)
	}
}
