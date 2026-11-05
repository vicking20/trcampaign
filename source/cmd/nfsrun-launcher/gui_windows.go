//go:build windows

package main

// The launcher window: Win32 through syscall (no cgo), painted with GDI into
// a back buffer. Texts are in i18n.go; the full log stays in
// nfsrun-launcher.log and behind "Show log".

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"nfsrun-server/internal/lsx"
)

var (
	user32  = sysDLL("user32.dll")
	gdi32   = sysDLL("gdi32.dll")
	shell32 = sysDLL("shell32.dll")
	dwmapi  = sysDLL("dwmapi.dll")
	uxtheme = sysDLL("uxtheme.dll")

	pRegisterClassExW   = user32.NewProc("RegisterClassExW")
	pCreateWindowExW    = user32.NewProc("CreateWindowExW")
	pDefWindowProcW     = user32.NewProc("DefWindowProcW")
	pGetMessageW        = user32.NewProc("GetMessageW")
	pTranslateMessage   = user32.NewProc("TranslateMessage")
	pDispatchMessageW   = user32.NewProc("DispatchMessageW")
	pPostQuitMessage    = user32.NewProc("PostQuitMessage")
	pPostMessageW       = user32.NewProc("PostMessageW")
	pSendMessageW       = user32.NewProc("SendMessageW")
	pShowWindow         = user32.NewProc("ShowWindow")
	pUpdateWindow       = user32.NewProc("UpdateWindow")
	pInvalidateRect     = user32.NewProc("InvalidateRect")
	pBeginPaint         = user32.NewProc("BeginPaint")
	pEndPaint           = user32.NewProc("EndPaint")
	pGetClientRect      = user32.NewProc("GetClientRect")
	pLoadCursorW        = user32.NewProc("LoadCursorW")
	pSetCursor          = user32.NewProc("SetCursor")
	pLoadImageW         = user32.NewProc("LoadImageW")
	pDrawIconEx         = user32.NewProc("DrawIconEx")
	pMessageBoxW        = user32.NewProc("MessageBoxW")
	pDestroyWindow      = user32.NewProc("DestroyWindow")
	pSetWindowTextW     = user32.NewProc("SetWindowTextW")
	pMoveWindow         = user32.NewProc("MoveWindow")
	pSetTimer           = user32.NewProc("SetTimer")
	pTrackMouseEvent    = user32.NewProc("TrackMouseEvent")
	pFillRect           = user32.NewProc("FillRect")
	pDrawTextW          = user32.NewProc("DrawTextW")
	pGetDpiForWindow    = user32.NewProc("GetDpiForWindow")
	pAdjustWindowRectEx = user32.NewProc("AdjustWindowRectEx")
	pSetWindowPos       = user32.NewProc("SetWindowPos")
	pGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	pSetFocus           = user32.NewProc("SetFocus")
	pGetDC              = user32.NewProc("GetDC")
	pReleaseDC          = user32.NewProc("ReleaseDC")
	pSetForegroundWnd   = user32.NewProc("SetForegroundWindow")
	pDestroyIcon        = user32.NewProc("DestroyIcon")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pSetBkColor             = gdi32.NewProc("SetBkColor")
	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pGetStockObject         = gdi32.NewProc("GetStockObject")
	pSetDCBrushColor        = gdi32.NewProc("SetDCBrushColor")
	pSetDCPenColor          = gdi32.NewProc("SetDCPenColor")
	pRoundRect              = gdi32.NewProc("RoundRect")
	pPolygon                = gdi32.NewProc("Polygon")
	pEllipse                = gdi32.NewProc("Ellipse")
	pGetDeviceCaps          = gdi32.NewProc("GetDeviceCaps")

	pShellExecuteW            = shell32.NewProc("ShellExecuteW")
	pGetUserDefaultLocaleName = kernel32.NewProc("GetUserDefaultLocaleName")
	pDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
	pSetWindowTheme           = uxtheme.NewProc("SetWindowTheme")
)

