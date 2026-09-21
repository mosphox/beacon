// Package h2fp computes the Akamai HTTP/2 fingerprint.
//
// The fingerprint is built from what a client says at the very start of an
// HTTP/2 connection — its SETTINGS values and their order, the initial
// connection-level WINDOW_UPDATE, any PRIORITY frames, and the order of the
// pseudo-headers in its first request. None of that is exposed by net/http, so
// the frames are read off the decrypted stream as they pass.
//
// The usual approach is to fork golang.org/x/net/http2 to reach its frame
// loop. That is not necessary: http2.Server.ServeConn is public and takes any
// net.Conn, so a connection wrapper can observe the bytes on their way through
// and hand the real server an untouched stream.
//
// Format:
//
//	SETTINGS|WINDOW_UPDATE|PRIORITY|PSEUDO_HEADER_ORDER
//	3:100;4:10485760;2:0|1048510465|0|m,s,a,p
package h2fp

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/net/http2/hpack"
)

// preface is the fixed string every HTTP/2 client sends first (RFC 9113 §3.4).
const preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

// frameHeaderLen is the fixed 9-byte header on every frame.
const frameHeaderLen = 9

// maxSniff bounds how much of the stream is buffered while looking for the
// first request. A client that has not identified itself within this much has
// given up its chance to be fingerprinted.
const maxSniff = 1 << 16

const (
	frameData         = 0x0
	frameHeaders      = 0x1
	framePriority     = 0x2
	frameSettings     = 0x4
	frameWindowUpdate = 0x8
	frameContinuation = 0x9

	flagAck        = 0x1
	flagEndHeaders = 0x4
	flagPadded     = 0x8
	flagPriority   = 0x20
)

type Setting struct {
	ID    uint16
	Value uint32
}

type Priority struct {
	StreamID  uint32
	Exclusive bool
	DependsOn uint32
	Weight    uint16
}

// Fingerprint is one client's HTTP/2 identity.
type Fingerprint struct {
	Raw  string // the Akamai string
	Hash string // its MD5

	Settings          []Setting
	WindowUpdate      uint32
	Priorities        []Priority
	PseudoHeaderOrder []string
}

// build renders the Akamai string and hashes it.
func (f *Fingerprint) build() {
	settings := make([]string, 0, len(f.Settings))
	for _, s := range f.Settings {
		settings = append(settings, fmt.Sprintf("%d:%d", s.ID, s.Value))
	}

	window := "0"
	if f.WindowUpdate != 0 {
		window = fmt.Sprintf("%d", f.WindowUpdate)
	}

	priorities := "0"
	if len(f.Priorities) > 0 {
		parts := make([]string, 0, len(f.Priorities))
		for _, p := range f.Priorities {
			excl := 0
			if p.Exclusive {
				excl = 1
			}
			parts = append(parts, fmt.Sprintf("%d:%d:%d:%d", p.StreamID, excl, p.DependsOn, p.Weight))
		}
		priorities = strings.Join(parts, ",")
	}

	f.Raw = strings.Join([]string{
		strings.Join(settings, ";"),
		window,
		priorities,
		strings.Join(f.PseudoHeaderOrder, ","),
	}, "|")

	sum := md5.Sum([]byte(f.Raw))
	f.Hash = hex.EncodeToString(sum[:])
}

// parser consumes the start of an HTTP/2 stream frame by frame. It is fed
// whatever arrives, in whatever sized pieces, and reports done once the first
// request's headers have been seen.
type parser struct {
	buf []byte

	sawPreface bool
	headerBlk  []byte
	inHeaders  bool

	fp   Fingerprint
	done bool
	give bool // stop trying: malformed, or too much data without a request
}

