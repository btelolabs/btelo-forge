package font

import "math"

// Type 2 stack limit: 48 in the specification, 513 in CFF2 and in real fonts.
const t2StackSize = 513

// t2Run interprets one Type 2 charstring.
type t2Run struct {
	c  *CFF
	pr *cffPrivate

	st        [t2StackSize]float64
	sp        int
	trans     [32]float64
	x, y      float64
	stems     int
	ops       int
	haveWidth bool // The first stack-clearing operator has been seen.
	widthSet  bool // It carried a width.
	width     float64
	widthOnly bool
	done      bool
	inSeac    bool
	pb        pathBuilder
}

func (s *t2Run) push(v float64) error {
	if s.sp >= t2StackSize {
		return errStack
	}
	s.st[s.sp] = v
	s.sp++
	return nil
}

// takeWidth handles the optional width before the first stack-clearing operator.
func (s *t2Run) takeWidth(extra bool) {
	if s.haveWidth {
		return
	}
	s.haveWidth = true
	if extra {
		s.width = s.pr.nominalW + s.st[0]
		s.widthSet = true
		copy(s.st[:], s.st[1:s.sp])
		s.sp--
	}
	if s.widthOnly {
		s.done = true
	}
}

func subrBias(n int) int {
	switch {
	case n < 1240:
		return 107
	case n < 33900:
		return 1131
	}
	return 32768
}

func (s *t2Run) line(dx, dy float64) {
	s.x += dx
	s.y += dy
	s.pb.lineTo(s.x, s.y)
}

func (s *t2Run) curve(dx1, dy1, dx2, dy2, dx3, dy3 float64) {
	x1, y1 := s.x+dx1, s.y+dy1
	x2, y2 := x1+dx2, y1+dy2
	s.x, s.y = x2+dx3, y2+dy3
	s.pb.curveTo(x1, y1, x2, y2, s.x, s.y)
}

func (s *t2Run) move(dx, dy float64) {
	s.x += dx
	s.y += dy
	s.pb.moveTo(s.x, s.y)
}

