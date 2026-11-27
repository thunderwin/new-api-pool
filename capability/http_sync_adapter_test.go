package capability

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPAdapterGuard(t *testing.T) {
	g := httpAdapterGuard{slots: make(chan struct{}, 1)}
	admit := func() func(*ExecutionError) {
		t.Helper()
		finish, err := g.enter()
		if err != nil {
			t.Fatal(err)
		}
		return finish
	}
	blocked := func(logCode string) {
		t.Helper()
		if _, err := g.enter(); err == nil || err.LogCode != logCode {
			t.Fatalf("expected %s, got %v", logCode, err)
		}
	}
	finish := admit()
	blocked("upstream_concurrency_limit")
	finish(nil)
	failure := &ExecutionError{HTTPStatus: http.StatusBadGateway}
	for range 8 {
		admit()(failure)
	}
	blocked("upstream_circuit_open")
	g.openTill = time.Now().Add(-time.Second)
	finish = admit()
	blocked("upstream_circuit_open")
	finish(nil)
	admit()(nil)
}
