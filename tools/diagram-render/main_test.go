package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDimensionsAndViewer(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80"><title>T</title></svg>`)
	w, h, err := dimensions(svg)
	if err != nil || w != 120 || h != 80 {
		t.Fatalf("dimensions=%dx%d err=%v", w, h, err)
	}
	page := string(viewer(diagram{Title: "图表", PNG: "图.png", SVG: "图.svg"}, svg, w, h))
	for _, value := range []string{"适应窗口", "高清 PNG", string(svg)} {
		if !strings.Contains(page, value) {
			t.Fatalf("viewer missing %q", value)
		}
	}
}

func TestCreatePreview(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.png")
	target := filepath.Join(dir, "preview.png")
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), A: 255})
		}
	}
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := createPreview(source, target, 100, 100); err != nil {
		t.Fatal(err)
	}
	preview, err := readPNG(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.Bounds().Size(); got.X != 100 || got.Y != 50 {
		t.Fatalf("preview size=%v", got)
	}
}

func TestCheckedInArtifacts(t *testing.T) {
	root, err := resolveRoot("")
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range diagrams {
		if err := validateArtifacts(root, spec); err != nil {
			t.Fatalf("%s: %v", spec.Stem, err)
		}
	}
}
