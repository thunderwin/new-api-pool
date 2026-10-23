package capability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func allowLocalTestUpstreams(t *testing.T) {
	t.Helper()
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
}

func TestEmbeddedPilotManifests(t *testing.T) {
	weather, ok := Get("weather-v1")
	if !ok || weather.Mode != ModeSync || weather.Provider != "open-meteo" {
		t.Fatal("weather-v1 manifest is missing or invalid")
	}
	ssl, ok := Get("ssl-analyze-v1")
	if !ok || ssl.Mode != ModeAsync || ssl.Provider != "ssllabs" {
		t.Fatal("ssl-analyze-v1 manifest is missing or invalid")
	}
	web, ok := Get("web-unlock-v1")
	if !ok || web.Mode != ModeSync || web.Provider != "brightdata" {
		t.Fatal("web-unlock-v1 manifest is missing or invalid")
	}
	if err := weather.Validate(); err != nil {
		t.Fatalf("weather-v1 should validate: %v", err)
	}
	if err := ssl.Validate(); err != nil {
		t.Fatalf("ssl-analyze-v1 should validate: %v", err)
	}
	if err := web.Validate(); err != nil {
		t.Fatalf("web-unlock-v1 should validate: %v", err)
	}
	if web.Billing.PriceUSD != 0.003 || web.Limits.MaxResponseBytes != 8*1024*1024 ||
		web.Retry.Attempts != 2 || web.Limits.Timeout != "90s" {
		t.Fatalf("unexpected web-unlock-v1 billing or limits: %#v", web)
	}
}

func TestLookupAndScalarHelpers(t *testing.T) {
	root := map[string]any{
		"input": map[string]any{"lat": 52.52, "enabled": true},
		"items": []any{map[string]any{"name": "first"}},
	}
	if v, ok := Lookup(root, "$.input.lat"); !ok {
		t.Fatal("expected numeric lookup to succeed")
	} else if n, ok := NumberValue(v); !ok || n != 52.52 {
		t.Fatal("expected numeric value")
	}
	if v, ok := Lookup(root, "$.items.0.name"); !ok {
		t.Fatal("expected array lookup to succeed")
	} else if s, ok := StringValue(v); !ok || s != "first" {
		t.Fatal("expected array string value")
	}
	if _, ok := Lookup(root, "$.missing.path"); ok {
		t.Fatal("expected missing path to fail")
	}
}

