//go:build windows

package main

import (
	"bytes"
	"embed"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

//go:embed assets/ME7Info.exe assets/me7sum.exe assets/defs/me7_std.map assets/defs/me7_alias.map assets/logo.png
var assets embed.FS

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	msimg32  = syscall.NewLazyDLL("msimg32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pBeginPaint       = user32.NewProc("BeginPaint")
	pEndPaint         = user32.NewProc("EndPaint")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pLoadCursor       = user32.NewProc("LoadCursorW")
	pSetCursor        = user32.NewProc("SetCursor")
	pDrawText         = user32.NewProc("DrawTextW")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")

	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pCreatePen          = gdi32.NewProc("CreatePen")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pRoundRect          = gdi32.NewProc("RoundRect")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pCreateFontIndirect = gdi32.NewProc("CreateFontIndirectW")
	pCreateCompatDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatBmp    = gdi32.NewProc("CreateCompatibleBitmap")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pSetStretchBltMode  = gdi32.NewProc("SetStretchBltMode")
	pBitBlt             = gdi32.NewProc("BitBlt")

	pGradientFill = msimg32.NewProc("GradientFill")
	pAlphaBlend   = msimg32.NewProc("AlphaBlend")

	pGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
	pDragAcceptFiles = shell32.NewProc("DragAcceptFiles")
	pDragQueryFile   = shell32.NewProc("DragQueryFileW")
	pDragFinish      = shell32.NewProc("DragFinish")
)

const (
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVisible     = 0x10000000
	swShow        = 5
	wmDestroy     = 0x0002
	wmPaint       = 0x000F
	wmLButtonDown = 0x0201
	wmMouseMove   = 0x0200
	wmSetCursor   = 0x0020
	wmDropFiles   = 0x0233
	wmEraseBkgnd  = 0x0014
	transparent   = 1
	halftone      = 4
	srcCopy       = 0x00CC0020
	dtCenter      = 0x1
	dtVcenter     = 0x4
	dtSingleline  = 0x20
	dtWordbreak   = 0x10
	dtNoPrefix    = 0x800
	idcArrow      = 32512
	idcHand       = 32649
	acSrcOver     = 0
	acSrcAlpha    = 1
	gradV         = 1
	gradH         = 0
)

type rect struct{ left, top, right, bottom int32 }
type point struct{ x, y int32 }
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}
type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}
type paintStruct struct {
	hdc        uintptr
	fErase     int32
	rcPaint    rect
	fRestore   int32
	fIncUpdate int32
	rgb        [32]byte
}
type triVertex struct {
	x, y                    int32
	red, green, blue, alpha uint16
}
type gradientRect struct{ upperLeft, lowerRight uint32 }
type logfont struct {
	height, width                                        int32
	escapement, orientation, weight                      int32
	italic, underline, strikeOut, charSet                byte
	outPrecision, clipPrecision, quality, pitchAndFamily byte
	faceName                                             [32]uint16
}
type bih struct {
	size                   uint32
	width, height          int32
	planes, bitCount       uint16
	compression, sizeImage uint32
	xppm, yppm             int32
	clrUsed, clrImportant  uint32
}
type blendFn struct{ op, flags, alpha, format byte }

func rgb(r, g, b uint32) uintptr { return uintptr(r | g<<8 | b<<16) }
func lo(l uintptr) int32         { return int32(int16(l & 0xffff)) }
func hi(l uintptr) int32         { return int32(int16((l >> 16) & 0xffff)) }
func c16(v uint16) uint16        { return v << 8 }

var (
	gTopR, gTopG, gTopB = c16(0xF7), c16(0xCF), c16(0xE0)
	gBotR, gBotG, gBotB = c16(0xBF), c16(0xE0), c16(0xF2)
	colNavy             = rgb(0x1B, 0x2A, 0x44)
	colNavy2            = rgb(0x2C, 0x40, 0x66)
	colCard             = rgb(0xFF, 0xFF, 0xFF)
	colCardBrd          = rgb(0xE6, 0xCF, 0xDD)
	colInk              = rgb(0x22, 0x2A, 0x38)
	colMuted            = rgb(0x7A, 0x86, 0x98)
	colSub              = rgb(0xC7, 0xD2, 0xE6)
	colOk               = rgb(0x1E, 0x8E, 0x54)
	colErr              = rgb(0xC0, 0x35, 0x35)
)

