package hud

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// Sink adapts a running dashboard to the event bus.
//
// This is the whole integration. Attach it and the HUD draws whatever the
// engine emits:
//
//	bus.Attach(hud.Sink(program))
//
// Deliver never returns an error and never blocks. The bus treats an error as
// a reason to count a drop and carry on, so returning one would only add a
// second counter for something the status line already shows; and blocking is
// the thing the whole package is built to avoid.
func Sink(p *Program) events.Sink {
	return &events.FuncSink{
		SinkName: "hud",
		Fn: func(_ context.Context, e events.Event) error {
			p.Send(e)
			return nil
		},
		CloseFn: func() error {
			p.Close()
			return nil
		},
	}
}

// PlainSink adapts the non-TTY fallback to the same bus, for a run with no
// terminal. Same contract, and the writes are serialised by Plain itself.
func PlainSink(p *Plain) events.Sink {
	return &events.FuncSink{
		SinkName: "hud-plain",
		Fn: func(_ context.Context, e events.Event) error {
			p.Write(e)
			return nil
		},
		CloseFn: func() error {
			p.Summary()
			return p.Err()
		},
	}
}
