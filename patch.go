package main

// Port of launch.php (ME7.x Launch Control + No-Lift-Shift code-cave patch).
// Byte output matches the PHP script for valid inputs. Where the PHP would
// silently write broken code, this version stops with an error instead.

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Default RAM location for the NLS counter (same as the PHP default).
const nlsCounterAddr = 0x384FF0

type Result struct {
	Log        []string
	FTOMN      int
	FTOMNMulti bool
	CodeCave   int
	ConfigVars int
	CallSite   int
	// Settings didn't fit in 0x17000-0x18000 and were placed after the code
	SettingsInCave bool
	Version        string
}

func (r *Result) logf(format string, a ...interface{}) {
	r.Log = append(r.Log, fmt.Sprintf(format, a...))
}

// ParseECU parses a ME7Info .ecu file the same way launch.php's prepareArray does:
// comment lines skipped, {...} contents ignored, whitespace removed, split on ','.
// Key = lowercase variable name, value = the remaining columns.
func ParseECU(data []byte) map[string][]string {
	ecu := map[string][]string{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.ReplaceAll(raw, "\r", "")
		if line == "" || strings.ContainsRune(";#/[", rune(line[0])) {
			continue
		}
		// Blank out the contents of {...} so commas inside comments don't split.
		var sb strings.Builder
		depth := 0
		for _, c := range line {
			switch {
			case c == '{':
				depth++
				sb.WriteRune(c)
			case c == '}':
				if depth > 0 {
					depth--
				}
				sb.WriteRune(c)
			case depth > 0:
				// drop comment content
			case c == ' ' || c == '\t':
				// drop whitespace
			default:
				sb.WriteRune(c)
			}
		}
		cols := strings.Split(sb.String(), ",")
		if len(cols) < 10 {
			continue
		}
		ecu[strings.ToLower(cols[0])] = cols[1:]
	}
	return ecu
}

func parseHex(s string) (int, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.EqualFold(s[:2], "0x") {
		s = s[2:]
	}
	v, err := strconv.ParseInt(s, 16, 64)
	return int(v), err
}

func varAddr(ecu map[string][]string, name string) (int, bool) {
	v, ok := ecu[name]
	if !ok || len(v) < 2 {
		return 0, false
	}
	a, err := parseHex(v[1])
	if err != nil || a == 0 {
		return 0, false
	}
	return a, true
}

// bitmask2int: 0x1000 -> 12
func bitmask2int(s string) int {
	v, _ := parseHex(s)
	i := 0
	for v > 1 {
		v /= 2
		i++
	}
	return i
}

// isVariableUsed: is addr referenced by any variable in the .ecu?
func isVariableUsed(ecu map[string][]string, addr int) string {
	want := fmt.Sprintf("0x%06X", addr)
	for k, vals := range ecu {
		for _, v := range vals {
			if strings.EqualFold(v, want) {
				return k
			}
		}
	}
	return ""
}

// at mimics PHP string indexing: out of range -> no byte (never matches).
func at(bin []byte, i int) int {
	if i < 0 || i >= len(bin) {
		return -1
	}
	return int(bin[i])
}

func findFTOMN(bin []byte) []int {
	var found []int
	for i := 0; i < len(bin); i++ {
		if at(bin, i) != 0x05 || at(bin, i+1) == 0x05 || at(bin, i+11) != 0x05 {
			continue
		}
		if at(bin, i+24) != 0x08 || at(bin, i+25) != 0x05 {
			continue
		}
		found = append(found, i+22)
	}
	if len(found) == 0 {
		for i := 0; i < len(bin); i++ {
			if at(bin, i) != 0x05 || at(bin, i+1) == 0x05 || at(bin, i+11) != 0x05 || at(bin, i+12) != 0x07 {
				continue
			}
			found = append(found, i+11)
		}
	}
	return found
}

