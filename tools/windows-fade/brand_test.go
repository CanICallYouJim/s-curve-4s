package main

import (
	"image/png"
	"os"
	"testing"
)

func TestBrandAnimationAsset(t *testing.T) {
	file, err := os.Open("brand.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sprite, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if sprite.Bounds().Dx()%15 != 0 || sprite.Bounds().Dy()%9 != 0 {
		t.Fatal("invalid animation grid")
	}
	width, height := sprite.Bounds().Dx()/15, sprite.Bounds().Dy()/9
	for frame := 0; frame < 135; frame++ {
		ox, oy := (frame%15)*width, (frame/15)*height
		visible := 0
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				_, _, _, alpha := sprite.At(ox+x, oy+y).RGBA()
				if alpha > 0 {
					visible++
				}
			}
		}
		if visible == 0 || visible == width*height {
			t.Fatalf("frame %d needs a visible mark and transparent background", frame)
		}
	}
	different := false
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			a, b, c, _ := sprite.At(x, y).RGBA()
			d, e, f, _ := sprite.At(9*width+x, 2*height+y).RGBA() // t=1.3 s, fully gold.
			if a != d || b != e || c != f {
				different = true
			}
		}
	}
	if !different {
		t.Fatal("the brand animation is static")
	}
}
