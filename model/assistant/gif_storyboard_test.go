// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

package assistant

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// syntheticGIF 生成 frames 帧的动图：第一帧整幅纯色，之后每帧只画左上角一小块（GIF 常见的增量帧）。
func syntheticGIF(t *testing.T, frames int) []byte {
	t.Helper()
	palette := color.Palette{color.White, color.Black, color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	animation := &gif.GIF{Config: image.Config{Width: 60, Height: 40, ColorModel: palette}}
	for index := 0; index < frames; index++ {
		bounds := image.Rect(0, 0, 60, 40)
		if index > 0 {
			bounds = image.Rect(0, 0, 10, 10)
		}
		frame := image.NewPaletted(bounds, palette)
		for i := range frame.Pix {
			frame.Pix[i] = uint8(2 + index%3)
		}
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 5)
		animation.Disposal = append(animation.Disposal, gif.DisposalNone)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, animation); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// 动图抽 9 帧拼成 3×3 分镜，首末帧都在；增量帧叠在前一帧上，不是一小片碎图。
func TestGIFStoryboardSamplesFramesInOrder(t *testing.T) {
	if got := gifStoryboardFrameIndexes(12); len(got) != 9 || got[0] != 0 || got[8] != 11 {
		t.Fatalf("indexes = %v", got)
	}
	if got := gifStoryboardFrameIndexes(3); len(got) != 3 {
		t.Fatalf("indexes = %v", got)
	}
	body := syntheticGIF(t, 12)
	board, frames, ok := gifStoryboard(body)
	if !ok || frames != 9 {
		t.Fatalf("ok=%v frames=%d", ok, frames)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(board))
	if err != nil {
		t.Fatal(err)
	}
	if bounds := decoded.Bounds(); bounds.Dx() <= bounds.Dy() || bounds.Dx() > 3*gifStoryboardCellSide+24 {
		t.Fatalf("storyboard size = %v", bounds)
	}
	// 第二格是第 2 次抽到的帧：右下角仍是第一帧铺满的红色，说明增量帧叠在了完整画面上。
	cellWidth := (decoded.Bounds().Dx() - 4*6) / 3
	r, g, b, _ := decoded.At(6+cellWidth+6+cellWidth-5, 6+cellWidth*40/60-5).RGBA()
	if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Fatalf("second cell corner = %d,%d,%d, want the red base frame", r>>8, g>>8, b>>8)
	}
}

// 单帧 GIF 和普通图片不走分镜；送给模型的动图只剩一张 JPEG。
func TestAnimatedGIFPartsOnlyForAnimations(t *testing.T) {
	if _, ok := animatedGIFParts(syntheticGIF(t, 1)); ok {
		t.Fatal("single-frame gif was turned into a storyboard")
	}
	var pngBody bytes.Buffer
	_ = png.Encode(&pngBody, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	if _, ok := animatedGIFParts(pngBody.Bytes()); ok {
		t.Fatal("png was treated as gif")
	}
	parts, err := normalizeLLMImageParts(syntheticGIF(t, 5), "image/gif")
	if err != nil || len(parts) != 1 || !strings.HasPrefix(parts[0], "data:image/jpeg;base64,") {
		t.Fatalf("parts=%d err=%v prefix=%q", len(parts), err, parts[0][:min(30, len(parts[0]))])
	}
}
