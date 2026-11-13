package main

import (
	"context"
	"fmt"
)

// readGoe liest einen go-eCharger (V3/V4, lokale HTTP-API v2 aktiviert).
//
// nrg = [U1, U2, U3, UN, I1, I2, I3, P1, P2, P3, PN, Ptotal, pf1, pf2, pf3, pfN]
// eto = Gesamtenergie in Wh (ein Zähler für alle Phasen)
func readGoe(ctx context.Context, f *fetcher, ch *ChannelConfig) (Reading, error) {
	var r Reading
	s, err := f.get(ctx, "http://"+ch.Host+"/api/status?filter=nrg,eto")
	if err != nil {
		return r, err
	}
	arr, ok := s["nrg"].([]any)
	if !ok || len(arr) < 16 {
		return r, fmt.Errorf("%s: nrg fehlt oder unvollständig – lokale HTTP-API v2 aktiviert?", ch.Host)
	}
	nrg := make([]float64, len(arr))
	for i, v := range arr {
		nrg[i], _ = v.(float64)
	}
	eto, ok := num(s, "eto")
	if !ok {
		return r, fmt.Errorf("%s: eto fehlt", ch.Host)
	}

	pf := func(v float64) float64 {
		if v > 1 || v < -1 { // go-e liefert den Leistungsfaktor in Prozent
			return v / 100
		}
		return v
	}

	if i := ch.phaseIdx; i >= 0 {
		r.Voltage = nrg[i]
		r.Current = nrg[4+i]
		r.Power = nrg[7+i]
		r.PF = pf(nrg[12+i])
	} else {
		// ohne Phase: Summe für einen einphasig gemeldeten Zähler
		r.Power = nrg[11]
		r.Current = nrg[4] + nrg[5] + nrg[6]
		n := 0.0
		for i := 0; i < 3; i++ {
			if nrg[i] > 0 {
				r.Voltage += nrg[i]
				n++
			}
		}
		if n > 0 {
			r.Voltage /= n
		}
	}
	r.ImportWh = eto
	return r, nil
}
