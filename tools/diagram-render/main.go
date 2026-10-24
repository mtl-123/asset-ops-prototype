package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type diagram struct {
	Stem          string
	SVG           string
	PNG           string
	Preview       string
	HTML          string
	Title         string
	PreviewWidth  int
	PreviewHeight int
	Record        string
	Branches      int
	Rules         int
}

var diagrams = []diagram{
	{Stem: "福利物资审计思维导图", SVG: "福利物资审计思维导图.svg", PNG: "福利物资审计思维导图.png", Preview: "福利物资审计思维导图-预览.png", HTML: "福利物资审计思维导图.html", Title: "审批与审计逻辑思维导图", PreviewWidth: 1600, PreviewHeight: 1800, Record: "福利物资审计思维导图-生成记录.json", Branches: 10, Rules: 64},
	{Stem: "福利物资-角色审批关系图", SVG: "福利物资-角色审批关系图.svg", PNG: "福利物资-角色审批关系图.png", Preview: "福利物资-角色审批关系图-预览.png", HTML: "福利物资-角色审批关系图.html", Title: "角色与审批关系", PreviewWidth: 1680, PreviewHeight: 1200},
	{Stem: "物品资产-角色审批与财务对账图", SVG: "物品资产-角色审批与财务对账图.svg", PNG: "物品资产-角色审批与财务对账图.png", Preview: "物品资产-角色审批与财务对账图-预览.png", HTML: "物品资产-角色审批与财务对账图.html", Title: "角色审批与财务对账", PreviewWidth: 1800, PreviewHeight: 1300},
}

var svgSize = regexp.MustCompile(`<svg[^>]*\bwidth="([0-9]+)"[^>]*\bheight="([0-9]+)"`)

func main() {
	mode := flag.String("mode", "validate", "validate or render")
	source := flag.String("source", "", "directory containing canonical SVG files")
	out := flag.String("out", "", "output directory; defaults to source")
	chrome := flag.String("chrome", "", "Chrome/Chromium executable used for PNG rendering")
	flag.Parse()

	root, err := resolveRoot(*source)
	fatalIf(err)
	target := *out
	if target == "" {
		target = root
	}
	target, err = filepath.Abs(target)
	fatalIf(err)
	fatalIf(os.MkdirAll(target, 0o755))

	switch *mode {
	case "validate":
		for _, spec := range diagrams {
			fatalIf(validateArtifacts(root, spec))
			fmt.Printf("validated %s\n", spec.Stem)
		}
	case "render":
		browser, err := findChrome(*chrome)
		fatalIf(err)
		for _, spec := range diagrams {
			fatalIf(renderDiagram(root, target, browser, spec))
			fmt.Printf("rendered %s\n", spec.Stem)
		}
	default:
		fatalIf(fmt.Errorf("unsupported mode %q", *mode))
	}
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "diagram-render:", err)
		os.Exit(1)
	}
}

func resolveRoot(value string) (string, error) {
	if value != "" {
		return filepath.Abs(value)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, diagrams[0].SVG)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("cannot locate workspace root; pass -source")
		}
		dir = parent
	}
}

func dimensions(svg []byte) (int, int, error) {
	match := svgSize.FindSubmatch(svg)
	if len(match) != 3 {
		return 0, 0, errors.New("SVG width and height are missing")
	}
	w, err := strconv.Atoi(string(match[1]))
	if err != nil {
		return 0, 0, err
	}
	h, err := strconv.Atoi(string(match[2]))
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func validateArtifacts(root string, spec diagram) error {
	svg, err := os.ReadFile(filepath.Join(root, spec.SVG))
	if err != nil {
		return err
	}
	w, h, err := dimensions(svg)
	if err != nil {
		return fmt.Errorf("%s: %w", spec.SVG, err)
	}
	if w < 100 || h < 100 || !bytes.Contains(svg, []byte("<title>")) {
		return fmt.Errorf("%s is not a complete diagram SVG", spec.SVG)
	}
	for _, name := range []string{spec.PNG, spec.Preview, spec.HTML} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if info.Size() == 0 {
			return fmt.Errorf("%s is empty", name)
		}
	}
	img, err := readPNG(filepath.Join(root, spec.PNG))
	if err != nil {
		return err
	}
	if img.Bounds().Dx() != w*2 || img.Bounds().Dy() != h*2 {
		return fmt.Errorf("%s dimensions %dx%d, want %dx%d", spec.PNG, img.Bounds().Dx(), img.Bounds().Dy(), w*2, h*2)
	}
	preview, err := readPNG(filepath.Join(root, spec.Preview))
	if err != nil {
		return err
	}
	if preview.Bounds().Dx() > spec.PreviewWidth || preview.Bounds().Dy() > spec.PreviewHeight {
		return fmt.Errorf("%s exceeds preview bounds", spec.Preview)
	}
	return nil
}

