package server

import (
	"context"
	"fmt"
	"net"
)

// Config holds runtime addresses and buffer capacity for Run.
type Config struct {
	SMTPAddr string
	HookAddr string
	APIAddr  string
	Cap      int

	// OnReady, if set, runs once all listeners are bound, with the actual
	// addresses (useful when a port of 0 was requested).
	OnReady func(Config)
}

// Run binds the SMTP receiver, webhook receiver, and Query API, then serves
// them in parallel until ctx is cancelled or one of them errors out.
func Run(ctx context.Context, cfg Config) error {
	if cfg.SMTPAddr == "" {
		cfg.SMTPAddr = DefaultSMTPAddr
	}
	if cfg.HookAddr == "" {
		cfg.HookAddr = DefaultHookAddr
	}
	if cfg.APIAddr == "" {
		cfg.APIAddr = DefaultAPIAddr
	}
	if cfg.Cap <= 0 {
		cfg.Cap = DefaultCap
	}

	// Bind everything first so a busy port fails fast with a clear message.
	var lns []net.Listener
	closeAll := func() {
		for _, ln := range lns {
			ln.Close()
		}
	}
	for _, b := range []struct {
		name, flag string
		addr       *string
	}{
		{"smtp", "smtp", &cfg.SMTPAddr},
		{"hook", "hook", &cfg.HookAddr},
		{"api", "api", &cfg.APIAddr},
	} {
		*b.addr = NormalizeAddr(*b.addr)
		ln, err := net.Listen("tcp", *b.addr)
		if err != nil {
			closeAll()
			return fmt.Errorf("%s: %w (pick another with --%s <port>)", b.name, err, b.flag)
		}
		*b.addr = ln.Addr().String()
		lns = append(lns, ln)
	}

	st := newStore(cfg.Cap)
	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if cfg.OnReady != nil {
		cfg.OnReady(cfg)
	}

	errCh := make(chan error, 3)
	go func() { errCh <- runSMTP(innerCtx, lns[0], st) }()
	go func() { errCh <- runHook(innerCtx, lns[1], st) }()
	go func() { errCh <- runAPI(innerCtx, lns[2], cfg, st) }()

	var firstErr error
	for i := 0; i < 3; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}