const (
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsClipChildren = 0x02000000
	wsChild        = 0x40000000
	wsVScroll      = 0x00200000
	esMultiline    = 0x0004
	esAutoVScroll  = 0x0040
	esReadOnly     = 0x0800
	mainStyle      = wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren

	wmDestroy        = 0x0002
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmEraseBkgnd     = 0x0014
	wmSetCursor      = 0x0020
	wmSetFont        = 0x0030
	wmKeyDown        = 0x0100
	wmTimer          = 0x0113
	wmCtlColorEdit   = 0x0133
	wmCtlColorStatic = 0x0138
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmMouseLeave     = 0x02A3
	wmDpiChanged     = 0x02E0
	wmApp            = 0x8000
	wmAppRepaint     = wmApp + 1
	wmAppQuit        = wmApp + 2

	emSetSel       = 0x00B1
	emScrollCaret  = 0x00B7
	emSetLimitText = 0x00C5

	dtCenter     = 0x0001
	dtRight      = 0x0002
	dtVCenter    = 0x0004
	dtWordBreak  = 0x0010
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800
	dtEditCtrl   = 0x2000
	dtEndEllip   = 0x8000

	dcBrush = 18
	dcPen   = 19
)

// colors as COLORREF (0x00BBGGRR)
func rgb(c uint32) uintptr { return uintptr((c>>16)&0xFF | (c & 0xFF00) | (c&0xFF)<<16) }

var (
	colBg       = rgb(0x0E0E0E)
	colHeader   = rgb(0x161616)
	colCard     = rgb(0x1A1A1A)
	colBorder   = rgb(0x2A2A2A)
	colOrange   = rgb(0xFF7A00)
	colOrangeHi = rgb(0xFF9026)
	colOrangeLo = rgb(0xE06A00)
	colWhite    = rgb(0xF2F2F2)
	colText2    = rgb(0xC8C8C8)
	colGray     = rgb(0x8A8A8A)
	colDim      = rgb(0x5E5E5E)
	colGreen    = rgb(0x3CCB5A)
	colRed      = rgb(0xE04848)
	colDark     = rgb(0x0E0E0E)
	colDisabled = rgb(0x2C2C2C)
)

type wndClassEx struct {
	cbSize     uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type point struct{ x, y int32 }

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type rect struct{ l, t, r, b int32 }

type paintStruct struct {
	hdc       uintptr
	erase     int32
	rc        rect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type trackMouse struct {
	cbSize    uint32
	flags     uint32
	hwnd      uintptr
	hoverTime uint32
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// Layout, in 96-DPI pixels.
const (
	winW = 840
	winH = 446
)

const (
	itemNone = iota
	itemPlay
	itemLog
	itemCfg
	itemDir
)

var itemRects = map[int]rect{
	itemPlay: {winW - 28 - 250, 344, winW - 28, 410},
}

const linkTop, linkBottom = 410, 432

type fontKey struct {
	size   float64
	weight int
	italic bool
	face   string
}

type gui struct {
	dir string

	// UI thread
	hwnd, edit uintptr
	dpi        int32
	fonts      map[fontKey]uintptr
	iconBig    uintptr
	cardBrush  uintptr
	showLog    bool
	linkRects  map[int]rect // device pixels, measured while painting
	hover      int
	pressed    int
	tracking   bool

	mu           sync.Mutex
	persona      string
	local        string
	server       serverState
	status       statusKind
	statusText   string
	statusDetail string
	friends      []lsx.Friend
	gotFriends   bool
	playable     bool
	running      bool
	logLines     []string
	logPartial   string
	logDirty     bool

	playCh    chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	headless  bool // the window could not be created: act like the console
	played    bool
}

var theGUI *gui

func newUI(console bool) launcherUI {
	if console {
		attachConsole()
		return &consoleUI{}
	}
	faceUI = uiFaceFor(curLang)
	self, _ := os.Executable()
	return &gui{
		dir:        filepath.Dir(self),
		fonts:      map[fontKey]uintptr{},
		playCh:     make(chan struct{}, 1),
		closed:     make(chan struct{}),
		statusText: T("st_preparing"),
		status:     statusBusy,
	}
}

// attachConsole: -console in a windowsgui exe. Use the console of the command
// prompt it was started from, or open a new one.
func attachConsole() {
	if r, _, _ := procAttachConsole.Call(^uintptr(0)); r == 0 {
		procAllocConsole.Call()
	}
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.Open("CONIN$"); err == nil {
		os.Stdin = f
	}
}

// systemLocale: the Windows user locale (pt-BR, en-US, ...), for LOCALE=auto.
func systemLocale() string {
	var buf [85]uint16
	if pGetUserDefaultLocaleName.Find() != nil {
		return ""
	}
	if n, _, _ := pGetUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:])
}

func showError(msg string) {
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16("NFS The Run - launcher"))), 0x10)
}

// ---- launcherUI -----------------------------------------------------------