func renderDiagram(source, out, chrome string, spec diagram) error {
	svg, err := os.ReadFile(filepath.Join(source, spec.SVG))
	if err != nil {
		return err
	}
	w, h, err := dimensions(svg)
	if err != nil {
		return err
	}
	if filepath.Clean(source) != filepath.Clean(out) {
		if err := os.WriteFile(filepath.Join(out, spec.SVG), svg, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(out, spec.HTML), viewer(spec, svg, w, h), 0o644); err != nil {
		return err
	}
	if err := rasterize(chrome, out, spec, svg, w, h); err != nil {
		return err
	}
	if err := createPreview(filepath.Join(out, spec.PNG), filepath.Join(out, spec.Preview), spec.PreviewWidth, spec.PreviewHeight); err != nil {
		return err
	}
	if spec.Record != "" {
		record := map[string]any{"width": w, "height": h, "png_size": []int{w * 2, h * 2}, "branches": spec.Branches, "rules": spec.Rules, "renderer": "Go orchestration with headless Chrome SVG rasterization", "source": spec.SVG}
		data, _ := json.MarshalIndent(record, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(filepath.Join(out, spec.Record), data, 0o644); err != nil {
			return err
		}
	}
	return validateArtifacts(out, spec)
}

func viewer(spec diagram, svg []byte, w, h int) []byte {
	return []byte(fmt.Sprintf(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>*{box-sizing:border-box}body{margin:0;background:#f5f7fc;font-family:"Microsoft YaHei",sans-serif;color:#203553}header{min-height:64px;padding:12px 22px;display:flex;align-items:center;gap:10px;background:#fff;border-bottom:1px solid #dce4ef;position:sticky;top:0;z-index:2}header strong{margin-right:auto}button,a{padding:8px 12px;border:1px solid #ccd8e8;border-radius:7px;background:#fff;color:#315fbd;text-decoration:none;cursor:pointer}main{height:calc(100vh - 64px);overflow:auto;display:grid;place-items:start center}#diagram{line-height:0;flex:none}svg{display:block;width:100%%;height:auto}@media(max-width:760px){header{flex-wrap:wrap}header strong{width:100%%}main{height:calc(100vh - 110px)}}@media print{header{display:none}main{height:auto;overflow:visible}#diagram{width:100%%!important}}</style></head><body><header><strong>%s</strong><button id="minus" aria-label="缩小">−</button><span id="zoom"></span><button id="plus" aria-label="放大">＋</button><button id="fit">适应窗口</button><a href="%s" download>高清 PNG</a><a href="%s" download>SVG</a></header><main id="stage"><div id="diagram">%s</div></main><script>const box=document.getElementById('diagram'),stage=document.getElementById('stage');let scale=1;function apply(){box.style.width=Math.round(%d*scale)+'px';document.getElementById('zoom').textContent=Math.round(scale*100)+'%%'}function fit(){scale=Math.min((stage.clientWidth-24)/%d,(stage.clientHeight-24)/%d);apply()}document.getElementById('plus').onclick=()=>{scale=Math.min(3,scale*1.25);apply()};document.getElementById('minus').onclick=()=>{scale=Math.max(.1,scale/1.25);apply()};document.getElementById('fit').onclick=fit;window.addEventListener('resize',fit);fit();</script></body></html>`, html.EscapeString(spec.Title), html.EscapeString(spec.Title), url.PathEscape(spec.PNG), url.PathEscape(spec.SVG), svg, w, w, h))
}

func findChrome(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if runtimeChrome := windowsChrome(); runtimeChrome != "" {
		if _, err := os.Stat(runtimeChrome); err == nil {
			return runtimeChrome, nil
		}
	}
	return "", errors.New("Chrome/Chromium not found for this operating system; pass -chrome or run the renderer on Windows")
}

func windowsChrome() string {
	if os.PathSeparator == '\\' {
		return `C:\Program Files\Google\Chrome\Application\chrome.exe`
	}
	return ""
}

func rasterize(chrome, out string, spec diagram, svg []byte, w, h int) error {
	temp := filepath.Join(out, "."+spec.Stem+".render.html")
	page := fmt.Sprintf(`<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:%dpx;height:%dpx;overflow:hidden;background:#fff}svg{display:block;width:%dpx;height:%dpx}</style>%s`, w, h, w, h, svg)
	if err := os.WriteFile(temp, []byte(page), 0o600); err != nil {
		return err
	}
	defer os.Remove(temp)
	output := filepath.Join(out, spec.PNG)
	inputAbs, err := filepath.Abs(temp)
	if err != nil {
		return err
	}
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	pageURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(inputAbs)}).String()
	cmd := exec.Command(chrome, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--force-device-scale-factor=2", fmt.Sprintf("--window-size=%d,%d", w, h), "--screenshot="+outputAbs, pageURL)
	if data, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Chrome rasterization failed: %w: %s", err, strings.TrimSpace(string(data)))
	}
	return nil
}

func readPNG(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

func createPreview(source, target string, maxW, maxH int) error {
	img, err := readPNG(source)
	if err != nil {
		return err
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	scale := min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	if scale > 1 {
		scale = 1
	}
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			sx := img.Bounds().Min.X + x*w/nw
			sy := img.Bounds().Min.Y + y*h/nh
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, dst)
}
