package main

import (
	"math"
	"sync"
	"time"
)

// SunSpec-Registerlayout eines Fronius Smart Meters (Modbus TCP):
//
//	40000  "SunS"
//	40002  Common Model (ID 1, Länge 65)
//	40069  Meter Model 213 (Drehstrom Wye, float32, Länge 124)
//	40195  End Model (0xFFFF, 0)
const (
	RegBase      = 40000
	commonLen    = 65
	meterModelID = 213
	meterLen     = 124
	totalRegs    = 2 + 2 + commonLen + 2 + meterLen + 2 // 197
)

// Device hält den aktuellen Registersatz eines emulierten Zählers.
type Device struct {
	cfg *DeviceConfig

	mu      sync.RWMutex
	regs    []uint16
	updated time.Time

	// Energiezähler dürfen für den Wechselrichter nie rückwärts laufen
	// (z. B. nach Shelly-Reset). Pro Kanal wird ein Offset geführt.
	lastImp, lastExp []float64
	offImp, offExp   []float64
}

func NewDevice(cfg *DeviceConfig) *Device {
	n := len(cfg.Channels)
	return &Device{
		cfg:     cfg,
		lastImp: make([]float64, n), lastExp: make([]float64, n),
		offImp: make([]float64, n), offExp: make([]float64, n),
	}
}

func (d *Device) State() DeviceState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	st := DeviceState{Name: d.cfg.Name, Channels: make([]ChannelState, len(d.lastImp))}
	for i := range d.lastImp {
		st.Channels[i] = ChannelState{
			LastImportWh: d.lastImp[i], LastExportWh: d.lastExp[i],
			OffsetImportWh: d.offImp[i], OffsetExportWh: d.offExp[i],
		}
	}
	return st
}

