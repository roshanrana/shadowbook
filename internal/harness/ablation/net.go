package ablation

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// freePort asks the kernel for an unused port.
//
// Each run starts its own ledger, and runs are sequential but their processes
// overlap briefly during shutdown, so a fixed port would intermittently collide
// with the previous run's socket in TIME_WAIT.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("ablation: reserve port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitHealthy polls /readyz, not /healthz: liveness answers as soon as the
// server binds, while the runner needs migrations applied and the database
// reachable before it seeds accounts.
func waitHealthy(ctx context.Context, addr string, within time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://" + addr + "/readyz"
	deadline := time.Now().Add(within)

	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("ablation: ledger at %s not healthy within %s: %w", addr, within, lastErr)
}
