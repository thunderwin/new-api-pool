package capability

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestEmbeddedAISAManifests(t *testing.T) {
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	ids := []string{
		"site-traffic-snapshot-v1",
		"site-traffic-trend-v1",
		"traffic-channels-v1",
		"site-referrals-v1",
		"similar-sites-v1",
		"backlinks-summary-v1",
		"referring-domains-v1",
		"backlink-anchors-v1",
		"backlink-competitors-v1",
		"domain-rank-overview-v1",
		"organic-competitors-v1",
		"ranked-keywords-v1",
		"keyword-gap-v1",
		"serp-google-v1",
		"ai-brand-mentions-v1",
		"page-seo-audit-v1",
	}
	seen := map[string]bool{}
	for _, id := range ids {
		m, ok := Get(id)
		if !ok {
			t.Fatalf("AISA manifest %s is missing", id)
		}
		if seen[id] {
			t.Fatalf("duplicate AISA manifest id %s", id)
		}
		seen[id] = true
		if err := m.Validate(); err != nil {
			t.Fatalf("%s should validate: %v", id, err)
		}
		if m.Provider != "aisa" || m.Mode != ModeSync || m.Billing.PriceUSD != 0.000001 {
			t.Fatalf("%s has unexpected provider, mode, or price: %#v", id, m)
		}
		if m.Billing.QuantityHeader != "X-AISA-Customer-Cost-Micros-USD" ||
			m.Billing.ReserveUSD <= 0 ||
			m.Request == nil ||
			m.Request.Headers["X-AISA-Max-Price-USD"] == "" {
			t.Fatalf("%s is missing AISA billing or cap fields", id)
		}
		if len(m.Retry.RetryOn) != 2 || m.Retry.Attempts != 2 ||
			!containsInt(m.Retry.RetryOn, http.StatusTooManyRequests) ||
			!containsInt(m.Retry.RetryOn, http.StatusServiceUnavailable) {
			t.Fatalf("%s has an unexpected retry policy: %#v", id, m.Retry)
		}
	}
	trend, _ := Get("site-traffic-trend-v1")
	if execErr := ValidateSync(trend, map[string]any{"domain": "example.com"}); execErr != nil {
		t.Fatalf("documented defaults should satisfy required inputs: %v", execErr)
	}
	aiMentions, _ := Get("ai-brand-mentions-v1")
	if execErr := ValidateSync(aiMentions, map[string]any{}); execErr == nil || execErr.Code != "invalid_request" {
		t.Fatalf("AI mentions must require target, got %#v", execErr)
	}
	if execErr := ValidateSync(aiMentions, map[string]any{"target": []any{}}); execErr == nil || execErr.Code != "invalid_request" {
		t.Fatalf("AI mentions must reject an empty target array, got %#v", execErr)
	}
}