// findHole: search backwards for a run of 0xFF of at least size bytes (16-byte aligned start).
func findHole(bin []byte, size, from, to int) int {
	binsize := len(bin)
	if to >= binsize || to == 0 {
		to = binsize - 64
	}
	if from < 0 {
		from = 0
	}
	ff := 0
	for i := to; i > from; i-- {
		if bin[i] == 0xFF {
			ff++
		} else if ff >= size {
			off := 16 - ((i + 16) % 16) + i + 16
			space := ff - (off - i)
			if space > size {
				return off
			}
			ff = 0
		} else {
			ff = 0
		}
	}
	return 0
}

func Patch(stock []byte, ecu map[string][]string) ([]byte, *Result, error) {
	r := &Result{FTOMN: -1}
	if m := regexp.MustCompile(`ME7\.[0-9.]+`).Find(stock); m != nil {
		r.Version = strings.TrimRight(string(m), ".")
	}
	bin := make([]byte, len(stock))
	copy(bin, stock)

	if len(bin) != 0x100000 && len(bin) != 0x80000 {
		return nil, r, fmt.Errorf("file is %d bytes; expected a full ME7 flash (512 KB or 1024 KB)", len(bin))
	}

	// Already patched? (checked first, because our own injected code would
	// otherwise trip the NLS-counter RAM check below with a confusing error)
	if last := bytes.LastIndex(bin, []byte{0xD7, 0x40, 0x06, 0x02, 0x03, 0xF8}); last >= 4 && bin[last-4] == 0xDA {
		return nil, r, fmt.Errorf("this file already has a call at the hook point, so it most likely already has launch control (or a previous attempt). Use an original file")
	}

	need := func(name string) (int, error) {
		a, ok := varAddr(ecu, name)
		if !ok {
			return 0, fmt.Errorf("ME7Info did not find '%s' in this file", name)
		}
		r.logf("%-8s = 0x%06X", name, a)
		return a, nil
	}

	tsrldyn, err := need("tsrldyn")
	if err != nil {
		return nil, r, err
	}
	vfil, err := need("vfil_w")
	if err != nil {
		return nil, r, err
	}
	nmot, err := need("nmot_w")
	if err != nil {
		return nil, r, err
	}
	wped, ok := varAddr(ecu, "wped")
	if ok {
		r.logf("wped     = 0x%06X", wped)
	} else if dw, ok2 := varAddr(ecu, "dwped"); ok2 {
		wped = dw + 2
		r.logf("wped not found, using dwped+2 = 0x%06X", wped)
	} else {
		return nil, r, fmt.Errorf("ME7Info did not find 'wped' (or 'dwped') in this file")
	}
	ub, err := need("ub")
	if err != nil {
		return nil, r, err
	}
	kuppl, err := need("b_kuppl")
	if err != nil {
		return nil, r, err
	}
	kupplBit := bitmask2int(ecu["b_kuppl"][3])
	brems, err := need("b_br")
	if err != nil {
		return nil, r, err
	}
	bremsBit := bitmask2int(ecu["b_br"][3])
	r.logf("B_kuppl bit %d, B_br bit %d", kupplBit, bremsBit)

	// Sanity checks for the addressing modes the injected code uses.
	// (launch.php assumes these silently and writes garbage if they don't hold.)
	for _, v := range []struct {
		n string
		a int
	}{{"tsrldyn", tsrldyn}, {"vfil_w", vfil}, {"wped", wped}, {"ub", ub}} {
		if v.a < 0x380000 || v.a > 0x383FFF {
			return nil, r, fmt.Errorf("%s at 0x%06X is outside 0x380000-0x383FFF; the launch code can't address it on this file", v.n, v.a)
		}
	}
	if nmot < 0xC000 || nmot > 0xFFFF {
		return nil, r, fmt.Errorf("nmot_w at 0x%06X is outside 0xC000-0xFFFF; the launch code can't address it on this file", nmot)
	}
	for _, v := range []struct {
		n string
		a int
	}{{"B_kuppl", kuppl}, {"B_br", brems}} {
		if v.a < 0xFD00 || v.a > 0xFDFE || v.a%2 != 0 {
			return nil, r, fmt.Errorf("%s at 0x%06X is not in the bit-addressable area 0xFD00-0xFDFE", v.n, v.a)
		}
	}
	bitoff := func(a int) byte { return byte((a - 0xFD00) / 2) }

	// FTOMN
	ft := findFTOMN(bin)
	switch {
	case len(ft) > 1:
		r.FTOMNMulti = true
		r.FTOMN = ft[0]
		r.logf("WARNING: %d FTOMN candidates found, zeroing the first one at 0x%X (was 0x%02X)", len(ft), ft[0], bin[ft[0]])
		bin[ft[0]] = 0
	case len(ft) == 1:
		r.FTOMN = ft[0]
		r.logf("FTOMN at 0x%X (was 0x%02X) -> 0x00", ft[0], bin[ft[0]])
		bin[ft[0]] = 0
	default:
		r.logf("FTOMN not found (left untouched)")
	}

	// Code cave for the main routine
	cave := findHole(bin, 256, 0, 0)
	if cave == 0 {
		return nil, r, fmt.Errorf("no free space (256 bytes of 0xFF) found for the launch control code")
	}
	r.CodeCave = cave
	r.logf("code cave at 0x%X", cave)

	// Config variables: same area as launch.php (0x17000-0x18000) so results match
	// existing LC files/XDFs. If that area has no room, store them right after the
	// code in the cave instead (the code addresses them with EXTS, any segment works).
	vars := findHole(bin, 32, 0x17000, 0x18000)
	if vars == 0 {
		vars = cave + 0xA0 // code is 142 bytes; keep settings 16-byte aligned after it
		r.SettingsInCave = true
		r.logf("no room in 0x17000-0x18000, settings stored after the code at 0x%X", vars)
	} else {
		r.logf("settings at 0x%X", vars)
	}
	r.ConfigVars = vars
	varSeg := byte((vars + 0x800000) >> 16)

	// NLS counter RAM must not be referenced directly by the stock code
	for _, pre := range [][]byte{{0xD7, 0x40, 0xE1, 0x00}, {0xD7, 0x00, 0x38, 0x00}} {
		for s := 0; ; {
			i := bytes.Index(bin[s:], pre)
			if i < 0 || s+i+8 > len(bin) {
				break
			}
			o := s + i
			mem := int(bin[o+6]) | int(bin[o+7])<<8
			var addr int
			if pre[2] == 0xE1 {
				addr = 0x384000 + mem&0x3FFF
			} else {
				addr = 0x380000 + mem
			}
			if addr == nlsCounterAddr || addr == nlsCounterAddr+1 {
				return nil, r, fmt.Errorf("RAM 0x%06X (NLS counter) is used by the stock code at 0x%X", addr, o)
			}
			s = o + 1
		}
	}

	if u := isVariableUsed(ecu, nlsCounterAddr); u != "" {
		return nil, r, fmt.Errorf("RAM address 0x%06X (NLS counter) is already used by '%s'", nlsCounterAddr, u)
	}

	// Hook location. launch.php used "last occurrence of D7 40 06 02 03 F8, minus 4".
	// The instruction it replaces is always MOVB RL4, ub (F3 F8 <ub>) in the ignition
	// routine, so we search for exactly that and require a single match. On early
	// software where that site doesn't exist, the old heuristic hooks the wrong routine.
	sig := []byte{0xD7, 0x40, 0x06, 0x02, 0x03, 0xF8}
	ubMem := ub - 0x380000 + 0x8000
	site := append([]byte{0xF3, 0xF8, byte(ubMem), byte(ubMem >> 8)}, sig...)
	var sites []int
	for s := 0; ; {
		i := bytes.Index(bin[s:], site)
		if i < 0 {
			break
		}
		sites = append(sites, s+i)
		s += i + 1
	}
	if len(sites) == 0 {
		// Distinguish "already patched" from "unsupported layout"
		last := bytes.LastIndex(bin, sig)
		if last >= 4 && bin[last-4] == 0xDA {
			return nil, r, fmt.Errorf("this file already has a call at the hook point, so it most likely already has launch control (or a previous attempt). Use an original file")
		}
		return nil, r, fmt.Errorf("the ignition-routine hook (MOVB RL4, ub before EXTP 206h) was not found. This software version is laid out differently (seen on the earliest 1999-2000 ME7.5 files), and the launch code can't be safely inserted")
	}
	if len(sites) > 1 {
		return nil, r, fmt.Errorf("found %d possible hook sites, expected exactly one; not patching to be safe", len(sites))
	}
	jump := sites[0]
	r.CallSite = jump
	r.logf("hook at 0x%X (MOVB RL4, ub)", jump)

	target := cave + 0x800000
	jb1, jb2 := bin[jump+2], bin[jump+3]
	bin[jump] = 0xDA
	bin[jump+1] = byte(target >> 16)
	bin[jump+2] = byte(target)
	bin[jump+3] = byte(target >> 8)

	// Default settings (same as the PHP script)
	copy(bin[vars:], []byte{0xA6, 0x01, 0x50, 0x46, 0x0A, 0x00, 0xF0, 0x55, 0xE6})

	// Code writer
	c := cave
	w := func(b ...byte) {
		for _, x := range b {
			bin[c] = x
			c++
		}
	}
	le16 := func(v int) { w(byte(v), byte(v>>8)) }
	dpp := func(a int) { le16(a + 0x8000) } // bitwiseandsum
	nls := nlsCounterAddr - 0x380000

	// LINE 1
	w(0x9A, bitoff(kuppl), 0x13, byte(kupplBit<<4), 0xF2, 0xF4)
	dpp(vfil)
	w(0xD7, 0x00, varSeg, 0x00, 0xF2, 0xF9)
	le16(vars)
	// LINE 2
	w(0x40, 0x49, 0x9D, 0x0B, 0xF2, 0xF4)
	le16(nmot)
	w(0xD7, 0x00, varSeg, 0x00, 0xF2, 0xF9)
	le16(vars + 2)
	// LINE 3
	w(0x40, 0x49, 0xFD, 0x03, 0xF7, 0x8E)
	dpp(tsrldyn)
	w(0x0D, 0x2F, 0x9A, bitoff(kuppl), 0x29, byte(kupplBit<<4), 0x8A, bitoff(brems))
	// LINE 4
	w(0x22, byte(bremsBit<<4), 0xF2, 0xF4)
	le16(nmot)
	w(0xD7, 0x00, varSeg, 0x00, 0xF2, 0xF9)
	le16(vars + 6)
	w(0x40, 0x49)
	// LINE 5
	w(0xFD, 0x1A, 0xC2, 0xF4)
	dpp(wped)
	w(0xD7, 0x00, varSeg, 0x00, 0xC2, 0xF9)
	le16(vars + 8)
	w(0x40, 0x49)
	// LINE 6
	w(0xFD, 0x12, 0xD7, 0x00, 0x38, 0x00, 0xF2, 0xF4)
	le16(nls)
	w(0xD7, 0x00, varSeg, 0x00, 0xF2, 0xF9)
	// LINE 7
	le16(vars + 4)
	w(0x40, 0x49, 0x9D, 0x11, 0xF7, 0x8E)
	dpp(tsrldyn)
	w(0x08, 0x41, 0xD7, 0x00, 0x38)
	// LINE 8
	w(0x00, 0xF7, 0xF8)
	le16(nls)
	w(0x0D, 0x09, 0xD7, 0x00, 0x38, 0x00, 0xF6, 0x8F)
	le16(nls)
	w(0x0D, 0x04)
	// LINE 9
	w(0xD7, 0x00, 0x38, 0x00, 0xF6, 0x8E)
	le16(nls)
	w(0xF3, 0xF8, jb1, jb2, 0xDB, 0x00)

	r.logf("wrote %d bytes of code", c-cave)
	return bin, r, nil
}
