package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Synthetic ECU image + .ecu file that contain every pattern Patch() looks for.
// (Real ECU files are Bosch firmware and can't be committed.)

const (
	tsrldynA = 0x380BC2
	vfilA    = 0x381D88
	nmotA    = 0x00F89E
	wpedA    = 0x3809B8
	ubA      = 0x3809A4
	clutchA  = 0x00FD48
	hookAt   = 0x70000
	ftomnAt  = 0x1A000
)

func ecuLine(name string, addr, mask int) string {
	return fmt.Sprintf("%-16s, {Alias, with comma}, 0x%06X,  2,  0x%04X, {}, 0, 0, 1, 0, {Kommentar, mit Komma}\r\n", name, addr, mask)
}

func synthECU() []byte {
	var sb strings.Builder
	sb.WriteString("; generated for tests\r\n[Version]\r\n")
	sb.WriteString(ecuLine("tsrldyn", tsrldynA, 0))
	sb.WriteString(ecuLine("vfil_w", vfilA, 0))
	sb.WriteString(ecuLine("nmot_w", nmotA, 0))
	sb.WriteString(ecuLine("wped", wpedA, 0))
	sb.WriteString(ecuLine("ub", ubA, 0))
	sb.WriteString(ecuLine("B_kuppl", clutchA, 0x1000))
	sb.WriteString(ecuLine("B_br", clutchA, 0x0100))
	return []byte(sb.String())
}

func synthBin(size int, settingsHole bool) []byte {
	b := bytes.Repeat([]byte{0x11}, size)
	// code cave near the end of code area
	copy(b[size-0x2000:], bytes.Repeat([]byte{0xFF}, 0x400))
	// settings hole in 0x17000-0x18000
	if settingsHole {
		copy(b[0x17100:], bytes.Repeat([]byte{0xFF}, 0x80))
	}
	// FTOMN pattern
	b[ftomnAt] = 0x05
	b[ftomnAt+11] = 0x05
	b[ftomnAt+22] = 0x05 // the byte that gets zeroed
	b[ftomnAt+24] = 0x08
	b[ftomnAt+25] = 0x05
	// hook: MOVB RL4, ub ; EXTP #206h ...
	ubMem := ubA - 0x380000 + 0x8000
	copy(b[hookAt:], []byte{0xF3, 0xF8, byte(ubMem), byte(ubMem >> 8), 0xD7, 0x40, 0x06, 0x02, 0x03, 0xF8})
	return b
}

func TestPatch1MB(t *testing.T) {
	stock := synthBin(0x100000, true)
	out, r, err := Patch(stock, ParseECU(synthECU()))
	if err != nil {
		t.Fatalf("patch failed: %v\n%s", err, strings.Join(r.Log, "\n"))
	}
	if r.CallSite != hookAt {
		t.Errorf("hook at %#x, want %#x", r.CallSite, hookAt)
	}
	// CALLS seg, addr
	target := r.CodeCave + 0x800000
	want := []byte{0xDA, byte(target >> 16), byte(target), byte(target >> 8)}
	if !bytes.Equal(out[hookAt:hookAt+4], want) {
		t.Errorf("hook bytes % x, want % x", out[hookAt:hookAt+4], want)
	}
	if out[ftomnAt+22] != 0 {
		t.Errorf("FTOMN not zeroed")
	}
	if r.SettingsInCave || r.ConfigVars < 0x17000 || r.ConfigVars >= 0x18000 {
		t.Errorf("settings at %#x (inCave=%v), want inside 0x17000-0x18000", r.ConfigVars, r.SettingsInCave)
	}
	if !bytes.Equal(out[r.ConfigVars:r.ConfigVars+9], []byte{0xA6, 0x01, 0x50, 0x46, 0x0A, 0x00, 0xF0, 0x55, 0xE6}) {
		t.Errorf("default settings wrong: % x", out[r.ConfigVars:r.ConfigVars+9])
	}
	// code must end by re-executing the replaced instruction and RETS
	code := out[r.CodeCave : r.CodeCave+142]
	if !bytes.HasSuffix(code, []byte{0xF3, 0xF8, stock[hookAt+2], stock[hookAt+3], 0xDB, 0x00}) {
		t.Errorf("code tail wrong: % x", code[len(code)-6:])
	}
	// settings are read via EXTS #81h
	if bytes.Count(code, []byte{0xD7, 0x00, 0x81, 0x00}) != 5 {
		t.Errorf("expected 5 EXTS #81h settings reads")
	}
	// running again must refuse
	if _, _, err := Patch(out, ParseECU(synthECU())); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("second run should refuse as already patched, got %v", err)
	}
}

func TestPatch512KB(t *testing.T) {
	if _, r, err := Patch(synthBin(0x80000, true), ParseECU(synthECU())); err != nil {
		t.Fatalf("512KB patch failed: %v\n%s", err, strings.Join(r.Log, "\n"))
	}
}

func TestSettingsFallbackToCave(t *testing.T) {
	out, r, err := Patch(synthBin(0x100000, false), ParseECU(synthECU()))
	if err != nil {
		t.Fatal(err)
	}
	if !r.SettingsInCave || r.ConfigVars != r.CodeCave+0xA0 {
		t.Fatalf("expected settings after code, got %#x (cave %#x)", r.ConfigVars, r.CodeCave)
	}
	seg := byte((r.ConfigVars + 0x800000) >> 16)
	code := out[r.CodeCave : r.CodeCave+142]
	if bytes.Count(code, []byte{0xD7, 0x00, seg, 0x00}) != 5 {
		t.Errorf("settings reads should use EXTS #%02Xh", seg)
	}
}

func TestRefusesWithoutHook(t *testing.T) {
	b := synthBin(0x100000, true)
	b[hookAt] = 0x11 // break the MOVB RL4, ub
	_, _, err := Patch(b, ParseECU(synthECU()))
	if err == nil || !strings.Contains(err.Error(), "hook") {
		t.Fatalf("expected hook error, got %v", err)
	}
}

func TestRefusesBadSize(t *testing.T) {
	if _, _, err := Patch(make([]byte, 1234), ParseECU(synthECU())); err == nil {
		t.Fatal("expected size error")
	}
}

func TestRefusesVariableOutOfRange(t *testing.T) {
	e := strings.Replace(string(synthECU()), "0x381D88", "0x384D88", 1)
	if _, _, err := Patch(synthBin(0x100000, true), ParseECU([]byte(e))); err == nil {
		t.Fatal("expected address-range error for vfil_w")
	}
}

func TestParseECU(t *testing.T) {
	m := ParseECU(synthECU())
	v, ok := m["b_kuppl"]
	if !ok {
		t.Fatal("b_kuppl missing")
	}
	if v[1] != "0x00FD48" || v[3] != "0x1000" {
		t.Errorf("columns parsed wrong: %q", v)
	}
	if bitmask2int(v[3]) != 12 {
		t.Errorf("bitmask2int(0x1000) = %d", bitmask2int(v[3]))
	}
}