func (g *gui) Console() bool { return g.headless }

func (g *gui) Run(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := g.create(); err != nil {
		// no window possible: start the game once, like the console version
		g.headless = true
		fn()
		return
	}
	go func() {
		fn()
		pPostMessageW.Call(g.hwnd, wmAppQuit, 0, 0)
	}()
	var m winMsg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	g.markClosed()
}

func (g *gui) markClosed() { g.closeOnce.Do(func() { close(g.closed) }) }

func (g *gui) repaint() {
	if g.hwnd != 0 {
		pPostMessageW.Call(g.hwnd, wmAppRepaint, 0, 0)
	}
}

func (g *gui) update(f func()) {
	g.mu.Lock()
	f()
	g.mu.Unlock()
	g.repaint()
}

func (g *gui) LogWriter() io.Writer { return guiLog{g} }

type guiLog struct{ g *gui }

func (w guiLog) Write(p []byte) (int, error) {
	g := w.g
	g.mu.Lock()
	s := g.logPartial + strings.ReplaceAll(string(p), "\r", "")
	parts := strings.Split(s, "\n")
	g.logPartial = parts[len(parts)-1]
	g.logLines = append(g.logLines, parts[:len(parts)-1]...)
	if n := len(g.logLines); n > 600 {
		g.logLines = append([]string(nil), g.logLines[n-500:]...)
	}
	g.logDirty = true
	g.mu.Unlock()
	return len(p), nil
}

func (g *gui) SetPlayer(p string)  { g.update(func() { g.persona = p }) }
func (g *gui) SetLocal(t string)   { g.update(func() { g.local = t }) }
func (g *gui) SetPlayable(on bool) { g.update(func() { g.playable = on }) }
func (g *gui) SetGame(on bool)     { g.update(func() { g.running = on }) }
func (g *gui) SetServer(s serverState) {
	g.update(func() { g.server = s })
}
func (g *gui) SetStatus(k statusKind, text, detail string) {
	g.update(func() { g.status, g.statusText, g.statusDetail = k, text, detail })
}
func (g *gui) SetFriends(list []lsx.Friend) {
	l := append([]lsx.Friend(nil), list...)
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Online != l[j].Online {
			return l[i].Online
		}
		return strings.ToLower(l[i].Persona) < strings.ToLower(l[j].Persona)
	})
	g.update(func() { g.friends, g.gotFriends = l, true })
}

func (g *gui) WaitPlay() bool {
	if g.headless {
		if g.played {
			return false
		}
		g.played = true
		return true
	}
	select {
	case <-g.playCh:
		return true
	case <-g.closed:
		return false
	}
}

func (g *gui) WaitClose() {
	if g.headless {
		select {}
	}
	<-g.closed
}

// AskInvite: a topmost Yes/No box, like Origin's invite toast. It has no
// owner window so the launcher window keeps working while it is open.
func (g *gui) AskInvite(from string) bool {
	const mbYesNo, mbIconQuestion, mbSetForeground, mbTopmost = 0x4, 0x20, 0x10000, 0x40000
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(T("inv_text", from)))),
		uintptr(unsafe.Pointer(u16(T("inv_title")))), mbYesNo|mbIconQuestion|mbSetForeground|mbTopmost)
	return r == 6 // IDYES
}

func (g *gui) Confirm(title, text string) bool {
	const mbYesNo, mbIconQuestion, mbSetForeground, mbTopmost = 0x4, 0x20, 0x10000, 0x40000
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), mbYesNo|mbIconQuestion|mbSetForeground|mbTopmost)
	return r == 6 // IDYES
}

func (g *gui) Fatal(text, detail string) {
	if g.headless {
		showError(text + "\n\n" + detail)
		return
	}
	g.update(func() {
		g.status, g.statusText, g.statusDetail = statusError, text, detail
		g.playable = false
	})
	<-g.closed
}

// ---- window ---------------------------------------------------------------

