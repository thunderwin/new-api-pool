package capability

import (
	"net/http"
	"sync"
	"time"
)

// HTTP adapters validate input and project responses only. Networking, auth,
// upstream status checks and billing stay in the shared HTTP path.
type httpSyncAdapter struct {
	validate func(map[string]any) *ExecutionError
	project  func(map[string]any, string) (map[string]any, bool, *ExecutionError)
	guard    *httpAdapterGuard
}

var httpSyncAdapters = map[string]httpSyncAdapter{
	"tikhub-douyin-search": {validateDouyinInput, projectDouyinSearch, &httpAdapterGuard{slots: make(chan struct{}, 8)}},
}

// A per-process bulkhead and short circuit breaker protect the paid HTTP route.
// Use a single probe after cooldown to avoid a burst against an unhealthy supplier.
type httpAdapterGuard struct {
	slots    chan struct{}
	mu       sync.Mutex
	failures int
	openTill time.Time
}

func (g *httpAdapterGuard) enter() (func(*ExecutionError), *ExecutionError) {
	unavailable := func(code string) (func(*ExecutionError), *ExecutionError) {
		return nil, &ExecutionError{Code: "upstream_unavailable", LogCode: code, Message: "upstream service is temporarily unavailable", HTTPStatus: http.StatusServiceUnavailable}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	probe := !g.openTill.IsZero()
	if probe && (time.Now().Before(g.openTill) || len(g.slots) != 0) {
		return unavailable("upstream_circuit_open")
	}
	select {
	case g.slots <- struct{}{}:
	default:
		return unavailable("upstream_concurrency_limit")
	}
	return func(err *ExecutionError) {
		g.mu.Lock()
		defer g.mu.Unlock()
		<-g.slots
		if !probe && !g.openTill.IsZero() {
			return // Ignore completions admitted before the circuit opened.
		}
		failed := err != nil && err.HTTPStatus >= 500 && err.Code != "credential_error"
		if failed {
			g.failures++
			if probe || g.failures >= 8 {
				g.openTill = time.Now().Add(30 * time.Second)
			}
		} else {
			g.failures = 0
			g.openTill = time.Time{}
		}
	}, nil
}