func TestExecuteSyncMapsRequestAndResponse(t *testing.T) {
	allowLocalTestUpstreams(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("lat") != "52.52" || r.URL.Query().Get("lon") != "13.41" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("units") != "metric" {
			t.Errorf("expected default units=metric")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"current":{"temp":21.5,"time":"2026-10-22T12:00"}}`))
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "test-sync-v1",
		Provider: "test",
		Mode:     ModeSync,
		Endpoint: server.URL,
		Method:   http.MethodGet,
		Request: &RequestSpec{
			Query:    map[string]string{"lat": "$.input.lat", "lon": "$.input.lon", "units": "$.input.units"},
			Defaults: map[string]any{"units": "metric"},
		},
		Response: &ResponseSpec{Output: map[string]string{"temperature": "$.current.temp", "time": "$.current.time"}},
		Billing:  BillingSpec{Type: BillingPerCall, PriceUSD: 0},
		Limits:   LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	if err := Register(m); err != nil {
		t.Fatal(err)
	}
	defer Unregister(m.ID)

	result, execErr := ExecuteSync(context.Background(), m, map[string]any{"lat": 52.52, "lon": 13.41}, "")
	if execErr != nil {
		t.Fatalf("ExecuteSync failed: %v", execErr)
	}
	if result.Output["temperature"] != 21.5 || result.Output["time"] != "2026-10-22T12:00" {
		t.Fatalf("unexpected output: %#v", result.Output)
	}
}

func TestAsyncSubmitAndPoll(t *testing.T) {
	allowLocalTestUpstreams(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/submit":
			_, _ = w.Write([]byte(`{"job":"job-123","status":"QUEUED"}`))
		case "/poll":
			if r.URL.Query().Get("id") != "job-123" {
				t.Errorf("poll did not receive job id")
			}
			_, _ = w.Write([]byte(`{"status":"READY","result":{"grade":"A"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "test-async-v1",
		Provider: "test",
		Mode:     ModeAsync,
		Submit: &SubmitSpec{
			Endpoint:  server.URL + "/submit",
			Method:    http.MethodGet,
			JobIDPath: "$.job",
		},
		Poll: &PollSpec{
			Endpoint:      server.URL + "/poll",
			Method:        http.MethodGet,
			Query:         map[string]string{"id": "$.input.id"},
			StatusPath:    "$.status",
			SuccessValues: []string{"READY"},
			FailureValues: []string{"ERROR"},
			ResultPath:    "$.result",
		},
		Billing: BillingSpec{Type: BillingPerCall, PriceUSD: 0},
		Limits:  LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	if err := Register(m); err != nil {
		t.Fatal(err)
	}
	defer Unregister(m.ID)

	submitted, execErr := SubmitAsync(context.Background(), m, map[string]any{}, "")
	if execErr != nil {
		t.Fatalf("SubmitAsync failed: %v", execErr)
	}
	if submitted.JobID != "job-123" {
		t.Fatalf("unexpected job id: %s", submitted.JobID)
	}
	polled, execErr := PollAsync(context.Background(), m, map[string]any{"id": "job-123"}, submitted.JobID)
	if execErr != nil {
		t.Fatalf("PollAsync failed: %v", execErr)
	}
	if !polled.Completed || polled.Status != "READY" {
		t.Fatalf("unexpected poll state: %#v", polled)
	}
	result, ok := polled.Result.(map[string]any)
	if !ok || result["grade"] != "A" {
		t.Fatalf("unexpected poll result: %#v", polled.Result)
	}
}

func TestPrivateUpstreamIsBlockedByDefault(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "test-ssrf-v1",
		Provider: "test",
		Mode:     ModeSync,
		Endpoint: server.URL,
		Method:   http.MethodGet,
		Response: &ResponseSpec{Output: map[string]string{"ok": "$.ok"}},
		Billing:  BillingSpec{Type: BillingPerCall, PriceUSD: 0},
		Limits:   LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	_, execErr := ExecuteSync(context.Background(), m, map[string]any{}, "")
	if execErr == nil || execErr.Code != "upstream_not_allowed" {
		t.Fatalf("expected private upstream to be blocked, got %#v", execErr)
	}
}

func TestResponseSizeLimit(t *testing.T) {
	allowLocalTestUpstreams(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":"` + strings.Repeat("x", 200) + `"}`))
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "test-large-v1",
		Provider: "test",
		Mode:     ModeSync,
		Endpoint: server.URL,
		Method:   http.MethodGet,
		Response: &ResponseSpec{Output: map[string]string{"data": "$.data"}},
		Billing:  BillingSpec{Type: BillingPerCall, PriceUSD: 0},
		Limits:   LimitsSpec{Timeout: "5s", MaxResponseBytes: 64},
	}
	_, execErr := ExecuteSync(context.Background(), m, map[string]any{}, "")
	if execErr == nil || execErr.Code != "upstream_response_too_large" {
		t.Fatalf("expected response limit error, got %#v", execErr)
	}
}

func testWebUnlockManifest(t *testing.T, endpoint string) *Manifest {
	t.Helper()
	allowLocalTestUpstreams(t)
	t.Setenv("CAPABILITY_BRIGHTDATA_API_KEY", "test-brightdata-key")
	t.Setenv("CAPABILITY_BRIGHTDATA_ZONE", "wayfair_unlocker")
	embedded, ok := Get("web-unlock-v1")
	if !ok {
		t.Fatal("web-unlock-v1 manifest is missing")
	}
	manifest := *embedded
	manifest.Endpoint = endpoint
	return &manifest
}

func TestWebUnlockSuccessAndConstantBodyFields(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if err := common.Unmarshal(raw, &receivedBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if r.Header.Get("Authorization") != "Bearer test-brightdata-key" {
			t.Errorf("unexpected authorization header")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Wayfair-Custom") != "" {
			t.Errorf("custom target headers or cookies were sent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"headers":{"content-type":"text/html"},"body":"<html>ok</html>"}`))
	}))
	defer server.Close()

	manifest := testWebUnlockManifest(t, server.URL)
	result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"url":    "https://www.wayfair.com/keyword.php?keyword=test",
		"zone":   "user-zone",
		"format": "raw",
	}, "")
	if execErr != nil {
		t.Fatalf("web unlock failed: %v", execErr)
	}
	if result.Output["status"] != float64(200) || result.Output["html"] != "<html>ok</html>" {
		t.Fatalf("unexpected output: %#v", result.Output)
	}
	if receivedBody["zone"] != "wayfair_unlocker" || receivedBody["format"] != "json" {
		t.Fatalf("constants did not override user input: %#v", receivedBody)
	}
	if receivedBody["url"] != "https://www.wayfair.com/keyword.php?keyword=test" {
		t.Fatalf("unexpected target URL: %#v", receivedBody["url"])
	}
}

func TestWebUnlockBusinessFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		errorCode  string
	}{
		{name: "target 403", statusCode: http.StatusOK, response: `{"status_code":403,"headers":{},"body":"secret page"}`, errorCode: "upstream_target_status"},
		{name: "target 404", statusCode: http.StatusOK, response: `{"status_code":404,"headers":{},"body":"not found secret"}`, errorCode: "upstream_target_status"},
		{name: "empty body", statusCode: http.StatusOK, response: `{"status_code":200,"headers":{},"body":"   "}`, errorCode: "upstream_empty_body"},
		{name: "brightdata outer error", statusCode: http.StatusUnauthorized, response: `plain secret error`, errorCode: "upstream_http_401"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			manifest := testWebUnlockManifest(t, server.URL)
			_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
				"url": "https://www.wayfair.com/product.html",
			}, "")
			if execErr == nil || execErr.Code != test.errorCode {
				t.Fatalf("expected %s, got %#v", test.errorCode, execErr)
			}
			if strings.Contains(execErr.Message, "secret") || strings.Contains(execErr.Error(), "secret") {
				t.Fatalf("upstream body leaked into error: %v", execErr)
			}
		})
	}
}