// run interprets cs, which may be a subroutine at some depth.
func (s *t2Run) run(cs []byte, depth int) error {
	if depth > maxCallDepth {
		return errBudget
	}
	for p := 0; p < len(cs) && !s.done; {
		if s.ops++; s.ops > maxOps || s.pb.over {
			return errBudget
		}
		b := cs[p]
		p++
		switch {
		case b >= 32 && b <= 246:
			if err := s.push(float64(int(b) - 139)); err != nil {
				return err
			}
			continue
		case b >= 247 && b <= 250:
			if p >= len(cs) {
				return errTruncated
			}
			if err := s.push(float64((int(b)-247)*256 + int(cs[p]) + 108)); err != nil {
				return err
			}
			p++
			continue
		case b >= 251 && b <= 254:
			if p >= len(cs) {
				return errTruncated
			}
			if err := s.push(float64(-(int(b)-251)*256 - int(cs[p]) - 108)); err != nil {
				return err
			}
			p++
			continue
		case b == 255:
			if p+4 > len(cs) {
				return errTruncated
			}
			if err := s.push(float64(int32(u32(cs, p))) / 65536); err != nil {
				return err
			}
			p += 4
			continue
		case b == 28:
			if p+2 > len(cs) {
				return errTruncated
			}
			if err := s.push(float64(i16(cs, p))); err != nil {
				return err
			}
			p += 2
			continue
		}
		switch b {
		case 1, 3, 18, 23: // hstem, vstem, hstemhm, vstemhm
			s.takeWidth(s.sp%2 == 1)
			s.stems += s.sp / 2
			s.sp = 0
		case 19, 20: // hintmask, cntrmask
			s.takeWidth(s.sp%2 == 1)
			s.stems += s.sp / 2
			s.sp = 0
			p += (s.stems + 7) / 8
		case 21: // rmoveto
			s.takeWidth(s.sp > 2)
			if s.done {
				break
			}
			if s.sp < 2 {
				return errStack
			}
			s.move(s.st[s.sp-2], s.st[s.sp-1])
			s.sp = 0
		case 22, 4: // hmoveto, vmoveto
			s.takeWidth(s.sp > 1)
			if s.done {
				break
			}
			if s.sp < 1 {
				return errStack
			}
			if b == 22 {
				s.move(s.st[s.sp-1], 0)
			} else {
				s.move(0, s.st[s.sp-1])
			}
			s.sp = 0
		case 5: // rlineto
			for i := 0; i+2 <= s.sp; i += 2 {
				s.line(s.st[i], s.st[i+1])
			}
			s.sp = 0
		case 6, 7: // hlineto, vlineto
			horiz := b == 6
			for i := 0; i < s.sp; i++ {
				if horiz {
					s.line(s.st[i], 0)
				} else {
					s.line(0, s.st[i])
				}
				horiz = !horiz
			}
			s.sp = 0
		case 8: // rrcurveto
			for i := 0; i+6 <= s.sp; i += 6 {
				s.curve(s.st[i], s.st[i+1], s.st[i+2], s.st[i+3], s.st[i+4], s.st[i+5])
			}
			s.sp = 0
		case 24: // rcurveline
			i := 0
			for ; i+8 <= s.sp; i += 6 {
				s.curve(s.st[i], s.st[i+1], s.st[i+2], s.st[i+3], s.st[i+4], s.st[i+5])
			}
			if i+2 <= s.sp {
				s.line(s.st[i], s.st[i+1])
			}
			s.sp = 0
		case 25: // rlinecurve
			i := 0
			for ; i+8 <= s.sp; i += 2 {
				s.line(s.st[i], s.st[i+1])
			}
			if i+6 <= s.sp {
				s.curve(s.st[i], s.st[i+1], s.st[i+2], s.st[i+3], s.st[i+4], s.st[i+5])
			}
			s.sp = 0
		case 26: // vvcurveto
			i, dx := 0, 0.0
			if s.sp%2 == 1 {
				dx = s.st[0]
				i = 1
			}
			for ; i+4 <= s.sp; i += 4 {
				s.curve(dx, s.st[i], s.st[i+1], s.st[i+2], 0, s.st[i+3])
				dx = 0
			}
			s.sp = 0
		case 27: // hhcurveto
			i, dy := 0, 0.0
			if s.sp%2 == 1 {
				dy = s.st[0]
				i = 1
			}
			for ; i+4 <= s.sp; i += 4 {
				s.curve(s.st[i], dy, s.st[i+1], s.st[i+2], s.st[i+3], 0)
				dy = 0
			}
			s.sp = 0
		case 30, 31: // vhcurveto, hvcurveto
			horiz := b == 31
			for i := 0; i+4 <= s.sp; i += 4 {
				last := 0.0
				if s.sp-i == 5 {
					last = s.st[i+4]
				}
				if horiz {
					s.curve(s.st[i], 0, s.st[i+1], s.st[i+2], last, s.st[i+3])
				} else {
					s.curve(0, s.st[i], s.st[i+1], s.st[i+2], s.st[i+3], last)
				}
				horiz = !horiz
			}
			s.sp = 0
		case 10, 29: // callsubr, callgsubr
			if s.sp < 1 {
				return errStack
			}
			s.sp--
			subrs := &s.pr.subrs
			if b == 29 {
				subrs = &s.c.globalSubrs
			}
			sub := subrs.get(int(s.st[s.sp]) + subrBias(subrs.count))
			if sub == nil {
				return errStack
			}
			if err := s.run(sub, depth+1); err != nil {
				return err
			}
		case 11: // return
			return nil
		case 14: // endchar
			s.takeWidth(s.sp == 1 || s.sp == 5)
			if s.done {
				break
			}
			if s.sp >= 4 {
				if err := s.seac(); err != nil {
					return err
				}
			}
			s.pb.closePath()
			s.done = true
		case 12:
			if p >= len(cs) {
				return errTruncated
			}
			op := cs[p]
			p++
			if err := s.escape(op); err != nil {
				return err
			}
		default:
			s.sp = 0
		}
	}
	return nil
}

// seac draws the accented character of an endchar with four arguments.
func (s *t2Run) seac() error {
	if s.inSeac {
		return errBudget
	}
	adx, ady := s.st[s.sp-4], s.st[s.sp-3]
	bchar, achar := int(s.st[s.sp-2])&0xFF, int(s.st[s.sp-1])&0xFF
	s.pb.closePath()
	for i, code := range [2]int{bchar, achar} {
		gid, ok := s.c.GIDByName(StandardEncoding[code])
		if !ok {
			continue
		}
		sub := t2Run{c: s.c, pr: s.c.private(gid), inSeac: true, ops: s.ops, pb: s.pb}
		if i == 1 {
			sub.x, sub.y = adx, ady
		}
		sub.pb.moveTo(sub.x, sub.y)
		if err := sub.run(s.c.charStrings.get(gid), 0); err != nil {
			return err
		}
		sub.pb.closePath()
		s.pb = sub.pb
		s.ops = sub.ops
	}
	return nil
}

