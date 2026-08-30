package chunkcodec

// Pure-Go LZMA1 raw-stream decoder — enough for Steam's legacy VZa chunks.
// Faithful port of Igor Pavlov's public-domain LZMA SDK (LzmaDec.c).
// props[0] = (pb*5+lp)*9+lc, props[1..4] = dictSize LE.

import (
	"fmt"
)

const (
	lzStateNum     = 12
	lzMaxPB        = 4
	lzMatchMinLen  = 2
	lzLenToPos     = 4
	lzAlignBits    = 4
	lzAlignSize    = 1 << lzAlignBits
	lzTopValue     = 1 << 24
	lzBitModelBits = 11
	lzBitModelTot  = 1 << lzBitModelBits
	lzMoveBits     = 5

	lzPosSlotBits   = 6
	lzLenLowBits    = 3
	lzLenMidBits    = 3
	lzLenHighBits   = 8
	lzLenChoice     = 0
	lzLenChoice2    = 1
	lzLenLow        = 2
	lzLenMid        = lzLenLow + (1 << lzLenLowBits)
	lzLenHigh       = lzLenMid + (1 << lzLenMidBits)
	lzNumLenProbs   = lzLenHigh + (1 << lzLenHighBits)
	lzFullDistances = 1 << 14 // kEndPosModelIndex
)

type lzDecoder struct {
	lc, lp, pb int
	out        []byte
	state      int
	reps       [4]uint32
	rc         *lzRangeCoder

	isMatch    []uint16 // [lzStateNum << pb]
	isRep      []uint16 // [lzStateNum]
	isRepG0    []uint16
	isRepG1    []uint16
	isRepG2    []uint16
	isRep0Long []uint16 // [lzStateNum << pb]
	posSlot    []uint16 // [lzLenToPos << lzPosSlotBits]
	specPos    []uint16 // [lzFullDistances - kEndPosModelIndex + ... ] classic 114+1
	alignP     []uint16 // [lzAlignSize]
	lenP       []uint16 // [lzNumLenProbs]
	repLenP    []uint16 // [lzNumLenProbs]
	litP       []uint16 // [0x300 << (lc+lp)]
}

func lzma1Decode(props, payload []byte, wantSize int64) ([]byte, error) {
	if len(props) < 5 {
		return nil, fmt.Errorf("lzma props too short")
	}
	p0 := int(props[0])
	lc := p0 % 9
	lp := (p0 / 9) % 5
	pb := (p0 / 9) / 5
	if lc > 8 || lp > 4 || pb > lzMaxPB {
		return nil, fmt.Errorf("lzma bad props lc=%d lp=%d pb=%d", lc, lp, pb)
	}

	capHint := int64(1 << 20)
	if wantSize > 0 && wantSize <= 64<<20 {
		capHint = wantSize
	}
	d := &lzDecoder{
		lc: lc, lp: lp, pb: pb,
		out: make([]byte, 0, capHint),
		rc:  newLZRangeCoder(payload),

		isMatch:    lzProbs(lzStateNum << pb),
		isRep:      lzProbs(lzStateNum),
		isRepG0:    lzProbs(lzStateNum),
		isRepG1:    lzProbs(lzStateNum),
		isRepG2:    lzProbs(lzStateNum),
		isRep0Long: lzProbs(lzStateNum << pb),
		posSlot:    lzProbs(lzLenToPos << lzPosSlotBits),
		specPos:    lzProbs(lzFullDistances - 14 + 1), // classic layout
		alignP:     lzProbs(lzAlignSize),
		lenP:       lzProbs(lzNumLenProbs),
		repLenP:    lzProbs(lzNumLenProbs),
		litP:       lzProbs(0x300 << (lc + lp)),
	}

	if err := d.run(wantSize); err != nil {
		return nil, err
	}
	return d.out, nil
}

func lzProbs(n int) []uint16 {
	p := make([]uint16, n)
	for i := range p {
		p[i] = lzBitModelTot / 2
	}
	return p
}

type lzRangeCoder struct {
	buf       []byte
	pos       int
	code      uint32
	rang      uint32
	corrupted bool
}

func newLZRangeCoder(buf []byte) *lzRangeCoder {
	rc := &lzRangeCoder{buf: buf, rang: 0xFFFFFFFF}
	rc.readByte() // first byte is discarded per spec
	rc.code = 0
	for i := 0; i < 4; i++ {
		rc.code = (rc.code << 8) | rc.readByte()
	}
	return rc
}

func (rc *lzRangeCoder) readByte() uint32 {
	if rc.pos >= len(rc.buf) {
		rc.corrupted = true
		return 0
	}
	b := rc.buf[rc.pos]
	rc.pos++
	return uint32(b)
}

func (rc *lzRangeCoder) normalize() {
	if rc.rang < lzTopValue {
		rc.rang <<= 8
		rc.code = (rc.code << 8) | rc.readByte()
	}
}

func (rc *lzRangeCoder) bit(probs []uint16, idx int) int {
	prob := uint32(probs[idx])
	bound := (rc.rang >> lzBitModelBits) * prob
	var b int
	if rc.code < bound {
		rc.rang = bound
		probs[idx] = uint16(prob + ((lzBitModelTot - prob) >> lzMoveBits))
		b = 0
	} else {
		rc.rang -= bound
		rc.code -= bound
		probs[idx] = uint16(prob - (prob >> lzMoveBits))
		b = 1
	}
	rc.normalize()
	return b
}

func (rc *lzRangeCoder) direct(nbits int) uint32 {
	var res uint32
	for i := nbits; i > 0; i-- {
		rc.rang >>= 1
		rc.code -= rc.rang
		t := uint32(0) - (rc.code >> 31)
		rc.code += rc.rang & t
		res = (res << 1) + (t + 1)
		rc.normalize()
	}
	return res
}

