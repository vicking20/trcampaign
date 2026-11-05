// mkwinres writes the launcher's Windows resources (icon, version info,
// manifest) as cmd/nfsrun-launcher/rsrc_windows_{386,amd64}.syso, which
// "go build" links in automatically. Run from the source folder:
//
//	go run ./cmd/mkwinres
package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"

	"nfsrun-server/internal/winres"
)

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="NFSRun.Launcher" version="1.0.0.0" processorArchitecture="*"/>
  <description>NFS The Run private server launcher</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
      <supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, PerMonitor</dpiAwareness>
    </windowsSettings>
  </application>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>
`

// Logo: dark rounded square with three orange speed bars and a white road
// line. Drawn with 4x4 supersampling.
type shape struct {
	in  func(x, y float64) bool
	col color.NRGBA
}

func roundRect(x0, y0, x1, y1, r float64) func(x, y float64) bool {
	return func(x, y float64) bool {
		if x < x0 || x > x1 || y < y0 || y > y1 {
			return false
		}
		cx := math.Max(x0+r, math.Min(x, x1-r))
		cy := math.Max(y0+r, math.Min(y, y1-r))
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
	}
}

// slanted bar: left edge at x0 (bottom) .. x1, leaning right by k per unit up
func bar(x0, x1, y0, y1, k float64) func(x, y float64) bool {
	return func(x, y float64) bool {
		if y < y0 || y > y1 {
			return false
		}
		s := (y1 - y) * k
		return x >= x0+s && x <= x1+s
	}
}

var bg = roundRect(0.03, 0.03, 0.97, 0.97, 0.2)

func clip(f func(x, y float64) bool) func(x, y float64) bool {
	return func(x, y float64) bool { return bg(x, y) && f(x, y) }
}

var logo = []shape{
	{bg, color.NRGBA{0x14, 0x14, 0x14, 0xff}},
	{clip(func(x, y float64) bool { return y < 0.11 }), color.NRGBA{0xff, 0x7a, 0x00, 0xff}},
	{bar(0.30, 0.82, 0.25, 0.37, 0.45), color.NRGBA{0xff, 0x7a, 0x00, 0xff}},
	{bar(0.22, 0.70, 0.43, 0.55, 0.45), color.NRGBA{0xff, 0x95, 0x2b, 0xff}},
	{bar(0.14, 0.56, 0.61, 0.73, 0.45), color.NRGBA{0xff, 0xb0, 0x5c, 0xff}},
	{clip(bar(0.10, 0.90, 0.81, 0.86, 0.0)), color.NRGBA{0xf2, 0xf2, 0xf2, 0xff}},
}

func render(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/ss) / float64(size)
					var c color.NRGBA
					for _, s := range logo {
						if s.in(x, y) {
							c = s.col
						}
					}
					fa := float64(c.A) / 255
					r += float64(c.R) * fa
					g += float64(c.G) * fa
					b += float64(c.B) * fa
					a += fa
				}
			}
			n := float64(ss * ss)
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / n)})
		}
	}
	return img
}

func main() {
	var b winres.Builder
	var imgs []image.Image
	for _, s := range []int{16, 20, 24, 32, 40, 48, 64, 256} {
		imgs = append(imgs, render(s))
	}
	if err := b.AddIcon(1, imgs); err != nil {
		panic(err)
	}
	b.AddManifest(manifest)
	// Windows wants a version resource; the project itself has no versions.
	vs := "1.0.0.0"
	b.AddVersion(winres.Version{Major: 1, Strings: map[string]string{
		"CompanyName":      "NFS The Run private server project",
		"FileDescription":  "NFS The Run - private server launcher",
		"FileVersion":      vs,
		"InternalName":     "nfsrun-launcher",
		"LegalCopyright":   "Community project. Need for Speed and The Run belong to their owners.",
		"OriginalFilename": "nfsrun-launcher.exe",
		"ProductName":      "NFS The Run private server",
		"ProductVersion":   vs,
	}})
	dir := filepath.Join("cmd", "nfsrun-launcher")
	for _, m := range []struct {
		arch    string
		machine uint16
	}{{"386", winres.I386}, {"amd64", winres.AMD64}} {
		out, err := b.COFF(m.machine)
		if err != nil {
			panic(err)
		}
		p := filepath.Join(dir, "rsrc_windows_"+m.arch+".syso")
		if err := os.WriteFile(p, out, 0644); err != nil {
			panic(err)
		}
		fmt.Println("wrote", p, len(out), "bytes")
	}
}
