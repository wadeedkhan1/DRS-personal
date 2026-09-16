// Package conn holds the outbound WebSocket the agent keeps open to the backend.
//
// The device dials out and keeps the socket open, so commands flow down it with no
// inbound port forwarding and no firewall exceptions. It reconnects with backoff, so a
// dropped network, a server restart or a laptop waking from sleep all recover without
// anyone intervening (SRS FR-2.6, NFR-4).
package conn

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"

	"drs/agent/windows/internal/config"
	"drs/agent/windows/internal/protocol"
	"drs/agent/windows/internal/screen"
	"drs/agent/windows/internal/sysinfo"
	"drs/agent/windows/internal/terminal"
)

const (
	agentVersion    = "1.0.0"
	minBackoff      = 1 * time.Second
	maxBackoff      = 30 * time.Second
	writeTimeout    = 10 * time.Second
	connectTimeout  = 20 * time.Second
	helloAckTimeout = 10 * time.Second
	readLimit       = 1 << 20 // 1 MiB
)

// StatusFunc is notified when the connection or session state changes, so the tray can
// show it. It must not block.
type StatusFunc func(online bool, inSession bool)

// Run connects and stays connected until ctx is cancelled.
//
// It returns nil on clean shutdown and an error only when the server said to stop for
// good, which is the difference that matters: a revoked secret must not turn into an
// infinite reconnect loop hammering the server.
func Run(ctx context.Context, cfg config.Config, onStatus StatusFunc, reconnect <-chan struct{}) error {
	backoff := minBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		sessCtx, cancelSess := context.WithCancel(ctx)
		go func() {
			select {
			case <-sessCtx.Done():
			case <-reconnect:
				log.Println("agent: manual reconnect triggered, aborting current session")
				cancelSess()
			}
		}()

		fatal, transient := session(sessCtx, cfg, onStatus)
		cancelSess()

		if fatal != nil {
			log.Printf("agent: fatal: %v — stopping", fatal)
			return fatal
		}
		if transient != nil {
			log.Printf("agent: connection ended: %v", transient)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-reconnect:
			log.Println("agent: manual reconnect triggered during backoff")
			backoff = minBackoff
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// session runs one connection attempt to completion, returning (fatal, transient).
func session(ctx context.Context, cfg config.Config, onStatus StatusFunc) (fatal, transient error) {
	dialCtx, cancelDial := context.WithTimeout(ctx, connectTimeout)
	defer cancelDial()

	c, _, err := websocket.Dial(dialCtx, cfg.WSURL, nil)
	if err != nil {
		return nil, err
	}
	defer c.CloseNow() //nolint:errcheck
	c.SetReadLimit(readLimit)

	hello, err := protocol.Encode(protocol.TypeHello, protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        cfg.DeviceID,
		AgentSecret:     cfg.AgentSecret,
		AgentVersion:    agentVersion,
	})
	if err != nil {
		return err, nil // our own bug, retrying will not help
	}

	sessCtx, endSession := context.WithCancel(ctx)
	defer endSession()

	// Every writer goes through this one mutex. The heartbeat loop and the WebRTC
	// session both write to this socket, and coder/websocket forbids concurrent writes.
	var writeMu sync.Mutex
	send := func(frame []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, cancel := context.WithTimeout(sessCtx, writeTimeout)
		defer cancel()
		return c.Write(writeCtx, websocket.MessageText, frame)
	}

	if err := send(hello); err != nil {
		return nil, err
	}

	interval := time.Duration(cfg.HeartbeatIntervalSeconds) * time.Second
	if fatalErr, transientErr := awaitWelcome(ctx, c, &interval); fatalErr != nil || transientErr != nil {
		return fatalErr, transientErr
	}
	log.Printf("agent: connected to %s as device %s, beating every %s", cfg.WSURL, cfg.DeviceID, interval)

	capture := screen.NewManager(send)
	// Guarantees no capture goroutine, and no peer connection, outlives the socket that
	// authorised it.
	defer capture.StopAll()

	// The command runner shares the same serialised sender; each command it runs sends one
	// result frame back up this socket.
	runner := terminal.NewRunner(send)

	if onStatus != nil {
		onStatus(true, false)
		defer onStatus(false, false)
	}

	fatalCh := make(chan error, 1)
	go reader(sessCtx, endSession, c, fatalCh, capture, runner, onStatus)

	if err := beat(send); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sessCtx.Done():
			return nil, ctx.Err()
		case f := <-fatalCh:
			return f, nil
		case <-ticker.C:
			if err := beat(send); err != nil {
				return nil, err
			}
		}
	}
}

