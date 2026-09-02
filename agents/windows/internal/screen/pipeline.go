package screen

import (
	"context"
	"errors"
	"image"
	"io"
	"runtime"
	"sync/atomic"
	"time"
)

// pipeline runs capture and conversion on its own goroutine, ahead of the encoder.
//
// The reason this exists: mediadevices' encoder *pulls* frames, so if capture and
// conversion happen inside the pull, every frame costs capture + convert + encode added
// together and nothing overlaps. Splitting them lets conversion of the next frame run
// while the current one is still encoding.
//
// It also fixes a latency problem that is easy to miss. A pull-based capture encodes
// whatever the screen looked like when the encoder got round to asking. Here the
// capturer always overwrites the pending frame with a newer one, so the encoder is
// always working on the freshest frame available and stale frames are dropped rather
// than queued. Queued frames are pure latency: nobody wants to watch a smooth stream of
// what happened a second ago.
//
// Two hard constraints from the layers on either side shape the rest of this file, and
// both were learned the hard way — each one produced a session that connected, sent a
// single keyframe, and then hung forever:
//
//  1. mediadevices' ToI420 discards the release func a Reader returns (it calls
//     "img, _, err := r.Read()"), so a buffer can only be recycled on the *next* pull.
//  2. For an *image.YCbCr that is already 4:2:0, ToI420's imageToYCbCr is a struct copy
//     ("*dst = *yuvImg"), so libvpx encodes straight out of this buffer. Writing to a
//     frame that has been handed out is a live data race with the encoder, not a
//     harmless overwrite.
//
// Together those mean the buffer handed to the encoder is off limits until the encoder
// asks for another one, which is exactly when it is known to be finished with it.
type pipeline struct {
	scaler *i420Scaler
	fps    int

	// frames holds at most one frame ready to encode; free holds buffers nobody owns.
	// Ownership moves with the buffer, so neither needs a lock.
	frames chan *image.YCbCr
	free   chan *image.YCbCr
	errCh  chan error

	// inFlight is the buffer the encoder is currently reading from. Touched only by
	// read, which only ever runs on the encoder's goroutine, so it needs no lock.
	inFlight *image.YCbCr

	// gate serialises GDI screen capture against the libvpx encode call.
	//
	// This is not a data lock — the two touch nothing in common. It exists because a
	// BitBlt against the desktop DC that overlaps a libvpx encode on another thread
	// wedges inside win32k and never returns, killing the stream. That reproduces
	// deterministically: capture alone runs at 30 fps indefinitely, capture plus the
	// parallel colour conversion holds a clean 24 fps indefinitely, and adding the
	// encoder hangs the third or fourth BitBlt permanently. Pinning the capture
	// goroutine to one OS thread does not help, so the conflict is between the two
	// calls themselves rather than thread affinity.
	//
	// The reference implementation never met this because it captured inside the
	// encoder's own pull, serialising the two by construction. Holding a token instead
	// keeps the stages on separate goroutines — conversion still overlaps encoding,
	// which is where most of the speedup came from — while preserving that guarantee.
	//
	// A buffered channel rather than a sync.Mutex, so both sides can give up on
	// ctx.Done(); a mutex would leave whichever goroutine lost the race blocked forever
	// when the session ends.
	gate chan struct{}

	// holdsGate tracks whether the encoder side currently owns the token. Same
	// single-goroutine argument as inFlight.
	holdsGate bool

	ctx context.Context

	// Counters for diagnosis. "published" is frames the capturer produced; "delivered"
	// is frames the encoder actually collected. If published climbs while delivered
	// stalls the encoder is the bottleneck; if neither moves, capture is.
	published atomic.Int64
	delivered atomic.Int64
}

// Three buffers, not two: one being encoded, one being converted, one waiting. With two,
// the capturer had nowhere to write until the encoder finished.
const pipelineBuffers = 3

func newPipeline(ctx context.Context, srcW, srcH, dstW, dstH, fps int) *pipeline {
	scaler := newI420Scaler(srcW, srcH, dstW, dstH)

	p := &pipeline{
		scaler: scaler,
		fps:    fps,
		frames: make(chan *image.YCbCr, 1),
		free:   make(chan *image.YCbCr, pipelineBuffers),
		errCh:  make(chan error, 1),
		gate:   make(chan struct{}, 1),
		ctx:    ctx,
	}
	for i := 0; i < pipelineBuffers; i++ {
		p.free <- scaler.newI420()
	}
	p.gate <- struct{}{}
	return p
}

