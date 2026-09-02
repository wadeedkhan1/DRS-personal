package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"sync"
	"time"

	"drs/agent/windows/internal/protocol"

	"github.com/pion/mediadevices/pkg/codec/vpx"
	mvideo "github.com/pion/mediadevices/pkg/io/video"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Capture and encoding bounds. The backend proposes fps and width; the agent clamps
// them so a buggy or hostile command cannot ask for a firehose.
const (
	minFPS     = 1
	maxFPS     = 60
	defaultFPS = 24

	minMaxWidth = 320
	maxMaxWidth = 3840
	// 1280 rather than 1600. Every stage downstream of capture costs time proportional
	// to pixel count, and 720p is the point where a soft-real-time pure-Go pipeline can
	// hold a smooth frame rate on ordinary hardware. Text stays legible because the
	// downscale box-filters rather than point-samples.
	defaultMaxWidth = 1280

	// Bitrate ceiling and floor. The actual target is derived from resolution and frame
	// rate, because a fixed number is either wasteful at 480p or starves 1080p.
	minBitRate = 1_200_000
	maxBitRate = 8_000_000
)

// bitRateFor picks a target bitrate for a resolution and frame rate.
//
// The divisor is empirical for VP8 on screen content, which compresses far better than
// camera video: large flat regions, but sharp edges that suffer visibly when the encoder
// runs out of bits. 2 Mbps at 1600x900 (the previous fixed value) was well under what
// this content needs, which is why the picture looked soft and blocky.
func bitRateFor(w, h, fps int) int {
	rate := w * h * fps / 6
	if rate < minBitRate {
		return minBitRate
	}
	if rate > maxBitRate {
		return maxBitRate
	}
	return rate
}

// vp8Session is one live peer-to-peer video session.
//
// The agent is the offerer: it produces the media, so it describes the stream and the
// browser answers. That also means the browser needs no capability to initiate
// anything, which keeps the trust direction one-way.
type vp8Session struct {
	pc     *webrtc.PeerConnection
	send   FrameSender
	sessID string
	cancel context.CancelFunc

	mu          sync.Mutex
	remoteSet   bool
	pendingCand []webrtc.ICECandidateInit

	closeOnce sync.Once
}

// newVP8Session builds the peer connection and track synchronously, so a setup failure
// is reported immediately, then negotiates and streams in the background.
func newVP8Session(ctx context.Context, cmd protocol.StartSession, send FrameSender) (session, error) {
	// Grab one frame up front: it proves capture works before a peer connection is
	// built, and it fixes the stream dimensions for the encoder.
	firstImg, err := capture()
	if err != nil {
		return nil, err
	}
	first, ok := firstImg.(*image.RGBA)
	if !ok {
		return nil, errors.New("screen capture returned an unexpected pixel format")
	}

	srcB := first.Bounds()
	srcW, srcH := srcB.Dx(), srcB.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, errors.New("captured display has zero size")
	}

	maxWidth := clamp(cmd.MaxWidth, minMaxWidth, maxMaxWidth, defaultMaxWidth)
	w, h := targetSize(srcW, srcH, maxWidth)
	fps := clamp(cmd.FPS, minFPS, maxFPS, defaultFPS)

	pcConfig := webrtc.Configuration{ICEServers: toPionICE(cmd.ICEServers)}
	if cmd.ICETransportPolicy == "relay" {
		// Discard host and server-reflexive candidates, leaving only TURN. Both peers
		// must agree on this: if only one side restricts itself, it offers nothing the
		// other can pair with and the session never connects.
		pcConfig.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}

	pc, err := webrtc.NewPeerConnection(pcConfig)
	if err != nil {
		return nil, err
	}

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "drs-screen",
	)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	rtpSender, err := pc.AddTrack(track)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	sctx, cancel := context.WithCancel(ctx)
	s := &vp8Session{pc: pc, send: send, sessID: cmd.SessionID, cancel: cancel}

	// Drain RTCP from the sender. Nothing here reads the payload, but the read is what
	// drives Pion's interceptors, so without it congestion control and NACK handling
	// never run and the stream degrades badly on a lossy link.
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, rerr := rtpSender.Read(buf); rerr != nil {
				return
			}
		}
	}()

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // end of candidates
		}
		j := c.ToJSON()
		s.sendEnvelope(protocol.TypeICECandidate, protocol.ICECandidate{
			SessionID:        s.sessID,
			Candidate:        j.Candidate,
			SDPMid:           j.SDPMid,
			SDPMLineIndex:    j.SDPMLineIndex,
			UsernameFragment: j.UsernameFragment,
		})
	})

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("agent: session %s peer state %s", short(s.sessID), st)
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			s.Close()
		}
	})

	// Capture and conversion run ahead of the encoder on their own goroutine, so a slow
	// encode never stalls capture and the encoder always gets the newest frame.
	pipe := newPipeline(sctx, srcW, srcH, w, h, fps)
	pipe.start(first)

	go s.negotiateAndStream(sctx, pipe, w, h, fps, track)
	return s, nil
}

