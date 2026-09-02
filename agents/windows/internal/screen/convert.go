package screen

import (
	"image"
	"runtime"
	"sync"
)

// This file fuses downscaling and colour conversion into a single parallel pass.
//
// The obvious implementation — scale the RGBA image, then hand it to the encoder and
// let mediadevices convert it to I420 — walks the pixels twice, allocates an
// intermediate image, and does both passes on one core. At 1600x900 that measured out
// around 60-100ms per frame, which on its own caps the stream in single digits.
//
// Instead we read the captured RGBA once and write Y, Cb and Cr for the *target*
// resolution directly, spread across all cores. Because the result is already an
// *image.YCbCr with 4:2:0 subsampling, mediadevices' ToI420 recognises it and degrades
// to a struct copy (see imageToYCbCr in pkg/io/video/convert.go), so the conversion
// stage effectively disappears.

// span is the half-open source range that one destination pixel averages over.
type span struct{ lo, hi int }

// i420Scaler converts RGBA frames of a fixed source size into I420 frames of a fixed
// target size. The per-axis source ranges depend only on the two sizes, so they are
// computed once here rather than per pixel per frame.
type i420Scaler struct {
	srcW, srcH int
	dstW, dstH int
	xr         []span // one per destination column
	yr         []span // one per destination row
	workers    int
}

func newI420Scaler(srcW, srcH, dstW, dstH int) *i420Scaler {
	s := &i420Scaler{
		srcW: srcW, srcH: srcH,
		dstW: dstW, dstH: dstH,
		xr:      makeSpans(srcW, dstW),
		yr:      makeSpans(srcH, dstH),
		workers: runtime.NumCPU(),
	}
	if s.workers < 1 {
		s.workers = 1
	}
	// Capture and encode need cores too. Leaving one free measurably reduces the
	// stutter that comes from starving the encoder goroutine on a busy machine.
	if s.workers > 2 {
		s.workers--
	}
	return s
}

// makeSpans maps each destination index to the source range it covers. Averaging over
// that range (a box filter) is what keeps downscaled text legible: point sampling drops
// whole pixels, so thin strokes break up or vanish entirely.
func makeSpans(srcLen, dstLen int) []span {
	out := make([]span, dstLen)
	for i := range out {
		lo := i * srcLen / dstLen
		hi := (i + 1) * srcLen / dstLen
		if hi <= lo {
			hi = lo + 1
		}
		if hi > srcLen {
			hi = srcLen
		}
		out[i] = span{lo, hi}
	}
	return out
}

// newI420 allocates a frame buffer matching the scaler's target size.
func (s *i420Scaler) newI420() *image.YCbCr {
	return image.NewYCbCr(image.Rect(0, 0, s.dstW, s.dstH), image.YCbCrSubsampleRatio420)
}

// convert fills dst from src. Rows are processed in pairs because 4:2:0 chroma is
// shared by a 2x2 block of luma samples, so a pair of rows is the smallest unit that
// can be written independently of its neighbours — which is what makes it safe to hand
// different row pairs to different goroutines with no locking.
func (s *i420Scaler) convert(dst *image.YCbCr, src *image.RGBA) {
	b := src.Bounds()
	base := src.PixOffset(b.Min.X, b.Min.Y)
	stride := src.Stride

	rowPairs := (s.dstH + 1) / 2
	workers := s.workers
	if workers > rowPairs {
		workers = rowPairs
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			// Strided rather than contiguous blocks: screen content is rarely uniform,
			// so interleaving keeps the workers evenly loaded.
			for pair := start; pair < rowPairs; pair += workers {
				s.convertRowPair(dst, src, base, stride, pair*2)
			}
		}(w)
	}
	wg.Wait()
}

func (s *i420Scaler) convertRowPair(dst *image.YCbCr, src *image.RGBA, base, stride, y0 int) {
	pix := src.Pix

	for x0 := 0; x0 < s.dstW; x0 += 2 {
		// Accumulates the 2x2 block's colour for the shared chroma sample.
		var blockR, blockG, blockB, blockN int

		for dy := y0; dy < y0+2 && dy < s.dstH; dy++ {
			ys := s.yr[dy]
			yRow := dy * dst.YStride

			for dx := x0; dx < x0+2 && dx < s.dstW; dx++ {
				xs := s.xr[dx]

				// Box-average the source pixels behind this destination pixel.
				var r, g, bl, n int
				for sy := ys.lo; sy < ys.hi; sy++ {
					rowStart := base + sy*stride
					for sx := xs.lo; sx < xs.hi; sx++ {
						i := rowStart + sx*4
						r += int(pix[i])
						g += int(pix[i+1])
						bl += int(pix[i+2])
						n++
					}
				}
				if n == 0 {
					n = 1
				}
				r /= n
				g /= n
				bl /= n

				dst.Y[yRow+dx] = lumaFromRGB(r, g, bl)

				blockR += r
				blockG += g
				blockB += bl
				blockN++
			}
		}

		if blockN == 0 {
			continue
		}
		cb, cr := chromaFromRGB(blockR/blockN, blockG/blockN, blockB/blockN)
		ci := (y0/2)*dst.CStride + x0/2
		dst.Cb[ci] = cb
		dst.Cr[ci] = cr
	}
}

// BT.601 integer coefficients, the same ones image/color uses. Kept as two functions so
// the luma path (run per pixel) does no chroma work.

func lumaFromRGB(r, g, b int) uint8 {
	yy := (19595*r + 38470*g + 7471*b + 1<<15) >> 16
	if yy < 0 {
		return 0
	}
	if yy > 255 {
		return 255
	}
	return uint8(yy)
}

func chromaFromRGB(r, g, b int) (cb, cr uint8) {
	cb1 := (-11056*r - 21712*g + 32768*b + 257<<15) >> 16
	cr1 := (32768*r - 27440*g - 5328*b + 257<<15) >> 16
	return clamp8(cb1), clamp8(cr1)
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// targetSize picks the encoded resolution: the source scaled to fit maxWidth, with both
// dimensions forced even because 4:2:0 chroma is subsampled by two.
func targetSize(srcW, srcH, maxWidth int) (int, int) {
	w, h := srcW, srcH
	if srcW > maxWidth && srcW > 0 {
		w = maxWidth
		h = srcH * maxWidth / srcW
	}
	w &= ^1
	h &= ^1
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	return w, h
}
