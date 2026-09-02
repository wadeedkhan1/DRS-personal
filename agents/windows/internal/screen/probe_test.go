package screen

import (
	"context"
	"image"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/pion/mediadevices/pkg/codec/vpx"
	mvideo "github.com/pion/mediadevices/pkg/io/video"
	"github.com/pion/mediadevices/pkg/prop"
)

// TestProbePipeline runs the exact production capture -> convert -> VP8 path with no
// WebRTC and no network, so a stall can be attributed to one stage. A watchdog dumps
// every goroutine stack if a frame takes too long, which is the only way to see where a
// hang actually lives.
func TestProbePipeline(t *testing.T) {
	firstImg, err := capture()
	if err != nil {
		t.Skipf("no desktop to capture: %v", err)
	}
	first := firstImg.(*image.RGBA)
	b := first.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	w, h := targetSize(srcW, srcH, defaultMaxWidth)
	fps := defaultFPS
	t.Logf("source %dx%d -> target %dx%d @ %d fps", srcW, srcH, w, h, fps)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipe := newPipeline(ctx, srcW, srcH, w, h, fps)
	pipe.start(first)

	params, err := vpx.NewVP8Params()
	if err != nil {
		t.Fatal(err)
	}
	params.BitRate = bitRateFor(w, h, fps)
	params.RateControlEndUsage = vpx.RateControlCBR
	params.LagInFrames = 0
	params.RateControlMaxQuantizer = 52
	params.KeyFrameInterval = fps * 2

	enc, err := params.BuildVideoEncoder(mvideo.ToI420(mvideo.ReaderFunc(pipe.read)), prop.Media{
		Video: prop.Video{Width: w, Height: h, FrameRate: float32(fps)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	// Watchdog: if any single Read takes over 5s, dump all stacks and die. Without this a
	// hang is indistinguishable from slowness.
	progress := make(chan int, 512)
	go func() {
		for {
			select {
			case <-progress:
			case <-time.After(20 * time.Second):
				os.Stdout.WriteString("\n=== WATCHDOG: no frame for 20s, goroutine dump ===\n")
				pprof.Lookup("goroutine").WriteTo(os.Stdout, 2)
				os.Exit(9)
			case <-ctx.Done():
				return
			}
		}
	}()

	start := time.Now()
	for i := 0; i < 120; i++ {
		t0 := time.Now()
		chunk, release, rerr := enc.Read()
		d := time.Since(t0)
		progress <- i
		if rerr != nil {
			t.Fatalf("frame %d: read failed after %v: %v", i, d, rerr)
		}
		if i == 0 || i%40 == 0 || d > 250*time.Millisecond {
			t.Logf("frame %3d: %6d bytes in %8v (published=%d delivered=%d)",
				i, len(chunk), d, pipe.published.Load(), pipe.delivered.Load())
		}
		release()
	}
	elapsed := time.Since(start)
	t.Logf("120 frames in %v = %.1f fps (published=%d delivered=%d)",
		elapsed, 120/elapsed.Seconds(), pipe.published.Load(), pipe.delivered.Load())
}