// awaitWelcome reads the first server frame. A fatal error here stops the agent for
// good; anything else is worth retrying.
func awaitWelcome(ctx context.Context, c *websocket.Conn, interval *time.Duration) (fatal, transient error) {
	readCtx, cancel := context.WithTimeout(ctx, helloAckTimeout)
	defer cancel()

	_, data, err := c.Read(readCtx)
	if err != nil {
		return nil, err
	}
	env, err := protocol.DecodeEnvelope(data)
	if err != nil {
		return nil, err
	}

	switch env.Type {
	case protocol.TypeWelcome:
		var wel protocol.Welcome
		if err := protocol.DecodeData(env.Data, &wel); err == nil && wel.HeartbeatIntervalSeconds > 0 {
			// The server owns this number, so its read deadline and our beat rate can
			// never disagree.
			*interval = time.Duration(wel.HeartbeatIntervalSeconds) * time.Second
		}
		if *interval <= 0 {
			*interval = 10 * time.Second
		}
		return nil, nil

	case protocol.TypeError:
		var em protocol.ErrorMsg
		_ = protocol.DecodeData(env.Data, &em)
		msg := em.Message
		if msg == "" {
			msg = "server rejected the connection"
		}
		if em.Fatal {
			return errors.New(msg), nil
		}
		return nil, errors.New(msg)

	default:
		return nil, errors.New("unexpected first frame from server")
	}
}

// reader dispatches inbound commands until the connection closes.
func reader(ctx context.Context, endSession context.CancelFunc, c *websocket.Conn,
	fatalCh chan<- error, capture *screen.Manager, runner *terminal.Runner, onStatus StatusFunc) {

	// A read error means the socket is gone; cancelling ends the session so the outer
	// loop can reconnect.
	defer endSession()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			continue
		}

		switch env.Type {
		case protocol.TypeStartSession:
			var cmd protocol.StartSession
			if err := protocol.DecodeData(env.Data, &cmd); err == nil && cmd.SessionID != "" {
				log.Printf("agent: session requested by %s", cmd.Operator)
				capture.Start(ctx, cmd)
				if onStatus != nil {
					onStatus(true, true)
				}
			}

		case protocol.TypeStopSession:
			var cmd protocol.StopSession
			if err := protocol.DecodeData(env.Data, &cmd); err == nil {
				capture.Stop(cmd.SessionID)
				if onStatus != nil {
					onStatus(true, false)
				}
			}

		case protocol.TypeAnswer:
			var a protocol.SDP
			if err := protocol.DecodeData(env.Data, &a); err == nil {
				capture.HandleAnswer(a)
			}

		case protocol.TypeICECandidate:
			var cand protocol.ICECandidate
			if err := protocol.DecodeData(env.Data, &cand); err == nil {
				capture.HandleICECandidate(cand)
			}

		case protocol.TypeTerminalCommand:
			var cmd protocol.TerminalCommand
			if err := protocol.DecodeData(env.Data, &cmd); err == nil && cmd.Command != "" {
				// Runs on its own goroutine so a slow command never stalls this reader,
				// which still has to handle heartbeats, session signaling and stops.
				runner.Execute(ctx, cmd)
			}

		case protocol.TypePing:
			// Liveness only; the heartbeat loop is what answers.

		case protocol.TypeError:
			var em protocol.ErrorMsg
			_ = protocol.DecodeData(env.Data, &em)
			if em.Fatal {
				msg := em.Message
				if msg == "" {
					msg = "server rejected the connection"
				}
				select {
				case fatalCh <- errors.New(msg):
				default:
				}
				return
			}
			log.Printf("agent: server error: %s", em.Message)
		}
	}
}

// beat samples metrics and sends one heartbeat through the shared writer.
func beat(send func([]byte) error) error {
	frame, err := protocol.Encode(protocol.TypeHeartbeat, protocol.Heartbeat{
		CPUPercent: sysinfo.CPUPercent(), // blocks ~500ms sampling
		RAMPercent: sysinfo.RAMPercent(),
		SysInfo:    sysinfo.Info(),
	})
	if err != nil {
		return err
	}
	return send(frame)
}