type uiState int

const (
	stateIdle uiState = iota
	stateBusy
	stateDone
	stateFail
)

type app struct {
	hwnd                                uintptr
	hInst                               uintptr
	logo                                uintptr
	logoW, logoH                        int32
	fontH1, fontBody, fontBtn, fontMono uintptr

	mu       sync.Mutex
	state    uiState
	headline string
	detail   string
	btnHot   bool
	btnRect  rect
	// set when the input failed the checksum pre-check; the button then offers "Patch anyway"
	forceFile string
}

var A app

const winW, winH = 480, 660

func main() {
	runtime.LockOSThread()
	A.hInst, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)

	className, _ := syscall.UTF16PtrFromString("SSSTuningLC")
	cursor, _, _ := pLoadCursor.Call(0, idcArrow)
	wc := wndClassEx{
		style:         0x0003,
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     A.hInst,
		hCursor:       cursor,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	loadFonts()
	loadLogo()
	A.state = stateIdle
	A.headline = "Drop a stock ME7.5 file here"
	A.detail = "or click the button to choose one.\nLaunch control and no-lift-shift are added, then the checksums are corrected automatically."

	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	x := (int32(sw) - winW) / 2
	y := (int32(sh) - winH) / 2

	title, _ := syscall.UTF16PtrFromString("SSS Tuning - ME7.5 Launch Control")
	A.hwnd, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsCaption|wsSysMenu|wsMinimizeBox|wsVisible,
		uintptr(x), uintptr(y), winW, winH, 0, 0, A.hInst, 0)

	pDragAcceptFiles.Call(A.hwnd, 1)
	pShowWindow.Call(A.hwnd, swShow)
	pUpdateWindow.Call(A.hwnd)

	if len(os.Args) > 1 {
		go worker(os.Args[1], false)
	}

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func loadFonts() {
	mk := func(size, weight int32, face string) uintptr {
		lf := logfont{height: -size, weight: weight, quality: 5}
		copy(lf.faceName[:], syscall.StringToUTF16(face))
		h, _, _ := pCreateFontIndirect.Call(uintptr(unsafe.Pointer(&lf)))
		return h
	}
	A.fontH1 = mk(23, 700, "Segoe UI")
	A.fontBody = mk(16, 400, "Segoe UI")
	A.fontBtn = mk(19, 600, "Segoe UI")
	A.fontMono = mk(13, 400, "Consolas")
}

func loadLogo() {
	data, err := assets.ReadFile("assets/logo.png")
	if err != nil {
		return
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	b := img.Bounds()
	w, h := int32(b.Dx()), int32(b.Dy())
	info := bih{size: 40, width: w, height: -h, planes: 1, bitCount: 32}
	var bits unsafe.Pointer
	memDC, _, _ := pCreateCompatDC.Call(0)
	hbm, _, _ := pCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 {
		pDeleteDC.Call(memDC)
		return
	}
	pix := unsafe.Slice((*byte)(bits), int(w)*int(h)*4)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			af := a >> 8
			pix[i+0] = byte(uint32(bl>>8) * af / 255)
			pix[i+1] = byte(uint32(g>>8) * af / 255)
			pix[i+2] = byte(uint32(r>>8) * af / 255)
			pix[i+3] = byte(af)
			i += 4
		}
	}
	pDeleteDC.Call(memDC)
	A.logo, A.logoW, A.logoH = hbm, w, h
}

func setState(s uiState, head, detail string) {
	A.mu.Lock()
	A.state, A.headline, A.detail, A.btnHot = s, head, detail, false
	A.mu.Unlock()
	pInvalidateRect.Call(A.hwnd, 0, 1)
}

