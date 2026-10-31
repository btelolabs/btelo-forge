package codec

import (
	"bytes"
	"testing"
)

// mqEncoder is the arithmetic encoder of T.88 annex E, for the tests.
type mqEncoder struct {
	a, c    uint32
	ct      int
	b       uint32
	started bool
	out     []byte
}

func newMQEncoder() *mqEncoder {
	return &mqEncoder{a: 0x8000, ct: 12}
}

func (e *mqEncoder) emit() {
	if e.started {
		e.out = append(e.out, byte(e.b))
	}
	e.started = true
}

func (e *mqEncoder) byteOut() {
	switch {
	case e.b == 0xFF && e.started:
		e.emit()
		e.b = e.c >> 20
		e.c &= 0xFFFFF
		e.ct = 7
	case e.c < 0x8000000:
		e.emit()
		e.b = e.c >> 19
		e.c &= 0x7FFFF
		e.ct = 8
	default:
		e.b++
		if e.b == 0xFF {
			e.c &= 0x7FFFFFF
			e.emit()
			e.b = e.c >> 20
			e.c &= 0xFFFFF
			e.ct = 7
		} else {
			e.emit()
			e.b = e.c >> 19
			e.c &= 0x7FFFF
			e.ct = 8
		}
	}
}

func (e *mqEncoder) renorm() {
	for {
		e.a <<= 1
		e.c <<= 1
		e.ct--
		if e.ct == 0 {
			e.byteOut()
		}
		if e.a&0x8000 != 0 {
			return
		}
	}
}

func (e *mqEncoder) encode(cx *uint8, d int) {
	s := &mqSteps[*cx]
	e.a -= s.qe
	if d == int(*cx&1) {
		if e.a&0x8000 == 0 {
			if e.a < s.qe {
				e.a = s.qe
			} else {
				e.c += s.qe
			}
			*cx = s.mps
			e.renorm()
		} else {
			e.c += s.qe
		}
		return
	}
	if e.a < s.qe {
		e.c += s.qe
	} else {
		e.a = s.qe
	}
	*cx = s.lps
	e.renorm()
}

// flush ends the codeword and returns it, without a final 0xFF.
func (e *mqEncoder) flush() []byte {
	temp := e.c + e.a
	e.c |= 0xFFFF
	if e.c >= temp {
		e.c -= 0x8000
	}
	e.c <<= e.ct
	e.byteOut()
	e.c <<= e.ct
	e.byteOut()
	if e.b != 0xFF {
		e.emit()
	}
	return e.out
}

// flushMarker ends the codeword as a JBIG2 segment does, with the 0xFF 0xAC marker.
func (e *mqEncoder) flushMarker() []byte {
	return append(e.flush(), 0xFF, 0xAC)
}

// The test sequence of T.88 H.2, also used by jbig2dec.
var (
	h2Coded = []byte{
		0x84, 0xC7, 0x3B, 0xFC, 0xE1, 0xA1, 0x43, 0x04, 0x02, 0x20, 0x00, 0x00, 0x41, 0x0D, 0xBB,
		0x86, 0xF4, 0x31, 0x7F, 0xFF, 0x88, 0xFF, 0x37, 0x47, 0x1A, 0xDB, 0x6A, 0xDF, 0xFF, 0xAC,
	}
	h2Plain = []byte{
		0x00, 0x02, 0x00, 0x51, 0x00, 0x00, 0x00, 0xC0, 0x03, 0x52, 0x87, 0x2A, 0xAA, 0xAA, 0xAA, 0xAA,
		0x82, 0xC0, 0x20, 0x00, 0xFC, 0xD7, 0x9E, 0xF6, 0xBF, 0x7F, 0xED, 0x90, 0x4F, 0x46, 0xA3, 0xBF,
	}
)

func TestMQAnnexH2(t *testing.T) {
	var d mqDecoder
	d.init(h2Coded)
	var cx uint8
	got := make([]byte, len(h2Plain))
	for i := range len(h2Plain) * 8 {
		got[i/8] |= byte(d.decode(&cx)) << (7 - i%8)
	}
	if !bytes.Equal(got, h2Plain) {
		t.Errorf("decoded % X", got)
	}
	e := newMQEncoder()
	cx = 0
	for i := range len(h2Plain) * 8 {
		e.encode(&cx, int(h2Plain[i/8]>>(7-i%8))&1)
	}
	if coded := e.flushMarker(); !bytes.Equal(coded, h2Coded) {
		t.Errorf("encoded % X", coded)
	}
}

func TestMQPastTheEnd(t *testing.T) {
	var d mqDecoder
	d.init(nil)
	var cx uint8
	for range 100000 {
		d.decode(&cx)
	}
}