func TestWebUnlockRetriesProvider502OnceThenSucceeds(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("x-brd-status-code", "502")
			w.Header().Set("x-brd-error", "temporary unlock failure")
			_, _ = w.Write([]byte(`{"error":"secret provider body"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":200,"headers":{},"body":"<html>retry-ok</html>"}`))
	}))
	defer server.Close()

	manifest := testWebUnlockManifest(t, server.URL)
	result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"url": "https://www.wayfair.com/product.html",
	}, "")
	if execErr != nil {
		t.Fatalf("expected retry to succeed: %v", execErr)
	}
	if calls != 2 {
		t.Fatalf("expected exactly two attempts, got %d", calls)
	}
	if result.Output["html"] != "<html>retry-ok</html>" {
		t.Fatalf("unexpected retry output: %#v", result.Output)
	}
}

func TestWebUnlockStopsAfterOneRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-brd-status-code", "502")
		w.Header().Set("x-brd-error", "unlock failed")
		_, _ = w.Write([]byte(`{"error":"secret provider body"}`))
	}))
	defer server.Close()

	manifest := testWebUnlockManifest(t, server.URL)
	_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"url": "https://www.wayfair.com/product.html",
	}, "")
	if execErr == nil || execErr.Code != "upstream_http_502" {
		t.Fatalf("expected upstream_http_502, got %#v", execErr)
	}
	if calls != 2 {
		t.Fatalf("expected exactly two attempts, got %d", calls)
	}
	if strings.Contains(execErr.Message, "secret") {
		t.Fatalf("upstream body leaked into error: %v", execErr)
	}
}

func TestWebUnlockURLValidation(t *testing.T) {
	web, ok := Get("web-unlock-v1")
	if !ok {
		t.Fatal("web-unlock-v1 manifest is missing")
	}
	t.Setenv("CAPABILITY_BRIGHTDATA_API_KEY", "test-brightdata-key")
	t.Setenv("CAPABILITY_BRIGHTDATA_ZONE", "wayfair_unlocker")

	invalid := []string{
		"https://example.com/product.html",
		"http://www.wayfair.com/product.html",
		"https://wayfair.com.evil.com/product.html",
		"https://user@wayfair.com/product.html",
		"https://127.0.0.1/product.html",
		"https://www.wayfair.com:8443/product.html",
		"",
	}
	for _, target := range invalid {
		execErr := ValidateSync(web, map[string]any{"url": target})
		if execErr == nil || execErr.Code != "invalid_request" {
			t.Errorf("expected %q to be rejected, got %#v", target, execErr)
		}
	}
	if execErr := ValidateSync(web, map[string]any{}); execErr == nil || execErr.Code != "invalid_request" {
		t.Fatalf("expected missing URL to be rejected, got %#v", execErr)
	}
	for _, target := range []string{
		"https://wayfair.com/product.html",
		"https://www.wayfair.ca/product.html",
		"https://shop.wayfair.co.uk/product.html",
		"https://wayfair.de/product.html",
		"https://www.wayfair.com:443/product.html",
	} {
		if execErr := ValidateSync(web, map[string]any{"url": target}); execErr != nil {
			t.Errorf("expected %q to be allowed, got %v", target, execErr)
		}
	}
}

func TestWeatherRequiresCoordinates(t *testing.T) {
	weather, ok := Get("weather-v1")
	if !ok {
		t.Fatal("weather-v1 manifest is missing")
	}
	if execErr := ValidateSync(weather, map[string]any{}); execErr == nil || execErr.Code != "invalid_request" {
		t.Fatalf("expected empty weather input to be invalid, got %#v", execErr)
	}
}

func TestWebUnlockResponseSizeLimitIsEightMiB(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status_code":200,"body":"`)
		_, _ = io.WriteString(w, strings.Repeat("x", 8*1024*1024+1))
		_, _ = io.WriteString(w, `"}`)
	}))
	defer server.Close()

	manifest := testWebUnlockManifest(t, server.URL)
	_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"url": "https://www.wayfair.com/product.html",
	}, "")
	if execErr == nil || execErr.Code != "upstream_response_too_large" {
		t.Fatalf("expected upstream_response_too_large, got %#v", execErr)
	}
}