// negotiateAndStream announces the session, offers, then runs capture -> VP8 -> track
// until the context is cancelled or the connection drops.
func (s *vp8Session) negotiateAndStream(ctx context.Context, pipe *pipeline, w, h, fps int, track *webrtc.TrackLocalStaticSample) {
	defer s.Close()

	// Announced before the offer so the browser can build its RTCPeerConnection and be
	// ready for a track, rather than racing the offer.
	s.sendEnvelope(protocol.TypeSessionReady, protocol.SessionReady{
		SessionID: s.sessID, Mode: protocol.ModeWebRTC,
	})

	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		s.reportError(err)
		return
	}
	if err := s.pc.SetLocalDescription(offer); err != nil {
		s.reportError(err)
		return
	}
	s.sendEnvelope(protocol.TypeOffer, protocol.SDP{
		SessionID: s.sessID, SDPType: "offer", SDP: offer.SDP,
	})

	interval := time.Second / time.Duration(fps)

	// The pipeline already delivers rate-paced I420 frames, so ToI420 below finds an
	// *image.YCbCr and reduces to a struct copy instead of converting every pixel.
	reader := mvideo.ReaderFunc(pipe.read)

	params, err := vpx.NewVP8Params()
	if err != nil {
		s.reportError(err)
		return
	}
	params.BitRate = bitRateFor(w, h, fps)

	// Deadline is deliberately NOT overridden. NewVP8Params already defaults it to
	// VPX_DL_REALTIME, and that constant is the literal value 1 (microsecond) which
	// libvpx compares against exactly — so setting a "nicer looking" 1ms silently asks
	// for a different encoder mode rather than a slightly relaxed realtime one.

	// Constant bitrate. The VP8 default is VBR, which lets the encoder bank bits during
	// still moments and spend them in bursts — pleasant for offline video, bad here,
	// because a burst arriving over a constrained link is exactly what shows up as a
	// latency spike when the screen starts moving.
	params.RateControlEndUsage = vpx.RateControlCBR

	// No lookahead. Any lag buffers frames inside the encoder before it emits them,
	// which is latency that no amount of network tuning can recover.
	params.LagInFrames = 0

	// Cap how far quality is allowed to fall. With the ceiling at the default 63 the
	// encoder will happily produce a smeared, blocky picture to defend the bitrate;
	// stopping short of that keeps text readable and lets the rate controller drop the
	// frame rate instead, which is the better trade for a screen.
	params.RateControlMaxQuantizer = 52

	// A keyframe every two seconds. Without periodic keyframes a viewer that joins late,
	// or drops a packet, has nothing to resynchronise on and sits on a smeared image.
	params.KeyFrameInterval = fps * 2

	log.Printf("agent: session %s encoding %dx%d @ %d fps, %d kbps",
		short(s.sessID), w, h, fps, params.BitRate/1000)

	enc, err := params.BuildVideoEncoder(mvideo.ToI420(reader), prop.Media{
		Video: prop.Video{Width: w, Height: h, FrameRate: float32(fps)},
	})
	if err != nil {
		s.reportError(err)
		return
	}
	defer enc.Close() //nolint:errcheck

	// Counters for the periodic report below. A stream that connects and then shows
	// nothing is the hardest failure to diagnose from the outside, so the encode loop
	// says what it is actually producing rather than failing silently.
	var samples, encodedBytes, emptyChunks, writeErrors int64
	var firstSample bool
	lastReport := time.Now()
	log.Printf("agent: session %s encode loop started", short(s.sessID))

	for {
		if ctx.Err() != nil {
			return
		}

		// Reported at the top of the loop, before any path that might skip it. The
		// previous version reported after the write and used `continue` for empty
		// chunks, so an encoder that emitted nothing produced no log line at all — the
		// one situation where the log mattered most.
		if elapsed := time.Since(lastReport); elapsed >= 10*time.Second {
			log.Printf("agent: session %s %d frames out (%.1f fps, %.0f kbps); "+
				"pipeline published=%d delivered=%d%s",
				short(s.sessID), samples,
				float64(samples)/elapsed.Seconds(),
				float64(encodedBytes*8)/elapsed.Seconds()/1000,
				pipe.published.Load(), pipe.delivered.Load(),
				emptyOrErrSuffix(emptyChunks, writeErrors))
			samples, encodedBytes, emptyChunks, writeErrors = 0, 0, 0, 0
			lastReport = time.Now()
		}

		chunk, release, rerr := enc.Read()
		if rerr != nil {
			if ctx.Err() == nil && !errors.Is(rerr, io.EOF) {
				s.reportError(rerr)
			}
			return
		}

		if len(chunk) == 0 {
			// libvpx produced no packet for this frame. Writing an empty sample would
			// put a malformed packet on the wire, so skip it and count it instead.
			emptyChunks++
			if emptyChunks == 1 {
				log.Printf("agent: session %s encoder returned an empty packet", short(s.sessID))
			}
			release()
			continue
		}

		if err := track.WriteSample(media.Sample{Data: chunk, Duration: interval}); err != nil {
			writeErrors++
			// Previously discarded. A write that always fails is precisely the case
			// where the operator sees a connected session with no picture, so the first
			// failure is reported immediately rather than hidden.
			if writeErrors == 1 {
				log.Printf("agent: session %s could not write video sample: %v", short(s.sessID), err)
			}
			if writeErrors == 30 {
				s.reportError(errors.New("the video track is not accepting frames: " + err.Error()))
				release()
				return
			}
		} else {
			samples++
			encodedBytes += int64(len(chunk))
			if !firstSample {
				firstSample = true
				log.Printf("agent: session %s first video sample sent (%d bytes)", short(s.sessID), len(chunk))
			}
		}
		release()

		if elapsed := time.Since(lastReport); elapsed >= 10*time.Second {
			log.Printf("agent: session %s sent %d frames (%.1f fps, %.0f kbps)%s",
				short(s.sessID), samples,
				float64(samples)/elapsed.Seconds(),
				float64(encodedBytes*8)/elapsed.Seconds()/1000,
				emptyOrErrSuffix(emptyChunks, writeErrors))
			samples, encodedBytes, emptyChunks, writeErrors = 0, 0, 0, 0
			lastReport = time.Now()
		}
	}
}