func (g *gui) create() error {
	theGUI = g
	inst, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := pLoadCursorW.Call(0, 32512)
	iconL, _, _ := pLoadImageW.Call(inst, 1, 1, 0, 0, 0x40) // LR_DEFAULTSIZE
	iconS, _, _ := pLoadImageW.Call(inst, 1, 1, 16, 16, 0)
	bg, _, _ := pCreateSolidBrush.Call(colBg)
	g.cardBrush, _, _ = pCreateSolidBrush.Call(colCard)
	wc := wndClassEx{
		style:      0x0003, // CS_HREDRAW|CS_VREDRAW
		wndProc:    syscall.NewCallback(wndProc),
		instance:   inst,
		icon:       iconL,
		cursor:     cursor,
		background: bg,
		className:  u16("NFSRunLauncher"),
		iconSm:     iconS,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	title := T("title")
	h, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wc.className)), uintptr(unsafe.Pointer(u16(title))),
		mainStyle, 0x80000000, 0x80000000, winW, winH, 0, 0, inst, 0)
	if h == 0 {
		return err
	}
	g.hwnd = h
	g.dpi = 96
	if pGetDpiForWindow.Find() == nil {
		if d, _, _ := pGetDpiForWindow.Call(h); d != 0 {
			g.dpi = int32(d)
		}
	} else {
		dc, _, _ := pGetDC.Call(h)
		if d, _, _ := pGetDeviceCaps.Call(dc, 88); d != 0 { // LOGPIXELSX
			g.dpi = int32(d)
		}
		pReleaseDC.Call(h, dc)
	}
	g.darkFrame()

	g.edit, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16("EDIT"))), 0,
		wsChild|wsVScroll|esMultiline|esReadOnly|esAutoVScroll, 0, 0, 10, 10, h, 0, inst, 0)
	if g.edit != 0 {
		pSendMessageW.Call(g.edit, emSetLimitText, 0, 0)
		if pSetWindowTheme.Find() == nil {
			pSetWindowTheme.Call(g.edit, uintptr(unsafe.Pointer(u16("DarkMode_Explorer"))), 0)
		}
	}
	g.layout(nil)
	pSetTimer.Call(h, 1, 300, 0)
	pShowWindow.Call(h, 5)
	pUpdateWindow.Call(h)
	pSetForegroundWnd.Call(h)
	return nil
}

// darkFrame: dark title bar (Windows 10 1809+), caption colors (Windows 11).
func (g *gui) darkFrame() {
	if pDwmSetWindowAttribute.Find() != nil {
		return
	}
	set := func(attr uintptr, v uint32) {
		pDwmSetWindowAttribute.Call(g.hwnd, attr, uintptr(unsafe.Pointer(&v)), 4)
	}
	set(19, 1) // DWMWA_USE_IMMERSIVE_DARK_MODE, pre-20H1 number
	set(20, 1) // DWMWA_USE_IMMERSIVE_DARK_MODE
	set(35, uint32(colHeader))
	set(36, uint32(colWhite))
	set(34, uint32(colOrange))
}

func (g *gui) px(v int32) int32 { return (v*g.dpi + 48) / 96 }

func (g *gui) sr(r rect) rect { return rect{g.px(r.l), g.px(r.t), g.px(r.r), g.px(r.b)} }

// layout sizes the window for the DPI (suggested: the rectangle Windows gave
// with WM_DPICHANGED) and places the log box.
func (g *gui) layout(suggested *rect) {
	for k, f := range g.fonts {
		pDeleteObject.Call(f)
		delete(g.fonts, k)
	}
	if suggested != nil {
		pSetWindowPos.Call(g.hwnd, 0, uintptr(suggested.l), uintptr(suggested.t),
			uintptr(suggested.r-suggested.l), uintptr(suggested.b-suggested.t), 0x14)
	} else {
		r := rect{0, 0, g.px(winW), g.px(winH)}
		pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), mainStyle, 0, 0)
		w, h := r.r-r.l, r.b-r.t
		sw, _, _ := pGetSystemMetrics.Call(0)
		sh, _, _ := pGetSystemMetrics.Call(1)
		x, y := (int32(sw)-w)/2, (int32(sh)-h)/2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		pSetWindowPos.Call(g.hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0x14)
	}
	if g.iconBig != 0 {
		pDestroyIcon.Call(g.iconBig)
	}
	inst, _, _ := procGetModuleHandleW.Call(0)
	g.iconBig, _, _ = pLoadImageW.Call(inst, 1, 1, uintptr(g.px(52)), uintptr(g.px(52)), 0)
	if g.edit != 0 {
		r := g.sr(rect{36, 124, winW - 36, 312})
		pMoveWindow.Call(g.edit, uintptr(r.l), uintptr(r.t), uintptr(r.r-r.l), uintptr(r.b-r.t), 1)
		pSendMessageW.Call(g.edit, wmSetFont, g.font(9.5, 400, false, "Consolas"), 1)
	}
	pInvalidateRect.Call(g.hwnd, 0, 0)
}

