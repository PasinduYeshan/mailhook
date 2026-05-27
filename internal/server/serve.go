package server

import "context"

// Config holds runtime addresses and buffer capacity for Run.
type Config struct {
	SMTPAddr string
	HookAddr string
	APIAddr  string
	Cap      int
}

// Run starts the SMTP receiver, webhook receiver, and Query API in parallel
// and blocks until ctx is cancelled or one of them errors out.
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

	st := newStore(cfg.Cap)
	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 3)
	go func() { errCh <- runSMTP(innerCtx, cfg.SMTPAddr, st) }()
	go func() { errCh <- runHook(innerCtx, cfg.HookAddr, st) }()
	go func() { errCh <- runAPI(innerCtx, cfg.APIAddr, st) }()

	var firstErr error
	for i := 0; i < 3; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}
