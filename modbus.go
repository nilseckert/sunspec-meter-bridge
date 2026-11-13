package main

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"time"
)

const (
	exIllegalFunction = 0x01
	exIllegalAddress  = 0x02
	exIllegalValue    = 0x03
	exGatewayNoTarget = 0x0B
)

// ModbusServer bedient alle Geräte auf einem TCP-Port, unterschieden per Unit-ID.
type ModbusServer struct {
	port       int
	devices    map[uint8]*Device
	staleAfter time.Duration
	// optional: Polling auf Bedarf
	poller *Poller
}

func (s *ModbusServer) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *ModbusServer) handle(conn net.Conn) {
	defer conn.Close()
	log := slog.With("port", s.port, "remote", conn.RemoteAddr().String())
	log.Debug("Verbindung geöffnet")

	hdr := make([]byte, 7)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		if _, err := io.ReadFull(conn, hdr); err != nil {
			if !errors.Is(err, io.EOF) {
				log.Debug("Verbindung beendet", "err", err)
			}
			return
		}
		txID := binary.BigEndian.Uint16(hdr[0:2])
		proto := binary.BigEndian.Uint16(hdr[2:4])
		length := binary.BigEndian.Uint16(hdr[4:6])
		unit := hdr[6]
		if proto != 0 || length < 2 || length > 256 {
			log.Warn("ungültiger MBAP-Header", "proto", proto, "len", length)
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}

		resp := s.process(unit, pdu, log)
		out := make([]byte, 7+len(resp))
		binary.BigEndian.PutUint16(out[0:2], txID)
		binary.BigEndian.PutUint16(out[4:6], uint16(len(resp)+1))
		out[6] = unit
		copy(out[7:], resp)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

func exception(fc, code byte) []byte { return []byte{fc | 0x80, code} }

func (s *ModbusServer) process(unit byte, pdu []byte, log *slog.Logger) []byte {
	fc := pdu[0]
	dev, ok := s.devices[unit]
	if !ok {
		log.Debug("unbekannte Unit-ID", "unit", unit)
		return exception(fc, exGatewayNoTarget)
	}
	switch fc {
	case 0x03, 0x04:
		if len(pdu) != 5 {
			return exception(fc, exIllegalValue)
		}
		addr := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count == 0 || count > 125 {
			return exception(fc, exIllegalValue)
		}
		if s.poller != nil {
			s.poller.Touch(dev, s.staleAfter)
			// erste Anfrage nach Leerlauf: kurz auf die angestoßene Abfrage warten
			s.poller.WaitFresh(dev, s.staleAfter, 2*time.Second)
		}
		if dev.Age() > s.staleAfter {
			return exception(fc, exGatewayNoTarget)
		}
		vals, ready, inRange := dev.Read(addr, count)
		if !inRange {
			log.Debug("Adresse außerhalb", "device", dev.cfg.Name, "addr", addr, "count", count)
			return exception(fc, exIllegalAddress)
		}
		if !ready {
			return exception(fc, exGatewayNoTarget)
		}
		resp := make([]byte, 2+2*len(vals))
		resp[0] = fc
		resp[1] = byte(2 * len(vals))
		for i, v := range vals {
			binary.BigEndian.PutUint16(resp[2+2*i:], v)
		}
		return resp
	default:
		return exception(fc, exIllegalFunction)
	}
}