func (g *gui) font(size float64, weight int, italic bool, face string) uintptr {
	k := fontKey{size, weight, italic, face}
	if f, ok := g.fonts[k]; ok {
		return f
	}
	it := uintptr(0)
	if italic {
		it = 1
	}
	h := -int32(size*float64(g.dpi)/72 + 0.5)
	f, _, _ := pCreateFontW.Call(uintptr(h), 0, 0, 0, uintptr(weight), it, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16(face))))
	g.fonts[k] = f
	return f
}

// faceUI: Segoe UI, or the system UI font of the CJK languages (font linking
// would draw Japanese kanji with Chinese glyph shapes, and older systems may
// not link at all). Set by newUI from the window language.
var faceUI = "Segoe UI"

func uiFaceFor(l langID) string {
	switch l {
	case langJA:
		return "Meiryo UI"
	case langZH:
		return "Microsoft YaHei UI"
	case langKO:
		return "Malgun Gothic"
	}
	return "Segoe UI"
}

func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	g := theGUI
	switch msg {
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		g.paint(dc)
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmAppRepaint:
		pInvalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmTimer:
		g.refreshLog(false)
		return 0
	case wmMouseMove:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		if !g.tracking {
			tm := trackMouse{flags: 2, hwnd: hwnd} // TME_LEAVE
			tm.cbSize = uint32(unsafe.Sizeof(tm))
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tm)))
			g.tracking = true
		}
		if h := g.hit(x, y); h != g.hover {
			g.hover = h
			pInvalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case wmMouseLeave:
		g.tracking = false
		if g.hover != itemNone {
			g.hover = itemNone
			pInvalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case wmLButtonDown:
		g.pressed = g.hit(int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF)))
		pSetFocus.Call(hwnd)
		pInvalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmLButtonUp:
		p := g.pressed
		g.pressed = itemNone
		if h := g.hit(int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))); h == p {
			g.click(h)
		}
		pInvalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmSetCursor:
		if wp == hwnd && lp&0xFFFF == 1 && g.hover != itemNone && g.enabled(g.hover) {
			c, _, _ := pLoadCursorW.Call(0, 32649) // IDC_HAND
			pSetCursor.Call(c)
			return 1
		}
	case wmKeyDown:
		if wp == 0x0D { // Enter
			g.click(itemPlay)
			return 0
		}
	case wmCtlColorStatic, wmCtlColorEdit:
		pSetTextColor.Call(wp, colText2)
		pSetBkColor.Call(wp, colCard)
		return g.cardBrush
	case wmDpiChanged:
		g.dpi = int32(wp & 0xFFFF)
		g.layout((*rect)(unsafe.Add(unsafe.Pointer(nil), lp)))
		return 0
	case wmClose:
		g.mu.Lock()
		running := g.running
		g.mu.Unlock()
		if running {
			const mbYesNo, mbIconWarning, mbDefButton2, idYes = 0x4, 0x30, 0x100, 6
			r, _, _ := pMessageBoxW.Call(hwnd,
				uintptr(unsafe.Pointer(u16(T("close_q")))),
				uintptr(unsafe.Pointer(u16("NFS The Run"))), mbYesNo|mbIconWarning|mbDefButton2)
			if r != idYes {
				return 0
			}
			killGames()
		}
		pDestroyWindow.Call(hwnd)
		return 0
	case wmAppQuit:
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		g.markClosed()
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func (g *gui) hit(x, y int32) int {
	in := func(s rect) bool { return x >= s.l && x < s.r && y >= s.t && y < s.b }
	if in(g.sr(itemRects[itemPlay])) {
		return itemPlay
	}
	for id, s := range g.linkRects {
		if in(s) {
			return id
		}
	}
	return itemNone
}

func (g *gui) enabled(id int) bool {
	if id != itemPlay {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.playable && !g.running
}

func (g *gui) click(id int) {
	switch id {
	case itemPlay:
		if !g.enabled(itemPlay) {
			return
		}
		select {
		case g.playCh <- struct{}{}:
			g.update(func() { g.statusText, g.statusDetail, g.status = T("st_starting"), "", statusBusy })
		default:
		}
	case itemLog:
		g.showLog = !g.showLog
		show := uintptr(0)
		if g.showLog {
			show = 5
			g.refreshLog(true)
		}
		pShowWindow.Call(g.edit, show)
		pInvalidateRect.Call(g.hwnd, 0, 0)
	case itemCfg:
		ini := filepath.Join(g.dir, iniName)
		pShellExecuteW.Call(g.hwnd, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16("notepad.exe"))),
			uintptr(unsafe.Pointer(u16(`"`+ini+`"`))), uintptr(unsafe.Pointer(u16(g.dir))), 5)
		g.update(func() {
			if !g.running && g.status != statusError {
				g.statusDetail = T("st_settings_hint")
			}
		})
	case itemDir:
		pShellExecuteW.Call(g.hwnd, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(g.dir))), 0, 0, 5)
	}
}