// Restore übernimmt gespeicherte Zählerstände. Passt die Kanalanzahl nicht
// (Config geändert), wird der Stand verworfen.
func (d *Device) Restore(st DeviceState) bool {
	if len(st.Channels) != len(d.lastImp) {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, c := range st.Channels {
		d.lastImp[i], d.lastExp[i] = c.LastImportWh, c.LastExportWh
		d.offImp[i], d.offExp[i] = c.OffsetImportWh, c.OffsetExportWh
	}
	return true
}

// Read liefert count Register ab addr, ok=false wenn außerhalb des Bereichs.
func (d *Device) Read(addr, count uint16) (vals []uint16, ready bool, ok bool) {
	start := int(addr) - RegBase
	if start < 0 || start+int(count) > totalRegs {
		return nil, false, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.regs == nil {
		return nil, false, true
	}
	out := make([]uint16, count)
	copy(out, d.regs[start:start+int(count)])
	return out, true, true
}

func (d *Device) Age() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.updated.IsZero() {
		return time.Duration(math.MaxInt64)
	}
	return time.Since(d.updated)
}

type phase struct {
	V, I, W, VA, VAR, PF, Imp, Exp float64
}

func monotonic(v float64, last, off *float64) float64 {
	if v+*off < *last {
		*off = *last - v
	}
	*last = v + *off
	return *last
}

// Update berechnet die Register aus den Shelly-Messwerten.
func (d *Device) Update(readings []Reading) {
	var ph [3]phase
	freq := 0.0

	slots := []int{0, 1, 2}
	if len(readings) == 1 {
		slots = []int{map[string]int{"L1": 0, "L2": 1, "L3": 2}[d.cfg.Phase]}
	}

	d.mu.Lock()
	for i, r := range readings {
		p := &ph[slots[i]]
		p.W = r.Power
		p.V = r.Voltage
		if p.V == 0 {
			p.V = 230
		}
		p.I = r.Current
		if p.I == 0 && p.V > 0 {
			p.I = math.Abs(p.W) / p.V
		}
		p.VA = p.V * p.I
		if p.VA < math.Abs(p.W) {
			p.VA = math.Abs(p.W)
		}
		p.PF = r.PF
		if p.PF == 0 && p.VA > 0 {
			p.PF = p.W / p.VA
		}
		p.VAR = math.Sqrt(math.Max(0, p.VA*p.VA-p.W*p.W))
		p.Imp = monotonic(r.ImportWh, &d.lastImp[i], &d.offImp[i])
		p.Exp = monotonic(r.ExportWh, &d.lastExp[i], &d.offExp[i])
		if r.Freq > 0 {
			freq = r.Freq
		}
	}
	if freq == 0 {
		freq = 50
	}
	d.regs = d.build(ph, freq)
	d.updated = time.Now()
	d.mu.Unlock()
}

func (d *Device) build(ph [3]phase, freq float64) []uint16 {
	r := make([]uint16, 0, totalRegs)

	// Header + Common Model
	r = append(r, 0x5375, 0x6e53) // "SunS"
	r = append(r, 1, commonLen)
	r = appendString(r, d.cfg.Manufacturer, 16)
	r = appendString(r, d.cfg.Model, 16)
	r = appendString(r, "", 8) // Opt
	r = appendString(r, d.cfg.Version, 8)
	r = appendString(r, d.cfg.Serial, 16)
	r = append(r, uint16(d.cfg.UnitID)) // DA

	// Meter Model 213
	r = append(r, meterModelID, meterLen)
	sum := func(f func(p phase) float64) float64 { return f(ph[0]) + f(ph[1]) + f(ph[2]) }
	f := func(vals ...float64) {
		for _, v := range vals {
			r = appendFloat(r, v)
		}
	}

	active := 0
	vSum := 0.0
	for _, p := range ph {
		if p.V > 0 {
			active++
			vSum += p.V
		}
	}
	vAvg := 0.0
	if active > 0 {
		vAvg = vSum / float64(active)
	}
	ll := func(a, b phase) float64 {
		if a.V == 0 || b.V == 0 {
			return 0
		}
		return (a.V + b.V) / 2 * math.Sqrt(3)
	}
	llAB, llBC, llCA := ll(ph[0], ph[1]), ll(ph[1], ph[2]), ll(ph[2], ph[0])
	llAvg := 0.0
	if active == 3 {
		llAvg = (llAB + llBC + llCA) / 3
	}

	totW, totVA := sum(func(p phase) float64 { return p.W }), sum(func(p phase) float64 { return p.VA })
	pf := 1.0
	if totVA > 0 {
		pf = totW / totVA
	}

	f(sum(func(p phase) float64 { return p.I }), ph[0].I, ph[1].I, ph[2].I)
	f(vAvg, ph[0].V, ph[1].V, ph[2].V)
	f(llAvg, llAB, llBC, llCA)
	f(freq)
	f(totW, ph[0].W, ph[1].W, ph[2].W)
	f(totVA, ph[0].VA, ph[1].VA, ph[2].VA)
	f(sum(func(p phase) float64 { return p.VAR }), ph[0].VAR, ph[1].VAR, ph[2].VAR)
	f(pf, ph[0].PF, ph[1].PF, ph[2].PF)
	f(sum(func(p phase) float64 { return p.Exp }), ph[0].Exp, ph[1].Exp, ph[2].Exp) // TotWhExp
	f(sum(func(p phase) float64 { return p.Imp }), ph[0].Imp, ph[1].Imp, ph[2].Imp) // TotWhImp
	// TotVAhExp/Imp ≈ Wh-Werte, Blindenergie-Quadranten nicht gemessen
	f(sum(func(p phase) float64 { return p.Exp }), ph[0].Exp, ph[1].Exp, ph[2].Exp)
	f(sum(func(p phase) float64 { return p.Imp }), ph[0].Imp, ph[1].Imp, ph[2].Imp)
	for i := 0; i < 16; i++ {
		f(0)
	}
	r = append(r, 0, 0) // Evt (uint32)

	// End Model
	r = append(r, 0xFFFF, 0)

	if len(r) != totalRegs {
		panic("sunspec: falsche Registeranzahl")
	}
	return r
}

func appendFloat(r []uint16, v float64) []uint16 {
	b := math.Float32bits(float32(v))
	return append(r, uint16(b>>16), uint16(b))
}

func appendString(r []uint16, s string, regs int) []uint16 {
	b := make([]byte, regs*2)
	copy(b, s)
	for i := 0; i < regs; i++ {
		r = append(r, uint16(b[2*i])<<8|uint16(b[2*i+1]))
	}
	return r
}