func (p *parser) feed(b []byte) {
	if p.done || p.give {
		return
	}
	p.buf = append(p.buf, b...)
	if len(p.buf) > maxSniff {
		p.give = true
		p.buf = nil
		return
	}

	if !p.sawPreface {
		if len(p.buf) < len(preface) {
			return
		}
		if string(p.buf[:len(preface)]) != preface {
			p.give = true
			p.buf = nil
			return
		}
		p.sawPreface = true
		p.buf = p.buf[len(preface):]
	}

	for !p.done && !p.give {
		if len(p.buf) < frameHeaderLen {
			return
		}
		size := int(p.buf[0])<<16 | int(p.buf[1])<<8 | int(p.buf[2])
		typ := p.buf[3]
		flags := p.buf[4]
		streamID := binary.BigEndian.Uint32(p.buf[5:9]) & 0x7fffffff

		if len(p.buf) < frameHeaderLen+size {
			return
		}
		payload := p.buf[frameHeaderLen : frameHeaderLen+size]
		p.frame(typ, flags, streamID, payload)
		// frame may have finished the job and released the buffer, so check
		// before advancing into it.
		if p.done || p.give {
			p.buf = nil
			return
		}
		p.buf = p.buf[frameHeaderLen+size:]
	}
}

func (p *parser) frame(typ, flags byte, streamID uint32, payload []byte) {
	switch typ {
	case frameSettings:
		if flags&flagAck != 0 {
			return
		}
		for len(payload) >= 6 {
			p.fp.Settings = append(p.fp.Settings, Setting{
				ID:    binary.BigEndian.Uint16(payload[0:2]),
				Value: binary.BigEndian.Uint32(payload[2:6]),
			})
			payload = payload[6:]
		}

	case frameWindowUpdate:
		// Only the connection-level update is part of the fingerprint.
		if streamID == 0 && len(payload) >= 4 {
			p.fp.WindowUpdate = binary.BigEndian.Uint32(payload[0:4]) & 0x7fffffff
		}

	case framePriority:
		if len(payload) >= 5 {
			p.fp.Priorities = append(p.fp.Priorities, priorityFrom(streamID, payload))
		}

	case frameHeaders:
		block := payload
		if flags&flagPadded != 0 {
			if len(block) < 1 {
				p.give = true
				return
			}
			pad := int(block[0])
			block = block[1:]
			if pad > len(block) {
				p.give = true
				return
			}
			block = block[:len(block)-pad]
		}
		if flags&flagPriority != 0 {
			if len(block) < 5 {
				p.give = true
				return
			}
			p.fp.Priorities = append(p.fp.Priorities, priorityFrom(streamID, block))
			block = block[5:]
		}
		p.headerBlk = append(p.headerBlk, block...)
		p.inHeaders = true
		if flags&flagEndHeaders != 0 {
			p.finishHeaders()
		}

	case frameContinuation:
		if !p.inHeaders {
			return
		}
		p.headerBlk = append(p.headerBlk, payload...)
		if flags&flagEndHeaders != 0 {
			p.finishHeaders()
		}

	case frameData:
		// A request body before any headers is nonsense; stop looking.
		p.give = true
	}
}

func priorityFrom(streamID uint32, b []byte) Priority {
	dep := binary.BigEndian.Uint32(b[0:4])
	return Priority{
		StreamID:  streamID,
		Exclusive: dep&0x80000000 != 0,
		DependsOn: dep & 0x7fffffff,
		// The wire carries weight-1, so a weight of 16 is sent as 15.
		Weight: uint16(b[4]) + 1,
	}
}

// finishHeaders decodes the first request's header block for its pseudo-header
// order.
//
// Decoding standalone is sound because this is the first HEADERS frame on the
// connection, where HPACK's dynamic table is still empty; indexed fields can
// only refer to the static table.
func (p *parser) finishHeaders() {
	dec := hpack.NewDecoder(4096, nil)
	var order []string
	dec.SetEmitFunc(func(hf hpack.HeaderField) {
		if strings.HasPrefix(hf.Name, ":") && len(hf.Name) > 1 {
			order = append(order, hf.Name[1:2])
		}
	})
	if _, err := dec.Write(p.headerBlk); err != nil {
		p.give = true
		return
	}

	p.fp.PseudoHeaderOrder = order
	p.fp.build()
	p.done = true
	p.headerBlk = nil
}