func (g *gui) refreshLog(force bool) {
	if !g.showLog || g.edit == 0 {
		return
	}
	g.mu.Lock()
	if !g.logDirty && !force {
		g.mu.Unlock()
		return
	}
	text := strings.Join(g.logLines, "\r\n")
	g.logDirty = false
	g.mu.Unlock()
	pSetWindowTextW.Call(g.edit, uintptr(unsafe.Pointer(u16(text))))
	n := uintptr(utf16Len(text))
	pSendMessageW.Call(g.edit, emSetSel, n, n)
	pSendMessageW.Call(g.edit, emScrollCaret, 0, 0)
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// ---- painting ---------------------------------------------------------------

type painter struct {
	g  *gui
	dc uintptr
}

func (p painter) fill(r rect, c uintptr) {
	s := p.g.sr(r)
	pSetDCBrushColor.Call(p.dc, c)
	b, _, _ := pGetStockObject.Call(dcBrush)
	pFillRect.Call(p.dc, uintptr(unsafe.Pointer(&s)), b)
}

func (p painter) box(r rect, radius int32, fillC, border uintptr) {
	s := p.g.sr(r)
	b, _, _ := pGetStockObject.Call(dcBrush)
	pen, _, _ := pGetStockObject.Call(dcPen)
	pSelectObject.Call(p.dc, b)
	pSelectObject.Call(p.dc, pen)
	pSetDCBrushColor.Call(p.dc, fillC)
	pSetDCPenColor.Call(p.dc, border)
	d := p.g.px(radius * 2)
	pRoundRect.Call(p.dc, uintptr(s.l), uintptr(s.t), uintptr(s.r), uintptr(s.b), uintptr(d), uintptr(d))
}

// slant: a parallelogram like the bars of the icon.
func (p painter) slant(x0, x1, y0, y1 int32, c uintptr) {
	k := (y1 - y0) * 45 / 100
	pts := [4]point{
		{p.g.px(x0 + k), p.g.px(y0)}, {p.g.px(x1 + k), p.g.px(y0)},
		{p.g.px(x1), p.g.px(y1)}, {p.g.px(x0), p.g.px(y1)},
	}
	b, _, _ := pGetStockObject.Call(dcBrush)
	pen, _, _ := pGetStockObject.Call(dcPen)
	pSelectObject.Call(p.dc, b)
	pSelectObject.Call(p.dc, pen)
	pSetDCBrushColor.Call(p.dc, c)
	pSetDCPenColor.Call(p.dc, c)
	pPolygon.Call(p.dc, uintptr(unsafe.Pointer(&pts[0])), 4)
}

// dot: a status circle of diameter d centered in the box (x, y0..y1).
func (p painter) dot(x, y0, y1, d int32, c uintptr) {
	b, _, _ := pGetStockObject.Call(dcBrush)
	pen, _, _ := pGetStockObject.Call(dcPen)
	pSelectObject.Call(p.dc, b)
	pSelectObject.Call(p.dc, pen)
	pSetDCBrushColor.Call(p.dc, c)
	pSetDCPenColor.Call(p.dc, c)
	l, t := p.g.px(x), p.g.px(y0)+(p.g.px(y1-y0)-p.g.px(d))/2
	pEllipse.Call(p.dc, uintptr(l), uintptr(t), uintptr(l+p.g.px(d)), uintptr(t+p.g.px(d)))
}

func (p painter) text(s string, r rect, font uintptr, c uintptr, flags uintptr) {
	if s == "" {
		return
	}
	sr := p.g.sr(r)
	pSelectObject.Call(p.dc, font)
	pSetTextColor.Call(p.dc, c)
	pDrawTextW.Call(p.dc, uintptr(unsafe.Pointer(u16(s))), ^uintptr(0), uintptr(unsafe.Pointer(&sr)), flags|dtNoPrefix)
}

const line = dtSingleLine | dtVCenter | dtEndEllip

func (g *gui) paint(hdc uintptr) {
	var cr rect
	pGetClientRect.Call(g.hwnd, uintptr(unsafe.Pointer(&cr)))
	mem, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(cr.r), uintptr(cr.b))
	old, _, _ := pSelectObject.Call(mem, bmp)
	pSetBkMode.Call(mem, 1) // TRANSPARENT
	p := painter{g, mem}

	g.mu.Lock()
	st := struct {
		persona, local, text, detail  string
		server                        serverState
		status                        statusKind
		friends                       []lsx.Friend
		gotFriends, playable, running bool
	}{g.persona, g.local, g.statusText, g.statusDetail, g.server, g.status, g.friends, g.gotFriends, g.playable, g.running}
	g.mu.Unlock()

	// background + header
	b, _, _ := pGetStockObject.Call(dcBrush)
	pSetDCBrushColor.Call(mem, colBg)
	pFillRect.Call(mem, uintptr(unsafe.Pointer(&cr)), b)
	p.fill(rect{0, 0, 2000, 92}, colHeader)
	// speed bars, right side of the header
	p.slant(500, 590, 22, 34, colOrange)
	p.slant(480, 562, 40, 52, rgb(0xFF952B))
	p.slant(460, 534, 58, 70, rgb(0xFFB05C))
	if g.iconBig != 0 {
		pDrawIconEx.Call(mem, uintptr(g.px(28)), uintptr(g.px(20)), g.iconBig, uintptr(g.px(52)), uintptr(g.px(52)), 0, 0, 3)
	}
	p.text("NEED FOR SPEED", rect{96, 16, 520, 36}, g.font(10.5, 700, false, faceUI), colOrange, line)
	p.text("THE RUN", rect{94, 30, 520, 80}, g.font(28, 900, true, faceUI), colWhite, line)
	p.text(T("private_server"), rect{620, 26, winW - 28, 46}, g.font(9.5, 700, false, faceUI), colGray, line|dtRight)
	p.fill(rect{0, 92, 2000, 95}, colOrange)

	label := g.font(8.5, 700, false, faceUI)
	if g.showLog {
		p.box(rect{28, 116, winW - 28, 320}, 8, colCard, colBorder)
	} else {
		// player card
		p.box(rect{28, 116, 412, 320}, 8, colCard, colBorder)
		p.text(T("player"), rect{48, 130, 396, 148}, label, colGray, line)
		name := st.persona
		if name == "" {
			name = "..."
		}
		p.text(name, rect{48, 148, 396, 186}, g.font(20, 700, false, faceUI), colWhite, line)
		p.text(T("server"), rect{48, 198, 396, 216}, label, colGray, line)
		// only the state, never the server address
		dot, state, hint := colDim, T("srv_none"), T("srv_none_hint")
		switch st.server {
		case serverConnecting:
			dot, state, hint = colOrange, T("srv_connecting"), ""
		case serverOnline:
			dot, state, hint = colGreen, T("srv_online"), ""
		case serverFailed:
			dot, state, hint = colRed, T("srv_offline"), ""
		}
		p.dot(50, 216, 240, 9, dot)
		p.text(state, rect{66, 216, 396, 240}, g.font(11.5, 600, false, faceUI), colWhite, line)
		p.text(hint, rect{66, 240, 396, 258}, g.font(9.5, 400, false, faceUI), colGray, line)
		p.text(st.local, rect{48, 284, 396, 304}, g.font(8.5, 400, false, faceUI), colDim, line)

		// friends card
		p.box(rect{428, 116, winW - 28, 320}, 8, colCard, colBorder)
		p.text(T("friends"), rect{448, 130, 700, 148}, label, colGray, line)
		online := 0
		for _, f := range st.friends {
			if f.Online {
				online++
			}
		}
		if st.gotFriends {
			c := colGray
			if online > 0 {
				c = colGreen
			}
			p.text(T("online_count", online), rect{600, 130, winW - 48, 148}, g.font(8.5, 700, false, faceUI), c, line|dtRight)
		}
		p.fill(rect{448, 154, winW - 48, 155}, colBorder)
		switch {
		case st.server == serverNone:
			p.text(T("friends_none_cfg"), rect{448, 160, winW - 48, 300}, g.font(10, 400, false, faceUI), colGray, dtCenter|dtVCenter|dtSingleLine)
		case st.server == serverFailed: // no stale "online" list while the server is down
			p.text(T("friends_down"), rect{448, 160, winW - 48, 300}, g.font(10, 400, false, faceUI), colGray, dtCenter|dtVCenter|dtSingleLine)
		case !st.gotFriends:
			p.text(T("loading"), rect{448, 160, winW - 48, 300}, g.font(10, 400, false, faceUI), colGray, dtCenter|dtVCenter|dtSingleLine)
		case len(st.friends) == 0:
			p.text(T("no_players"), rect{448, 160, winW - 48, 300}, g.font(10, 400, false, faceUI), colGray, dtCenter|dtVCenter|dtSingleLine)
		default:
			const rows = 6
			y := int32(162)
			for i, f := range st.friends {
				if i == rows-1 && len(st.friends) > rows {
					p.text(T("more_players", len(st.friends)-i), rect{466, y, winW - 48, y + 24}, g.font(9.5, 400, false, faceUI), colGray, line)
					break
				}
				dot, what, nc := colDim, T("pres_offline"), colGray
				if f.Online {
					nc = colWhite
					if strings.EqualFold(f.Presence, "JOINABLE") {
						dot, what = colOrange, T("pres_joinable")
					} else {
						dot, what = colGreen, T("pres_ingame")
					}
				}
				p.dot(450, y, y+24, 8, dot)
				p.text(f.Persona, rect{466, y, 650, y + 24}, g.font(11, 600, false, faceUI), nc, line)
				p.text(what, rect{650, y, winW - 48, y + 24}, g.font(9.5, 400, false, faceUI), colGray, line|dtRight)
				y += 24
			}
		}
	}

	// status
	dot := colOrange
	switch st.status {
	case statusOK:
		dot = colGreen
	case statusError:
		dot = colRed
	}
	p.dot(30, 342, 368, 10, dot)
	p.text(st.text, rect{48, 342, 548, 368}, g.font(12.5, 600, false, faceUI), colWhite, line)
	p.text(st.detail, rect{48, 368, 548, 404}, g.font(9.5, 400, false, faceUI), colGray, dtWordBreak|dtEditCtrl|dtEndEllip)

	// links
	link := g.font(9.5, 600, false, faceUI)
	logText := T("show_log")
	if g.showLog {
		logText = T("hide_log")
	}
	if g.linkRects == nil {
		g.linkRects = map[int]rect{}
	}
	x := g.px(28)
	pSelectObject.Call(mem, link)
	for _, l := range []struct {
		id int
		s  string
	}{{itemLog, logText}, {itemCfg, T("settings")}, {itemDir, T("open_folder")}} {
		c := colGray
		if g.hover == l.id {
			c = colOrangeHi
		}
		m := rect{0, 0, 1000, 100}
		pDrawTextW.Call(mem, uintptr(unsafe.Pointer(u16(l.s))), ^uintptr(0), uintptr(unsafe.Pointer(&m)), dtSingleLine|dtNoPrefix|0x400) // DT_CALCRECT
		r := rect{x, g.px(linkTop), x + m.r + g.px(4), g.px(linkBottom)}
		g.linkRects[l.id] = r
		pSetTextColor.Call(mem, c)
		pDrawTextW.Call(mem, uintptr(unsafe.Pointer(u16(l.s))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), line|dtNoPrefix)
		x = r.r + g.px(20)
	}

	// JOGAR
	br := itemRects[itemPlay]
	enabled := st.playable && !st.running
	switch {
	case st.running:
		p.box(br, 6, colCard, colOrange)
		p.text(T("in_game"), br, g.font(18, 900, true, faceUI), colOrange, dtCenter|dtVCenter|dtSingleLine)
	case !enabled:
		p.box(br, 6, colDisabled, colDisabled)
		p.text(T("play"), br, g.font(20, 900, true, faceUI), colDim, dtCenter|dtVCenter|dtSingleLine)
	default:
		c := colOrange
		if g.pressed == itemPlay {
			c = colOrangeLo
		} else if g.hover == itemPlay {
			c = colOrangeHi
		}
		p.box(br, 6, c, c)
		p.text(T("play"), br, g.font(20, 900, true, faceUI), colDark, dtCenter|dtVCenter|dtSingleLine)
	}

	pBitBlt.Call(hdc, 0, 0, uintptr(cr.r), uintptr(cr.b), mem, 0, 0, 0x00CC0020)
	pSelectObject.Call(mem, old)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(mem)
}