// escape runs the two-byte operators.
func (s *t2Run) escape(op byte) error {
	need := func(n int) bool { return s.sp >= n }
	switch op {
	case 34, 35, 36, 37: // Flex family.
		return s.flex(op)
	case 3, 4, 10, 11, 12, 15, 24, 28: // Two arguments.
		if !need(2) {
			return errStack
		}
		a, b := s.st[s.sp-2], s.st[s.sp-1]
		s.sp -= 2
		var r float64
		switch op {
		case 3:
			r = boolValue(a != 0 && b != 0)
		case 4:
			r = boolValue(a != 0 || b != 0)
		case 10:
			r = a + b
		case 11:
			r = a - b
		case 12:
			if b != 0 {
				r = a / b
			}
		case 15:
			r = boolValue(a == b)
		case 24:
			r = a * b
		case 28:
			s.st[s.sp] = b
			s.sp++
			r = a
		}
		s.st[s.sp] = r
		s.sp++
	case 5, 9, 14, 26: // One argument.
		if !need(1) {
			return errStack
		}
		v := s.st[s.sp-1]
		switch op {
		case 5:
			v = boolValue(v == 0)
		case 9:
			v = math.Abs(v)
		case 14:
			v = -v
		case 26:
			v = math.Sqrt(math.Abs(v))
		}
		s.st[s.sp-1] = v
	case 18: // drop
		if !need(1) {
			return errStack
		}
		s.sp--
	case 20: // put
		if !need(2) {
			return errStack
		}
		if i := int(s.st[s.sp-1]); i >= 0 && i < len(s.trans) {
			s.trans[i] = s.st[s.sp-2]
		}
		s.sp -= 2
	case 21: // get
		if !need(1) {
			return errStack
		}
		v := 0.0
		if i := int(s.st[s.sp-1]); i >= 0 && i < len(s.trans) {
			v = s.trans[i]
		}
		s.st[s.sp-1] = v
	case 22: // ifelse
		if !need(4) {
			return errStack
		}
		if s.st[s.sp-2] > s.st[s.sp-1] {
			s.st[s.sp-4] = s.st[s.sp-3]
		}
		s.sp -= 3
	case 23: // random
		return s.push(0.5)
	case 27: // dup
		if !need(1) {
			return errStack
		}
		return s.push(s.st[s.sp-1])
	case 29: // index
		if !need(1) {
			return errStack
		}
		i := int(s.st[s.sp-1])
		if i < 0 {
			i = 0
		}
		s.sp--
		if i >= s.sp {
			return errStack
		}
		return s.push(s.st[s.sp-1-i])
	case 30: // roll
		if !need(2) {
			return errStack
		}
		n, j := int(s.st[s.sp-2]), int(s.st[s.sp-1])
		s.sp -= 2
		if n < 0 || n > s.sp {
			return errStack
		}
		if n > 1 {
			s.ops += n
			j = ((j % n) + n) % n
			top := s.st[s.sp-n : s.sp]
			var tmp [t2StackSize]float64
			copy(tmp[:], top)
			for i := 0; i < n; i++ {
				top[(i+j)%n] = tmp[i]
			}
		}
	default:
		s.sp = 0
	}
	return nil
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (s *t2Run) flex(op byte) error {
	a := s.st[:s.sp]
	switch op {
	case 34: // hflex
		if len(a) < 7 {
			return errStack
		}
		s.curve(a[0], 0, a[1], a[2], a[3], 0)
		s.curve(a[4], 0, a[5], -a[2], a[6], 0)
	case 35: // flex
		if len(a) < 12 {
			return errStack
		}
		s.curve(a[0], a[1], a[2], a[3], a[4], a[5])
		s.curve(a[6], a[7], a[8], a[9], a[10], a[11])
	case 36: // hflex1
		if len(a) < 9 {
			return errStack
		}
		s.curve(a[0], a[1], a[2], a[3], a[4], 0)
		s.curve(a[5], 0, a[6], a[7], a[8], -(a[1] + a[3] + a[7]))
	case 37: // flex1
		if len(a) < 11 {
			return errStack
		}
		dx := a[0] + a[2] + a[4] + a[6] + a[8]
		dy := a[1] + a[3] + a[5] + a[7] + a[9]
		s.curve(a[0], a[1], a[2], a[3], a[4], a[5])
		if math.Abs(dx) > math.Abs(dy) {
			s.curve(a[6], a[7], a[8], a[9], a[10], -dy)
		} else {
			s.curve(a[6], a[7], a[8], a[9], -dx, a[10])
		}
	}
	s.sp = 0
	return nil
}