func TestManifestFileIDsAreUnique(t *testing.T) {
	paths, err := fs.Glob(manifestFS, "manifests/*.json")
	if err != nil {
		t.Fatalf("list embedded manifests: %v", err)
	}
	ids := map[string]string{}
	for _, path := range paths {
		raw, err := fs.ReadFile(manifestFS, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var manifest Manifest
		if err := common.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if previous, exists := ids[manifest.ID]; exists {
			t.Fatalf("duplicate manifest id %s in %s and %s", manifest.ID, previous, path)
		}
		ids[manifest.ID] = path
	}
	if len(ids) != 19 {
		t.Fatalf("expected 19 embedded manifests, got %d", len(ids))
	}
}

func TestManifestRejectsUnsafeRequestExtensions(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{
			ID:       "extension-test-v1",
			Provider: "test",
			Mode:     ModeSync,
			Endpoint: "https://example.com",
			Method:   http.MethodPost,
			Request:  &RequestSpec{},
			Response: &ResponseSpec{Output: map[string]string{"ok": "$.ok"}},
			Billing: BillingSpec{
				Type:           BillingPerUnit,
				Unit:           "micro_usd",
				PriceUSD:       0.000001,
				QuantityHeader: "X-Cost",
			},
			Limits: LimitsSpec{Timeout: "5s", MaxResponseBytes: 1024},
		}
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "invalid body wrap", mutate: func(m *Manifest) { m.Request.BodyWrap = "object_list" }},
		{name: "invalid header name", mutate: func(m *Manifest) { m.Request.Headers = map[string]string{"bad header": "x"} }},
		{name: "authorization header", mutate: func(m *Manifest) { m.Request.Headers = map[string]string{"Authorization": "x"} }},
		{name: "host header", mutate: func(m *Manifest) { m.Request.Headers = map[string]string{"Host": "x"} }},
		{name: "content length header", mutate: func(m *Manifest) { m.Request.Headers = map[string]string{"Content-Length": "1"} }},
		{name: "header injection", mutate: func(m *Manifest) { m.Request.Headers = map[string]string{"X-Test": "ok\r\nX-Bad: 1"} }},
		{name: "quantity path and header", mutate: func(m *Manifest) { m.Billing.QuantityPath = "$.cost" }},
		{name: "missing quantity source", mutate: func(m *Manifest) { m.Billing.QuantityHeader = "" }},
		{name: "negative reserve", mutate: func(m *Manifest) { m.Billing.ReserveUSD = -1 }},
		{name: "invalid error map status", mutate: func(m *Manifest) {
			m.Response.ErrorMap = map[string]ErrorMapSpec{"402": {Code: "x", HTTPStatus: 200, Message: "x"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := base()
			test.mutate(m)
			if err := m.Validate(); err == nil {
				t.Fatalf("expected validation error for %#v", m)
			}
		})
	}
}

func TestDataForSEOArrayBodyStaticHeadersAndOutput(t *testing.T) {
	allowLocalTestUpstreams(t)
	t.Setenv("CAPABILITY_TEST_AISA_KEY", "test-aisa-key")
	var received []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fixed") != "" {
			t.Errorf("POST constants leaked into query: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("declared") != "query-value" {
			t.Errorf("declared POST query parameter is missing: %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-AISA-Max-Price-USD") != "0.10" {
			t.Errorf("static cap header is missing")
		}
		if r.Header.Get("Idempotency-Key") != "idem-test" {
			t.Errorf("idempotency header is missing")
		}
		if r.Header.Get("Authorization") != "Bearer test-aisa-key" {
			t.Errorf("bearer auth is missing")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := common.Unmarshal(raw, &received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cost", "2500")
		_, _ = w.Write([]byte(`{"status_code":20000,"tasks":[{"status_code":20000,"result":[{"rank":1}]}]}`))
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "array-body-test-v1",
		Provider: "aisa",
		Mode:     ModeSync,
		Endpoint: server.URL,
		Method:   http.MethodPost,
		Auth:     &AuthSpec{Type: "bearer", SecretRef: "CAPABILITY_TEST_AISA_KEY"},
		Request: &RequestSpec{
			Query:    map[string]string{"declared": "$.input.declared"},
			Body:     map[string]string{"target": "$.input.target"},
			BodyWrap: "array",
			Consts:   map[string]string{"fixed": "only-in-body"},
			Headers:  map[string]string{"X-AISA-Max-Price-USD": "0.10"},
		},
		Response: &ResponseSpec{
			Success: &SuccessSpec{Path: "$.tasks.0.status_code", Values: []any{20000}},
			Output:  map[string]string{"result": "$.tasks.0.result"},
		},
		Billing: BillingSpec{
			Type:           BillingPerUnit,
			Unit:           "micro_usd",
			PriceUSD:       0.000001,
			QuantityHeader: "X-Cost",
			ReserveUSD:     0.10,
		},
		Limits:      LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
		Idempotency: &IdempotencySpec{Header: "Idempotency-Key"},
	}
	result, execErr := ExecuteSync(context.Background(), m, map[string]any{
		"target":   "example.com",
		"declared": "query-value",
	}, "idem-test")
	if execErr != nil {
		t.Fatalf("ExecuteSync failed: %v", execErr)
	}
	if len(received) != 1 {
		t.Fatalf("expected a one-element array body, got %#v", received)
	}
	body, ok := received[0].(map[string]any)
	if !ok || body["target"] != "example.com" || body["fixed"] != "only-in-body" {
		t.Fatalf("unexpected array body: %#v", received)
	}
	if result.Headers.Get("X-Cost") != "2500" {
		t.Fatalf("response headers were not preserved: %#v", result.Headers)
	}
	if _, ok := result.Output["result"].([]any); !ok {
		t.Fatalf("unexpected DataForSEO output: %#v", result.Output)
	}
}

func TestSimilarwebFixtureExtractsMetaAndData(t *testing.T) {
	allowLocalTestUpstreams(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Query().Get("domain") != "cnn.com" || r.URL.Query().Get("country") != "ww" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"status":"success","request":{"domain":"cnn.com"}},"data":[{"domain":"bbc.com"}]}`))
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "similarweb-fixture-v1",
		Provider: "aisa",
		Mode:     ModeSync,
		Endpoint: server.URL,
		Method:   http.MethodGet,
		Request: &RequestSpec{
			Required: []string{"domain"},
			Query:    map[string]string{"domain": "$.input.domain", "country": "$.input.country"},
			Defaults: map[string]any{"country": "ww"},
		},
		Response: &ResponseSpec{Output: map[string]string{"meta": "$.meta", "data": "$.data"}},
		Billing: BillingSpec{
			Type:           BillingPerUnit,
			Unit:           "micro_usd",
			PriceUSD:       0.000001,
			QuantityHeader: "X-AISA-Customer-Cost-Micros-USD",
			ReserveUSD:     0.60,
		},
		Limits: LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	result, execErr := ExecuteSync(context.Background(), m, map[string]any{"domain": "cnn.com"}, "")
	if execErr != nil {
		t.Fatalf("ExecuteSync failed: %v", execErr)
	}
	if _, ok := result.Output["meta"].(map[string]any); !ok {
		t.Fatalf("meta was not extracted: %#v", result.Output)
	}
	if data, ok := result.Output["data"].([]any); !ok || len(data) != 1 {
		t.Fatalf("data was not extracted: %#v", result.Output)
	}
}

func TestDataForSEOInternalFailureIsNotRetriedOrLeaked(t *testing.T) {
	allowLocalTestUpstreams(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tasks":[{"status_code":40501,"status_message":"secret provider detail"}]}`))
	}))
	defer server.Close()

	m := &Manifest{
		ID:       "internal-status-test-v1",
		Provider: "aisa",
		Mode:     ModeSync,
		Endpoint: server.URL,
		Method:   http.MethodPost,
		Request:  &RequestSpec{Body: map[string]string{"target": "$.input.target"}, BodyWrap: "array"},
		Response: &ResponseSpec{
			Success: &SuccessSpec{
				Path:        "$.tasks.0.status_code",
				Values:      []any{20000},
				FailureCode: "upstream_task_failed",
			},
			Output: map[string]string{"result": "$.tasks.0.result"},
		},
		Billing: BillingSpec{Type: BillingPerCall, PriceUSD: 0},
		Limits:  LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
		Retry:   RetrySpec{Attempts: 2, RetryOn: []int{429, 503}},
	}
	_, execErr := ExecuteSync(context.Background(), m, map[string]any{"target": "example.com"}, "")
	if execErr == nil || execErr.Code != "upstream_task_failed" {
		t.Fatalf("expected internal task failure, got %#v", execErr)
	}
	if calls != 1 {
		t.Fatalf("internal status failure must not be retried, got %d calls", calls)
	}
	if strings.Contains(execErr.Message, "secret") {
		t.Fatalf("upstream message leaked: %v", execErr)
	}
}