type checksumErr struct{ detail string }

func (e *checksumErr) Error() string { return e.detail }

func worker(input string, force bool) {
	A.mu.Lock()
	A.forceFile = ""
	A.mu.Unlock()
	setState(stateBusy, "Working...", "Reading the file, running ME7Info, patching, and correcting checksums.")
	dst, res, err := process(input, force)
	if err != nil {
		if ce, ok := err.(*checksumErr); ok {
			A.mu.Lock()
			A.forceFile = input
			A.mu.Unlock()
			setState(stateFail, "Checksum errors in this file", ce.detail+
				"\n\nUsually this means a tuned file whose checksums weren't fully corrected, or a bad read. "+
				"If the file runs fine in the car, it's safe to continue: the output gets fully corrected checksums.")
			return
		}
		d := err.Error()
		if res != nil && len(res.Log) > 0 {
			d += "\n\n" + strings.Join(res.Log, "\n")
		}
		setState(stateFail, "Couldn't patch that file", d)
		return
	}
	d := "Saved next to the original:\n" + filepath.Base(dst)
	d += fmt.Sprintf("\n\ncode 0x%X   settings 0x%X   hook 0x%X", res.CodeCave, res.ConfigVars, res.CallSite)
	if force {
		d += "\n\nInput had checksum errors; the output's checksums are fully corrected."
	}
	if res.SettingsInCave {
		d += "\n\nSettings area was full - stored after the code instead."
	}
	if res.Version != "" && res.Version != "ME7.5" {
		d += "\n\nThis is an " + res.Version + " file; the patch targets ME7.5. Bench-test first."
	}
	d += "\n\nChecksums corrected and verified. Bench-test before flashing."
	setState(stateDone, "Done", d)
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		paint(hwnd)
		return 0
	case wmSetCursor:
		if lo(lParam) == 1 {
			A.mu.Lock()
			hot := A.btnHot
			A.mu.Unlock()
			id := uintptr(idcArrow)
			if hot {
				id = idcHand
			}
			c, _, _ := pLoadCursor.Call(0, id)
			pSetCursor.Call(c)
			return 1
		}
	case wmMouseMove:
		x, y := lo(lParam), hi(lParam)
		A.mu.Lock()
		inside := x >= A.btnRect.left && x <= A.btnRect.right && y >= A.btnRect.top && y <= A.btnRect.bottom && A.state != stateBusy
		changed := inside != A.btnHot
		A.btnHot = inside
		A.mu.Unlock()
		if changed {
			pInvalidateRect.Call(hwnd, 0, 0)
		}
	case wmLButtonDown:
		x, y := lo(lParam), hi(lParam)
		A.mu.Lock()
		inside := x >= A.btnRect.left && x <= A.btnRect.right && y >= A.btnRect.top && y <= A.btnRect.bottom && A.state != stateBusy
		A.mu.Unlock()
		if inside {
			A.mu.Lock()
			ff := A.forceFile
			A.mu.Unlock()
			if ff != "" {
				go worker(ff, true)
			} else if f := pickFile(); f != "" {
				go worker(f, false)
			}
		}
		return 0
	case wmDropFiles:
		A.mu.Lock()
		busy := A.state == stateBusy
		A.mu.Unlock()
		if !busy {
			buf := make([]uint16, 1024)
			pDragQueryFile.Call(wParam, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			go worker(syscall.UTF16ToString(buf), false)
		}
		pDragFinish.Call(wParam)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

func paint(hwnd uintptr) {
	var ps paintStruct
	winDC, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	var rc rect
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	W, H := rc.right, rc.bottom

	// double buffer
	memDC, _, _ := pCreateCompatDC.Call(winDC)
	bmp, _, _ := pCreateCompatBmp.Call(winDC, uintptr(W), uintptr(H))
	oldBmp, _, _ := pSelectObject.Call(memDC, bmp)
	hdc := memDC

	verts := []triVertex{{0, 0, gTopR, gTopG, gTopB, 0xffff}, {W, H, gBotR, gBotG, gBotB, 0xffff}}
	gr := gradientRect{0, 1}
	pGradientFill.Call(hdc, uintptr(unsafe.Pointer(&verts[0])), 2, uintptr(unsafe.Pointer(&gr)), 1, gradV)

	pad := int32(26)
	card := rect{pad, pad, W - pad, H - pad}
	fillRoundRect(hdc, card, 24, colCard, colCardBrd)

	var logoBottom int32 = card.top + 40
	if A.logo != 0 {
		lw := (card.right - card.left) * 62 / 100
		if lw > A.logoW {
			lw = A.logoW
		}
		lh := A.logoH * lw / A.logoW
		lx := (W - lw) / 2
		ly := card.top + 22
		blitLogo(hdc, lx, ly, lw, lh)
		logoBottom = ly + lh
	}
	drawText(hdc, "LAUNCH CONTROL  +  NO-LIFT-SHIFT", A.fontBody, colMuted,
		rect{card.left, logoBottom + 2, card.right, logoBottom + 24}, dtCenter|dtSingleline|dtVcenter|dtNoPrefix)

	A.mu.Lock()
	st, head, detail, hot := A.state, A.headline, A.detail, A.btnHot
	A.mu.Unlock()

	btnTop := logoBottom + 34
	btn := rect{card.left + 34, btnTop, card.right - 34, btnTop + 94}
	A.mu.Lock()
	A.btnRect = btn
	A.mu.Unlock()

	if st == stateBusy {
		fillRoundRect(hdc, btn, 16, colNavy, colNavy)
		drawText(hdc, "Working...", A.fontBtn, colCard, btn, dtCenter|dtSingleline|dtVcenter|dtNoPrefix)
	} else {
		bg := colNavy
		if hot {
			bg = colNavy2
		}
		fillRoundRect(hdc, btn, 16, bg, bg)
		A.mu.Lock()
		forcing := A.forceFile != ""
		A.mu.Unlock()
		label := "Select ME7.5 file"
		sub := "or drag a .bin onto this window"
		if forcing {
			label = "Patch anyway"
			sub = "or drag a different file onto this window"
		} else if st == stateDone || st == stateFail {
			label = "Patch another file"
		}
		drawText(hdc, label, A.fontBtn, colCard,
			rect{btn.left, btn.top + 18, btn.right, btn.top + 52}, dtCenter|dtSingleline|dtVcenter|dtNoPrefix)
		drawText(hdc, sub, A.fontBody, colSub,
			rect{btn.left, btn.top + 48, btn.right, btn.top + 72}, dtCenter|dtSingleline|dtVcenter|dtNoPrefix)
		gradBar(hdc, rect{btn.left + 34, btn.bottom - 20, btn.right - 34, btn.bottom - 16})
	}

	headCol := colInk
	if st == stateDone {
		headCol = colOk
	} else if st == stateFail {
		headCol = colErr
	}
	sy := btn.bottom + 24
	drawText(hdc, head, A.fontH1, headCol,
		rect{card.left + 24, sy, card.right - 24, sy + 34}, dtCenter|dtSingleline|dtVcenter|dtNoPrefix)

	font := A.fontBody
	if st == stateDone || st == stateFail {
		font = A.fontMono
	}
	drawText(hdc, detail, font, colMuted,
		rect{card.left + 26, sy + 42, card.right - 26, card.bottom - 40}, dtCenter|dtWordbreak|dtNoPrefix)

	drawText(hdc, "AI-written · no warranty · use at your own risk", A.fontBody, colMuted,
		rect{card.left, card.bottom - 32, card.right, card.bottom - 12}, dtCenter|dtSingleline|dtVcenter|dtNoPrefix)

	pBitBlt.Call(winDC, 0, 0, uintptr(W), uintptr(H), memDC, 0, 0, srcCopy)
	pSelectObject.Call(memDC, oldBmp)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(memDC)
	pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
}

func fillRoundRect(hdc uintptr, r rect, radius int32, fill, border uintptr) {
	brush, _, _ := pCreateSolidBrush.Call(fill)
	pen, _, _ := pCreatePen.Call(0, 1, border)
	ob, _, _ := pSelectObject.Call(hdc, brush)
	op, _, _ := pSelectObject.Call(hdc, pen)
	pRoundRect.Call(hdc, uintptr(r.left), uintptr(r.top), uintptr(r.right), uintptr(r.bottom), uintptr(radius), uintptr(radius))
	pSelectObject.Call(hdc, ob)
	pSelectObject.Call(hdc, op)
	pDeleteObject.Call(brush)
	pDeleteObject.Call(pen)
}

func gradBar(hdc uintptr, r rect) {
	verts := []triVertex{
		{r.left, r.top, c16(0xE8), c16(0x6F), c16(0xA0), 0xffff},
		{r.right, r.bottom, c16(0x5A), c16(0xA6), c16(0xD8), 0xffff},
	}
	gr := gradientRect{0, 1}
	pGradientFill.Call(hdc, uintptr(unsafe.Pointer(&verts[0])), 2, uintptr(unsafe.Pointer(&gr)), 1, gradH)
}

func blitLogo(hdc uintptr, x, y, w, h int32) {
	memDC, _, _ := pCreateCompatDC.Call(hdc)
	old, _, _ := pSelectObject.Call(memDC, A.logo)
	pSetStretchBltMode.Call(hdc, halftone)
	bf := blendFn{op: acSrcOver, alpha: 0xff, format: acSrcAlpha}
	pAlphaBlend.Call(hdc, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		memDC, 0, 0, uintptr(A.logoW), uintptr(A.logoH),
		uintptr(*(*uint32)(unsafe.Pointer(&bf))))
	pSelectObject.Call(memDC, old)
	pDeleteDC.Call(memDC)
}

func drawText(hdc uintptr, text string, font, color uintptr, r rect, flags uint32) {
	old, _, _ := pSelectObject.Call(hdc, font)
	pSetBkMode.Call(hdc, transparent)
	pSetTextColor.Call(hdc, color)
	t, _ := syscall.UTF16PtrFromString(text)
	rr := r
	pDrawText.Call(hdc, uintptr(unsafe.Pointer(t)), ^uintptr(0), uintptr(unsafe.Pointer(&rr)), uintptr(flags))
	pSelectObject.Call(hdc, old)
}

type openFileName struct {
	lStructSize                    uint32
	hwndOwner, hInstance           uintptr
	lpstrFilter, lpstrCustomFilter *uint16
	nMaxCustFilter, nFilterIndex   uint32
	lpstrFile                      *uint16
	nMaxFile                       uint32
	lpstrFileTitle                 *uint16
	nMaxFileTitle                  uint32
	lpstrInitialDir, lpstrTitle    *uint16
	flags                          uint32
	nFileOffset, nFileExtension    uint16
	lpstrDefExt                    *uint16
	lCustData, lpfnHook            uintptr
	lpTemplateName                 *uint16
	pvReserved                     uintptr
	dwReserved, flagsEx            uint32
}

func utf16z(s string) []uint16 {
	r := make([]uint16, 0, len(s)+1)
	for _, c := range s {
		r = append(r, uint16(c))
	}
	return append(r, 0)
}

func pickFile() string {
	buf := make([]uint16, 1024)
	filter := utf16z("All files (*.*)\x00*.*\x00ME7 flash files (*.bin;*.ori)\x00*.bin;*.ori\x00")
	title := utf16z("Select original ME7.5 file (512 KB or 1024 KB)")
	ofn := openFileName{
		hwndOwner:   A.hwnd,
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  &title[0],
		flags:       0x00001000 | 0x00000800 | 0x00080000,
	}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	ret, _, _ := pGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func extractTools(dir string) error {
	files := []string{"ME7Info.exe", "me7sum.exe", "defs/me7_std.map", "defs/me7_alias.map"}
	if err := os.MkdirAll(filepath.Join(dir, "defs"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "ecus"), 0755); err != nil {
		return err
	}
	for _, f := range files {
		data, err := assets.ReadFile("assets/" + f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(f)), data, 0755); err != nil {
			return err
		}
	}
	return nil
}

func run(dir, exe string, args ...string) (string, error) {
	cmd := exec.Command(filepath.Join(dir, exe), args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func uniqueName(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func process(input string, force bool) (string, *Result, error) {
	stock, err := os.ReadFile(input)
	if err != nil {
		return "", nil, fmt.Errorf("can't read file: %v", err)
	}
	if len(stock) != 0x100000 && len(stock) != 0x80000 {
		return "", nil, fmt.Errorf("file is %d bytes. Expected a full ME7 flash (512 KB or 1024 KB)", len(stock))
	}
	dir, err := os.MkdirTemp("", "me7lc")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	if err := extractTools(dir); err != nil {
		return "", nil, fmt.Errorf("can't unpack tools: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input.bin"), stock, 0644); err != nil {
		return "", nil, err
	}
	out, _ := run(dir, "me7sum.exe", "input.bin")
	if !force && !strings.Contains(out, "DONE! 0 error(s)") {
		return "", nil, &checksumErr{summarizeChecksum(out)}
	}
	out, _ = run(dir, "ME7Info.exe", "-n", "-o", "input.ecu", "input.bin")
	ecuData, err := os.ReadFile(filepath.Join(dir, "ecus", "input.ecu"))
	if err != nil {
		return "", nil, fmt.Errorf("ME7Info failed:\n%s", tail(out, 8))
	}
	patched, res, err := Patch(stock, ParseECU(ecuData))
	if err != nil {
		return "", res, err
	}
	if err := os.WriteFile(filepath.Join(dir, "patched.bin"), patched, 0644); err != nil {
		return "", res, err
	}
	out, _ = run(dir, "me7sum.exe", "patched.bin", "final.bin")
	if !strings.Contains(out, "corrected in final.bin") && !strings.Contains(out, "DONE! 0 error(s)") {
		return "", res, fmt.Errorf("ME7Sum couldn't correct the checksums:\n%s", tail(out, 6))
	}
	out, _ = run(dir, "me7sum.exe", "final.bin")
	if !strings.Contains(out, "DONE! 0 error(s)") {
		return "", res, fmt.Errorf("checksum verification failed after correction:\n%s", tail(out, 6))
	}
	final, err := os.ReadFile(filepath.Join(dir, "final.bin"))
	if err != nil || len(final) != len(stock) {
		return "", res, fmt.Errorf("output file missing or wrong size")
	}
	ext := filepath.Ext(input)
	base := strings.TrimSuffix(input, ext)
	if ext == "" || len(ext) > 5 {
		base, ext = input, ".bin"
	}
	dst := uniqueName(base + "_LC_NLS" + ext)
	if err := os.WriteFile(dst, final, 0644); err != nil {
		return "", res, fmt.Errorf("can't save output: %v", err)
	}
	return dst, res, nil
}

// summarizeChecksum pulls the failing lines out of ME7Sum's report.
func summarizeChecksum(out string) string {
	var bad []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.Contains(l, "NOT OK") || strings.Contains(l, "BAD") {
			l = strings.TrimSpace(l)
			if len(l) > 70 {
				l = l[:70] + "..."
			}
			bad = append(bad, l)
		}
	}
	if len(bad) > 4 {
		bad = append(bad[:4], fmt.Sprintf("(+%d more)", len(bad)-4))
	}
	s := "ME7Sum found checksum errors in the input:"
	if len(bad) > 0 {
		s += "\n" + strings.Join(bad, "\n")
	} else {
		s += "\n" + tail(out, 2)
	}
	return s
}
