// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

package assistant

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"strconv"

	golangdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// 动图原样交给视觉模型，多数只看得到第一帧：线上有张 GIF 第一帧像在睡觉，后面几帧是
// 别的动作，简介写成「侧卧在床上睡觉」，按「晚安」配出去被群友当场指出「并非睡觉图」。
// 所以动图先均匀抽几帧、按时间顺序拼成一张分镜图再给模型，每格左上角标上帧序号。
const (
	gifStoryboardMaxFrames = 9
	gifStoryboardCellSide  = 340
	gifStoryboardQuality   = 85
)

// gifStoryboard 把多帧 GIF 拼成一张 JPEG 分镜图，返回图和抽了几帧。单帧或解不开时返回 false，
// 调用方照旧处理原图。
func gifStoryboard(body []byte) ([]byte, int, bool) {
	animation, err := gif.DecodeAll(bytes.NewReader(body))
	if err != nil || len(animation.Image) < 2 {
		return nil, 0, false
	}
	frames := composeGIFFrames(animation, gifStoryboardFrameIndexes(len(animation.Image)))
	if len(frames) < 2 {
		return nil, 0, false
	}
	columns := 2
	if len(frames) > 4 {
		columns = 3
	}
	rows := (len(frames) + columns - 1) / columns
	bounds := frames[0].Bounds()
	cellWidth, cellHeight := gifStoryboardCellSide, gifStoryboardCellSide
	if bounds.Dx() > bounds.Dy() {
		cellHeight = max(1, gifStoryboardCellSide*bounds.Dy()/bounds.Dx())
	} else if bounds.Dy() > bounds.Dx() {
		cellWidth = max(1, gifStoryboardCellSide*bounds.Dx()/bounds.Dy())
	}
	const gap = 6
	canvas := image.NewRGBA(image.Rect(0, 0, columns*cellWidth+(columns+1)*gap, rows*cellHeight+(rows+1)*gap))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{R: 40, G: 40, B: 40, A: 255}), image.Point{}, draw.Src)
	for index, frame := range frames {
		column, row := index%columns, index/columns
		cell := image.Rect(gap+column*(cellWidth+gap), gap+row*(cellHeight+gap), gap+column*(cellWidth+gap)+cellWidth, gap+row*(cellHeight+gap)+cellHeight)
		// 透明像素按白底铺：贴纸多是透明背景，铺黑会看不清线稿。
		draw.Draw(canvas, cell, image.White, image.Point{}, draw.Src)
		golangdraw.CatmullRom.Scale(canvas, cell, frame, frame.Bounds(), draw.Over, nil)
		labelGIFFrame(canvas, cell.Min, index+1)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, canvas, &jpeg.Options{Quality: gifStoryboardQuality}); err != nil {
		return nil, 0, false
	}
	return encoded.Bytes(), len(frames), true
}

// gifStoryboardFrameIndexes 均匀抽帧，首帧和末帧都在里面。
func gifStoryboardFrameIndexes(total int) []int {
	count := min(total, gifStoryboardMaxFrames)
	indexes := make([]int, 0, count)
	for index := 0; index < count; index++ {
		frame := 0
		if count > 1 {
			frame = index * (total - 1) / (count - 1)
		}
		if len(indexes) == 0 || indexes[len(indexes)-1] != frame {
			indexes = append(indexes, frame)
		}
	}
	return indexes
}

// composeGIFFrames 按 GIF 的叠加和处置规则把帧依次画到画布上，取出指定几帧的完整画面。
// GIF 的后续帧常常只存变化的那一块，直接取单帧会是一小片碎图。
func composeGIFFrames(animation *gif.GIF, wanted []int) []image.Image {
	width, height := animation.Config.Width, animation.Config.Height
	if width <= 0 || height <= 0 {
		first := animation.Image[0].Bounds()
		width, height = first.Max.X, first.Max.Y
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	want := map[int]bool{}
	for _, index := range wanted {
		want[index] = true
	}
	frames := make([]image.Image, 0, len(wanted))
	for index, frame := range animation.Image {
		var previous *image.RGBA
		disposal := byte(0)
		if index < len(animation.Disposal) {
			disposal = animation.Disposal[index]
		}
		if disposal == gif.DisposalPrevious {
			previous = image.NewRGBA(canvas.Bounds())
			draw.Draw(previous, previous.Bounds(), canvas, image.Point{}, draw.Src)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if want[index] {
			snapshot := image.NewRGBA(canvas.Bounds())
			draw.Draw(snapshot, snapshot.Bounds(), canvas, image.Point{}, draw.Src)
			frames = append(frames, snapshot)
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			draw.Draw(canvas, canvas.Bounds(), previous, image.Point{}, draw.Src)
		}
	}
	return frames
}

// labelGIFFrame 在格子左上角画帧序号，让模型知道先后顺序。
func labelGIFFrame(canvas *image.RGBA, at image.Point, number int) {
	text := strconv.Itoa(number)
	face := basicfont.Face7x13
	box := image.Rect(at.X, at.Y, at.X+8+7*len(text), at.Y+17)
	draw.Draw(canvas, box, image.NewUniform(color.RGBA{R: 200, G: 30, B: 30, A: 255}), image.Point{}, draw.Src)
	drawer := font.Drawer{Dst: canvas, Src: image.White, Face: face, Dot: fixed.P(at.X+4, at.Y+13)}
	drawer.DrawString(text)
}