func (rc *lzRangeCoder) bitTree(probs []uint16, nbits int) int {
	m := 1
	for i := 0; i < nbits; i++ {
		m = (m << 1) | rc.bit(probs, m)
	}
	return m - (1 << nbits)
}

func (rc *lzRangeCoder) revBitTree(probs []uint16, nbits int) int {
	m := 1
	res := 0
	for i := 0; i < nbits; i++ {
		b := rc.bit(probs, m)
		m = (m << 1) | b
		res |= b << i
	}
	return res
}

func (d *lzDecoder) lenDecode(probs []uint16, posState int) int {
	if d.rc.bit(probs, lzLenChoice) == 0 {
		return lzMatchMinLen + d.rc.bitTree(probs[lzLenLow+posState*(1<<lzLenLowBits):], lzLenLowBits)
	}
	if d.rc.bit(probs, lzLenChoice2) == 0 {
		return lzMatchMinLen + (1 << lzLenLowBits) + d.rc.bitTree(probs[lzLenMid+posState*(1<<lzLenMidBits):], lzLenMidBits)
	}
	return lzMatchMinLen + (1 << lzLenLowBits) + (1 << lzLenMidBits) + d.rc.bitTree(probs[lzLenHigh:], lzLenHighBits)
}

func (d *lzDecoder) run(wantSize int64) error {
	pb := d.pb
	posMask := (1 << pb) - 1
	lpMask := (1 << d.lp) - 1

	for {
		if wantSize > 0 && int64(len(d.out)) >= wantSize {
			return nil
		}
		if d.rc.corrupted {
			return fmt.Errorf("lzma stream corrupted")
		}
		posState := len(d.out) & posMask

		if d.rc.bit(d.isMatch, (d.state<<pb)+posState) == 0 {
			// --- literal ---
			prev := byte(0)
			if len(d.out) > 0 {
				prev = d.out[len(d.out)-1]
			}
			litState := ((len(d.out) & lpMask) << d.lc) + (int(prev) >> (8 - d.lc))
			probs := d.litP[0x300*litState:]
			sym := 1
			if d.state >= 7 {
				mi := len(d.out) - int(d.reps[0]) - 1
				matchByte := byte(0)
				if mi >= 0 && mi < len(d.out) {
					matchByte = d.out[mi]
				}
				for sym < 0x100 {
					matchBit := int(matchByte >> 7)
					matchByte <<= 1
					b := d.rc.bit(probs, ((1+matchBit)<<8)+sym)
					sym = (sym << 1) | b
					if matchBit != b {
						break
					}
				}
			}
			for sym < 0x100 {
				sym = (sym << 1) | d.rc.bit(probs, sym)
			}
			d.out = append(d.out, byte(sym))
			switch {
			case d.state < 4:
				d.state = 0
			case d.state < 10:
				d.state -= 3
			default:
				d.state -= 6
			}
			continue
		}

		// --- match ---
		var length int
		if d.rc.bit(d.isRep, d.state) != 0 {
			// rep match
			if d.rc.bit(d.isRepG0, d.state) == 0 {
				if d.rc.bit(d.isRep0Long, (d.state<<pb)+posState) == 0 {
					// short rep: 1 byte at reps[0]
					if d.state < 7 {
						d.state = 9
					} else {
						d.state = 11
					}
					d.copyMatch(1)
					continue
				}
			} else {
				var dist uint32
				if d.rc.bit(d.isRepG1, d.state) == 0 {
					dist = d.reps[1]
				} else {
					if d.rc.bit(d.isRepG2, d.state) == 0 {
						dist = d.reps[2]
					} else {
						dist = d.reps[3]
						d.reps[3] = d.reps[2]
					}
					d.reps[2] = d.reps[1]
				}
				d.reps[1] = d.reps[0]
				d.reps[0] = dist
			}
			length = d.lenDecode(d.repLenP, posState)
			if d.state < 7 {
				d.state = 8
			} else {
				d.state = 11
			}
		} else {
			// new match
			d.reps[3], d.reps[2], d.reps[1] = d.reps[2], d.reps[1], d.reps[0]
			length = d.lenDecode(d.lenP, posState)
			if d.state < 7 {
				d.state = 7
			} else {
				d.state = 10
			}
			lenToPos := length - lzMatchMinLen
			if lenToPos >= lzLenToPos {
				lenToPos = lzLenToPos - 1
			}
			slot := d.rc.bitTree(d.posSlot[lenToPos<<lzPosSlotBits:], lzPosSlotBits)
			var dist uint32
			if slot >= 4 {
				ndirect := (slot >> 1) - 1
				dist = (2 | (uint32(slot) & 1)) << ndirect
				if slot < 14 { // kEndPosModelIndex
					dist += uint32(d.rc.revBitTree(d.specPos[int(dist)-slot-1:], ndirect))
				} else {
					dist += d.rc.direct(ndirect-lzAlignBits) << lzAlignBits
					dist += uint32(d.rc.revBitTree(d.alignP, lzAlignBits))
					if dist == 0xFFFFFFFF {
						if wantSize <= 0 {
							return nil // EOS marker
						}
						return fmt.Errorf("lzma unexpected EOS marker")
					}
				}
			} else {
				dist = uint32(slot)
			}
			d.reps[0] = dist
		}

		if uint64(d.reps[0]) >= uint64(len(d.out)) {
			return fmt.Errorf("lzma distance %d out of range (out=%d)", d.reps[0], len(d.out))
		}
		d.copyMatch(length)
	}
}

func (d *lzDecoder) copyMatch(length int) {
	for i := 0; i < length; i++ {
		idx := len(d.out) - int(d.reps[0]) - 1
		d.out = append(d.out, d.out[idx])
	}
}
