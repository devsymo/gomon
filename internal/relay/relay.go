// Package relay accepts plaintext Xymon messages (e.g. on 127.0.0.1:1984)
// and forwards them over TLS to a gomon/xymond server. It lets unmodified
// legacy clients, ext scripts and tools (BBWin, devmon, old xymon builds)
// communicate encrypted without any change: point their XYMSRV at the relay.
package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"time"

	"gomon/internal/xclient"
)

type Relay struct {
	Listen    string
	Upstreams []xclient.Recipient
	Opts      xclient.Options
	Log       *slog.Logger
	MaxSize   int
}

func (r *Relay) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", r.Listen)
	if err != nil {
		return err
	}
	if r.MaxSize <= 0 {
		r.MaxSize = 10 << 20
	}
	go func() { <-ctx.Done(); ln.Close() }()
	r.Log.Info("relay listening", "addr", r.Listen, "upstreams", len(r.Upstreams))
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go r.handle(ctx, c)
	}
}

func (r *Relay) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	msg, err := io.ReadAll(io.LimitReader(c, int64(r.MaxSize)))
	if err != nil && !errors.Is(err, io.EOF) && len(msg) == 0 {
		return
	}
	if len(msg) == 0 {
		return
	}
	var resp string
	for i, up := range r.Upstreams {
		out, err := xclient.Send(ctx, up, string(msg), r.Opts)
		if err != nil {
			r.Log.Warn("relay send failed", "upstream", up.Host, "err", err)
			continue
		}
		if i == 0 {
			resp = out
		}
	}
	if resp != "" {
		_, _ = io.WriteString(c, resp)
	}
}