func TestUpstreamErrorMappingsDoNotLeakMessages(t *testing.T) {
	allowLocalTestUpstreams(t)
	mapSpec := map[string]ErrorMapSpec{
		"401":                               {Code: "upstream_unavailable", HTTPStatus: 503, Message: "The upstream data service is temporarily unavailable.", LogCode: "upstream_authentication"},
		"authentication_unavailable":        {Code: "upstream_unavailable", HTTPStatus: 503, Message: "The upstream data service is temporarily unavailable.", LogCode: "upstream_authentication_unavailable"},
		"insufficient_wallet_balance":       {Code: "upstream_unavailable", HTTPStatus: 503, Message: "The upstream data service is temporarily unavailable.", LogCode: "upstream_wallet_balance"},
		"estimated_price_exceeds_max_price": {Code: "cost_cap_exceeded", HTTPStatus: 422, Message: "The estimated cost exceeds the request cap.", LogCode: "upstream_cost_cap_exceeded"},
		"402":                               {Code: "upstream_unavailable", HTTPStatus: 503, Message: "The upstream data service is temporarily unavailable.", LogCode: "upstream_balance_unavailable"},
	}
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantLog    string
	}{
		{name: "wallet balance", status: 402, body: `{"error":{"code":"insufficient_wallet_balance","message":"secret wallet detail"}}`, wantStatus: 503, wantCode: "upstream_unavailable", wantLog: "upstream_wallet_balance"},
		{name: "cost cap", status: 402, body: `{"error":{"code":"estimated_price_exceeds_max_price","message":"secret cap detail"}}`, wantStatus: 422, wantCode: "cost_cap_exceeded", wantLog: "upstream_cost_cap_exceeded"},
		{name: "auth 401", status: 401, body: `{"error":"secret auth detail"}`, wantStatus: 503, wantCode: "upstream_unavailable", wantLog: "upstream_authentication"},
		{name: "auth dependency", status: 503, body: `{"error":{"code":"authentication_unavailable","message":"secret dependency detail"}}`, wantStatus: 503, wantCode: "upstream_unavailable", wantLog: "upstream_authentication_unavailable"},
		{name: "balance status fallback", status: 402, body: `plain secret balance body`, wantStatus: 503, wantCode: "upstream_unavailable", wantLog: "upstream_balance_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			m := &Manifest{
				ID:       "error-map-test-v1",
				Provider: "aisa",
				Mode:     ModeSync,
				Endpoint: server.URL,
				Method:   http.MethodGet,
				Response: &ResponseSpec{
					Output:        map[string]string{"ok": "$.ok"},
					ErrorCodePath: "$.error.code",
					ErrorMap:      mapSpec,
				},
				Billing: BillingSpec{Type: BillingPerCall, PriceUSD: 0},
				Limits:  LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
				Retry:   RetrySpec{Attempts: 1, RetryOn: []int{429, 503}},
			}
			_, execErr := ExecuteSync(context.Background(), m, map[string]any{}, "")
			if execErr == nil || execErr.HTTPStatus != test.wantStatus ||
				execErr.Code != test.wantCode || execErr.LogCode != test.wantLog {
				t.Fatalf("unexpected mapped error: %#v", execErr)
			}
			if strings.Contains(execErr.Message, "secret") {
				t.Fatalf("upstream body leaked: %v", execErr)
			}
		})
	}
}

