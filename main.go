package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// wird beim Build per -ldflags "-X main.version=..." gesetzt
var version = "dev"

func main() {
	cfgPath := flag.String("config", envOr("CONFIG", "/config/config.yaml"), "Pfad zur YAML-Config")
	healthcheck := flag.Bool("healthcheck", false, "/healthz des laufenden Prozesses prüfen (für Docker HEALTHCHECK)")
	showVersion := flag.Bool("version", false, "Version ausgeben")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	slog.Info("sunspec-meter-bridge", "version", version)

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		slog.Error("Config ungültig", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	devices := make([]*Device, 0, len(cfg.Devices))
	servers := map[int]*ModbusServer{}
	for _, dc := range cfg.Devices {
		d := NewDevice(dc)
		devices = append(devices, d)
		srv, ok := servers[dc.Port]
		if !ok {
			srv = &ModbusServer{port: dc.Port, devices: map[uint8]*Device{}, staleAfter: cfg.StaleAfter}
			servers[dc.Port] = srv
		}
		srv.devices[dc.UnitID] = d
		slog.Info("Gerät", "name", dc.Name, "port", dc.Port, "unit_id", dc.UnitID,
			"serial", dc.Serial, "channels", len(dc.Channels))
	}

	var sw *stateWriter
	if cfg.StateFile != "" {
		st, err := LoadState(cfg.StateFile)
		if err != nil {
			slog.Error("State-Datei nicht lesbar", "file", cfg.StateFile, "err", err)
			os.Exit(1)
		}
		for _, d := range devices {
			if ds, ok := st[d.cfg.Serial]; ok {
				if d.Restore(ds) {
					slog.Info("Zählerstand geladen", "device", d.cfg.Name)
				} else {
					slog.Warn("Zählerstand verworfen (Kanalanzahl geändert)", "device", d.cfg.Name)
				}
			}
		}
		sw = &stateWriter{path: cfg.StateFile, last: collectState(devices)}
		// Schreibbarkeit früh prüfen statt erst nach dem ersten Intervall.
		if err := SaveState(cfg.StateFile, sw.last); err != nil {
			slog.Error("State-Datei nicht schreibbar", "file", cfg.StateFile, "err", err)
			os.Exit(1)
		}
	}
	saveState := func() {
		if sw == nil {
			return
		}
		if err := sw.save(devices); err != nil {
			slog.Error("Zählerstand speichern fehlgeschlagen", "err", err)
		}
	}

	for port, srv := range servers {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			slog.Error("Listen fehlgeschlagen", "port", port, "err", err)
			os.Exit(1)
		}
		go func() { <-ctx.Done(); ln.Close() }()
		go func() {
			if err := srv.Serve(ln); err != nil && ctx.Err() == nil {
				slog.Error("Modbus-Server beendet", "port", srv.port, "err", err)
				os.Exit(1)
			}
		}()
		slog.Info("Modbus TCP lauscht", "port", port)
	}

	go serveHealth(ctx, devices, cfg.StaleAfter)

	client := &http.Client{Timeout: cfg.HTTPTimeout}
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	stateTicker := time.NewTicker(cfg.StateInterval)
	defer stateTicker.Stop()
	poll(ctx, client, devices)
	for {
		select {
		case <-ctx.Done():
			saveState()
			slog.Info("beendet")
			return
		case <-stateTicker.C:
			saveState()
		case <-ticker.C:
			poll(ctx, client, devices)
		}
	}
}

func poll(ctx context.Context, client *http.Client, devices []*Device) {
	f := newFetcher(client)
	var wg sync.WaitGroup
	for _, d := range devices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			readings := make([]Reading, len(d.cfg.Channels))
			var cwg sync.WaitGroup
			errs := make([]error, len(d.cfg.Channels))
			for i, ch := range d.cfg.Channels {
				cwg.Add(1)
				go func() {
					defer cwg.Done()
					readings[i], errs[i] = readChannel(ctx, f, ch)
				}()
			}
			cwg.Wait()
			for _, err := range errs {
				if err != nil {
					slog.Warn("Shelly-Abfrage fehlgeschlagen", "device", d.cfg.Name, "err", err)
					return
				}
			}
			d.Update(readings)
			total := 0.0
			for _, r := range readings {
				total += r.Power
			}
			slog.Debug("aktualisiert", "device", d.cfg.Name, "W", total)
		}()
	}
	wg.Wait()
}

// /healthz: 200 wenn alle Geräte aktuelle Werte haben.
func serveHealth(ctx context.Context, devices []*Device, staleAfter time.Duration) {
	addr := envOr("HEALTH_ADDR", ":8080")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		ok := true
		var b strings.Builder
		for _, d := range devices {
			age := d.Age()
			state := "ok"
			if age > staleAfter {
				state, ok = "stale", false
			}
			fmt.Fprintf(&b, "%s: %s\n", d.cfg.Name, state)
		}
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = w.Write([]byte(b.String()))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
		slog.Error("Health-Server", "err", err)
	}
}

func runHealthcheck() int {
	host, port, err := net.SplitHostPort(envOr("HEALTH_ADDR", ":8080"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthz:", resp.Status)
		return 1
	}
	return 0
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