// start launches the capture loop. first is the frame already grabbed for sizing, so the
// stream opens without waiting a full tick for one.
func (p *pipeline) start(first *image.RGBA) {
	go p.run(first)
}

func (p *pipeline) run(first *image.RGBA) {
	// GDI capture stays on one OS thread for the life of the session. This is not what
	// fixes the win32k hang described above — the gate is — but creating and destroying
	// device contexts from whichever thread the scheduler happened to pick is worth
	// avoiding on its own, and CGO elsewhere in the process makes goroutines migrate
	// between OS threads constantly.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	interval := time.Second / time.Duration(p.fps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(p.frames)

	frame := first
	for {
		if p.ctx.Err() != nil {
			return
		}

		if buf := p.acquire(); buf != nil {
			p.scaler.convert(buf, frame)
			p.publish(buf)
		}
		// If acquire returned nil every buffer is spoken for, i.e. the encoder is the
		// bottleneck right now. Skipping this capture is the correct response: producing
		// a frame nobody can consume would only add latency.

		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}

		if !p.lockGate() {
			return
		}
		img, err := capture()
		p.unlockGate()

		if err != nil {
			p.fail(err)
			return
		}
		rgba, ok := img.(*image.RGBA)
		if !ok {
			p.fail(errors.New("screen capture returned an unexpected pixel format"))
			return
		}
		frame = rgba
	}
}

// lockGate takes the capture/encode token, reporting false if the session ended while
// waiting for it.
func (p *pipeline) lockGate() bool {
	select {
	case <-p.gate:
		return true
	case <-p.ctx.Done():
		return false
	}
}

func (p *pipeline) unlockGate() {
	select {
	case p.gate <- struct{}{}:
	default:
		// Unreachable: the token is single and held here. Guarded anyway so a future
		// double release degrades rather than blocking the stream forever.
	}
}

func (p *pipeline) fail(err error) {
	select {
	case p.errCh <- err:
	default:
	}
}

// acquire takes a buffer to write into, preferring a free one and otherwise reclaiming
// the pending frame the encoder has not picked up yet. It can never reclaim the
// in-flight frame, which is what keeps the encoder's buffer stable while libvpx reads
// it.
func (p *pipeline) acquire() *image.YCbCr {
	select {
	case buf := <-p.free:
		return buf
	default:
	}
	// No free buffer: the pending frame is already stale, so reuse it rather than
	// letting the encoder deliver old content.
	select {
	case buf := <-p.frames:
		return buf
	default:
		return nil
	}
}

// publish makes buf the pending frame, discarding any frame not yet consumed.
func (p *pipeline) publish(buf *image.YCbCr) {
	select {
	case old := <-p.frames:
		p.free <- old
	default:
	}
	select {
	case p.frames <- buf:
		p.published.Add(1)
	default:
		// Raced with the encoder taking the slot; hand the buffer back.
		p.free <- buf
	}
}

// read is the encoder's frame source.
//
// The returned release func is deliberately a no-op: ToI420 drops it, so recycling and
// the gate hand-back both happen here on the following call instead. Arriving here is
// itself the proof that the encoder has finished with the previous frame.
func (p *pipeline) read() (image.Image, func(), error) {
	noop := func() {}

	// End of the previous encode: give the token back so capture can run, then recycle
	// the buffer libvpx has just stopped reading.
	if p.holdsGate {
		p.holdsGate = false
		p.unlockGate()
	}
	if p.inFlight != nil {
		select {
		case p.free <- p.inFlight:
		default:
		}
		p.inFlight = nil
	}

	select {
	case err := <-p.errCh:
		return nil, noop, err
	default:
	}

	var buf *image.YCbCr
	select {
	case <-p.ctx.Done():
		return nil, noop, io.EOF
	case err := <-p.errCh:
		return nil, noop, err
	case b, ok := <-p.frames:
		if !ok {
			return nil, noop, io.EOF
		}
		buf = b
	}

	// Taken only once a frame is in hand. Waiting for a frame while holding the token
	// would block the very capture that produces it.
	if !p.lockGate() {
		return nil, noop, io.EOF
	}
	p.holdsGate = true
	p.inFlight = buf
	p.delivered.Add(1)
	return buf, noop, nil
}