// emptyOrErrSuffix appends the abnormal counters only when they are non-zero, so the
// common case stays a one-line report.
func emptyOrErrSuffix(empty, writeErrs int64) string {
	switch {
	case empty == 0 && writeErrs == 0:
		return ""
	case writeErrs == 0:
		return fmt.Sprintf(", %d empty chunks", empty)
	default:
		return fmt.Sprintf(", %d empty chunks, %d write errors", empty, writeErrs)
	}
}

// HandleAnswer applies the browser's answer and flushes candidates that arrived first.
func (s *vp8Session) HandleAnswer(sdp string) {
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: sdp,
	}); err != nil {
		log.Printf("agent: session %s set answer: %v", short(s.sessID), err)
		return
	}

	s.mu.Lock()
	s.remoteSet = true
	pending := s.pendingCand
	s.pendingCand = nil
	s.mu.Unlock()

	for _, c := range pending {
		if err := s.pc.AddICECandidate(c); err != nil {
			log.Printf("agent: session %s buffered candidate rejected: %v", short(s.sessID), err)
		}
	}
}

// HandleICECandidate applies a candidate, buffering it if the answer has not arrived.
//
// Trickle ICE means candidates routinely overtake the answer. AddICECandidate before
// SetRemoteDescription is an error, and dropping those candidates silently is a classic
// cause of a session that negotiates cleanly and then never produces video.
func (s *vp8Session) HandleICECandidate(c protocol.ICECandidate) {
	if c.Candidate == "" {
		return
	}
	init := webrtc.ICECandidateInit{
		Candidate:        c.Candidate,
		SDPMid:           c.SDPMid,
		SDPMLineIndex:    c.SDPMLineIndex,
		UsernameFragment: c.UsernameFragment,
	}

	s.mu.Lock()
	if !s.remoteSet {
		s.pendingCand = append(s.pendingCand, init)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if err := s.pc.AddICECandidate(init); err != nil {
		log.Printf("agent: session %s candidate rejected: %v", short(s.sessID), err)
	}
}

// Close tears the session down exactly once.
func (s *vp8Session) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.pc.Close()
	})
}

func (s *vp8Session) sendEnvelope(t protocol.MsgType, payload any) {
	frame, err := protocol.Encode(t, payload)
	if err != nil {
		return
	}
	_ = s.send(frame)
}

func (s *vp8Session) reportError(err error) {
	log.Printf("agent: session %s webrtc error: %v", short(s.sessID), err)
	s.sendEnvelope(protocol.TypeSessionError, protocol.SessionErrorMsg{
		SessionID: s.sessID, Message: err.Error(),
	})
}

// toPionICE converts the wire ICE list to Pion's config type. The list comes entirely
// from the backend so that both peers use an identical one.
func toPionICE(in []protocol.ICEServer) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(in))
	for _, srv := range in {
		e := webrtc.ICEServer{URLs: srv.URLs}
		if srv.Username != "" {
			e.Username = srv.Username
			e.Credential = srv.Credential
		}
		out = append(out, e)
	}
	return out
}