func TestSyncMissingOutputAndTimeout(t *testing.T) {
	allowLocalTestUpstreams(t)
	t.Run("missing output field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"other":"secret body"}`))
		}))
		defer server.Close()
		m := &Manifest{
			ID:       "missing-output-test-v1",
			Provider: "test",
			Mode:     ModeSync,
			Endpoint: server.URL,
			Method:   http.MethodGet,
			Response: &ResponseSpec{Output: map[string]string{"value": "$.value"}},
			Billing:  BillingSpec{Type: BillingPerCall, PriceUSD: 0},
			Limits:   LimitsSpec{Timeout: "1s", MaxResponseBytes: 4096},
		}
		_, execErr := ExecuteSync(context.Background(), m, map[string]any{}, "")
		if execErr == nil || execErr.Code != "missing_output_field" {
			t.Fatalf("expected missing_output_field, got %#v", execErr)
		}
		if strings.Contains(execErr.Message, "secret") {
			t.Fatalf("upstream body leaked: %v", execErr)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer server.Close()
		m := &Manifest{
			ID:       "timeout-test-v1",
			Provider: "test",
			Mode:     ModeSync,
			Endpoint: server.URL,
			Method:   http.MethodGet,
			Response: &ResponseSpec{Output: map[string]string{"ok": "$.ok"}},
			Billing:  BillingSpec{Type: BillingPerCall, PriceUSD: 0},
			Limits:   LimitsSpec{Timeout: "20ms", MaxResponseBytes: 4096},
			Retry:    RetrySpec{Attempts: 1, RetryOn: []int{429, 503}},
		}
		_, execErr := ExecuteSync(context.Background(), m, map[string]any{}, "")
		if execErr == nil || execErr.Code != "upstream_unreachable" {
			t.Fatalf("expected timeout to surface as upstream_unreachable, got %#v", execErr)
		}
	})
}
