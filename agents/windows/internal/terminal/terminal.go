// Package terminal runs operator-requested commands on the device and returns their
// output. This is the Phase 1 "command runner": each command is a one-shot process whose
// stdout, stderr and exit code are captured and returned as a single frame. There is no
// interactive shell yet — a later phase adds a PTY on top of the same wiring.
//
// Commands run at the agent's own privilege. The agent runs inside the logged-in user's
// session, non-elevated (see cmd/agent/platform_windows.go), so a command here cannot make
// machine-wide changes that require Administrator. Elevating that is a deliberate later
// step, not an accident of this code.
package terminal

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os/exec"
	"syscall"
	"time"

	"drs/agent/windows/internal/protocol"
)

// commandTimeout bounds a single command. Long enough for an ordinary diagnostic or a
// quick step, short enough that a hung process frees the runner and tells the operator
// rather than blocking silently forever.
const commandTimeout = 60 * time.Second

// maxOutputBytes caps stdout and stderr each. A runaway command (a recursive listing of
// C:\, a log tail) could otherwise return megabytes, and this output rides back over the
// same session socket whose inbound side the backend read-limits. Truncating here keeps a
// single command from overwhelming the channel.
const maxOutputBytes = 256 * 1024

// createNoWindow is CREATE_NO_WINDOW. The agent is a windowsgui process with no console,
// so a child console app (powershell.exe, cmd.exe) would otherwise flash up a console
// window on the monitored user's screen. Suppressing it keeps the command invisible,
// which is the whole point of running it remotely.
const createNoWindow = 0x08000000

// FrameSender writes one already-encoded envelope up the agent socket. It serialises with
// every other writer, exactly like the screen manager's sender — conn guards them all
// with one mutex.
type FrameSender func(frame []byte) error

// Runner executes terminal commands and sends their results back.
type Runner struct {
	send FrameSender
}

// NewRunner builds a Runner that emits results through send.
func NewRunner(send FrameSender) *Runner {
	return &Runner{send: send}
}

// Execute runs one command asynchronously and sends a TerminalResult when it finishes. It
// returns immediately so the socket reader is never blocked by a slow command; each
// command gets its own goroutine, and the shared, mutex-guarded sender serialises the
// results.
func (r *Runner) Execute(ctx context.Context, cmd protocol.TerminalCommand) {
	go r.run(ctx, cmd)
}

func (r *Runner) run(ctx context.Context, cmd protocol.TerminalCommand) {
	result := protocol.TerminalResult{
		SessionID: cmd.SessionID,
		CommandID: cmd.CommandID,
	}

	if cmd.Command == "" {
		result.Error = "empty command"
		result.ExitCode = -1
		r.reply(result)
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	c := buildCommand(runCtx, cmd)

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()

	result.Stdout = clamp(stdout.String())
	result.Stderr = clamp(stderr.String())

	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		result.Error = "command timed out after 60s"
		result.ExitCode = -1
	case err == nil:
		result.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The command ran and exited non-zero. That is a normal outcome, not a runner
			// failure, so it is reported via ExitCode with Error left empty.
			result.ExitCode = exitErr.ExitCode()
		} else {
			// The command could not be started at all (e.g. the shell was not found).
			result.Error = err.Error()
			result.ExitCode = -1
		}
	}

	r.reply(result)
}

func (r *Runner) reply(result protocol.TerminalResult) {
	frame, err := protocol.Encode(protocol.TypeTerminalResult, result)
	if err != nil {
		log.Printf("agent: could not encode terminal result: %v", err)
		return
	}
	if err := r.send(frame); err != nil {
		log.Printf("agent: could not send terminal result: %v", err)
	}
}

// buildCommand constructs the OS command for a terminal request. PowerShell is the default
// because it is what an operator expects on a modern Windows box; cmd is offered for the
// cases where a classic batch one-liner is easier. -NoProfile keeps a user's profile
// script from changing behaviour or slowing startup; -NonInteractive makes a command that
// would otherwise prompt fail fast instead of hanging until the timeout.
func buildCommand(ctx context.Context, cmd protocol.TerminalCommand) *exec.Cmd {
	var c *exec.Cmd
	if cmd.Shell == "cmd" {
		c = exec.CommandContext(ctx, "cmd.exe", "/c", cmd.Command)
	} else {
		c = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmd.Command)
	}
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}

// clamp truncates output to maxOutputBytes, appending a marker so the operator knows the
// result was cut rather than that the command simply stopped there.
func clamp(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + "\n...[output truncated]"
}
