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

func cloneKeywordManifest(t *testing.T, id string, endpoint string) *Manifest {
	t.Helper()
	embedded, ok := Get(id)
	if !ok {
		t.Fatalf("%s manifest is missing", id)
	}
	raw, err := common.Marshal(embedded)
	if err != nil {
		t.Fatalf("clone %s: %v", id, err)
	}
	var manifest Manifest
	if err := common.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("clone %s: %v", id, err)
	}
	manifest.Endpoint = endpoint
	return &manifest
}

func TestKeywordCapabilityManifests(t *testing.T) {
	tests := []struct {
		id           string
		endpointPath string
		maxUSD       float64
		arrayMax     float64
	}{
		{
			id:           "keyword-ideas-v1",
			endpointPath: "/dataforseo/dataforseo_labs/google/keyword_ideas/live",
			maxUSD:       0.27,
			arrayMax:     20,
		},
		{
			id:           "keyword-volume-v1",
			endpointPath: "/dataforseo/dataforseo_labs/google/keyword_overview/live",
			maxUSD:       0.16,
			arrayMax:     200,
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			m, ok := Get(test.id)
			if !ok {
				t.Fatalf("%s manifest is missing", test.id)
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("%s should validate: %v", test.id, err)
			}
			if m.Provider != "aisa" || m.Mode != ModeSync ||
				!strings.HasSuffix(m.Endpoint, test.endpointPath) ||
				m.Request == nil || m.Response == nil {
				t.Fatalf("unexpected %s contract: %#v", test.id, m)
			}
			if m.Request.BodyWrap != "array" ||
				m.Request.Headers["X-AISA-Max-Price-USD"] == "" ||
				m.Billing.Type != BillingPerUnit ||
				m.Billing.Unit != "micro_usd" ||
				m.Billing.PriceUSD != 0.000001 ||
				m.Billing.QuantityHeader != "X-AISA-Customer-Cost-Micros-USD" ||
				m.Billing.ReserveUSD != test.maxUSD ||
				m.Request.Defaults["location_name"] != "United States" ||
				m.Request.Defaults["language_code"] != "en" {
				t.Fatalf("unexpected %s request or billing fields: %#v", test.id, m)
			}
			arrayRange := m.Request.ArrayLengths["keywords"]
			if arrayRange.Min == nil || *arrayRange.Min != 1 ||
				arrayRange.Max == nil || *arrayRange.Max != test.arrayMax {
				t.Fatalf("unexpected %s keyword count range: %#v", test.id, arrayRange)
			}
			if m.Response.Success == nil ||
				m.Response.Success.Path != "$.tasks.0.status_code" ||
				len(m.Response.Success.Values) != 1 ||
				responseValuesEqual(m.Response.Success.Values[0], 20000) == false {
				t.Fatalf("unexpected %s success contract: %#v", test.id, m.Response.Success)
			}
			if len(m.Retry.RetryOn) != 2 || m.Retry.Attempts != 2 ||
				!containsInt(m.Retry.RetryOn, http.StatusTooManyRequests) ||
				!containsInt(m.Retry.RetryOn, http.StatusServiceUnavailable) {
				t.Fatalf("unexpected %s retry policy: %#v", test.id, m.Retry)
			}
		})
	}

	ideas, _ := Get("keyword-ideas-v1")
	if limit, ok := NumberValue(ideas.Request.Defaults["limit"]); !ok || limit != 50 {
		t.Fatalf("keyword ideas default limit should be 50: %#v", ideas.Request.Defaults)
	}
}

func TestKeywordIdeasRequestAndProjectedOutput(t *testing.T) {
	allowLocalTestUpstreams(t)
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	var received []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-aisa-key" {
			t.Errorf("bearer authorization is missing")
		}
		if r.Header.Get("X-AISA-Max-Price-USD") != "0.27" {
			t.Errorf("unexpected max price header: %s", r.Header.Get("X-AISA-Max-Price-USD"))
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := common.Unmarshal(raw, &received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "132000")
		_, _ = w.Write([]byte(`{
			"status_code":20000,
			"tasks":[{"status_code":20000,"result":[{"items":[
				{
					"keyword":"running shoes",
					"keyword_info":{
						"search_volume":12100,
						"cpc":1.25,
						"competition_level":"HIGH",
						"monthly_searches":[{"year":2026,"month":10,"search_volume":12000}]
					},
					"search_intent_info":{"main_intent":"commercial"},
					"must_not_leak":{"raw":true}
				},
				{
					"keyword":"trail shoes",
					"keyword_info":{"search_volume":2400,"cpc":0.75,"competition_level":"MEDIUM"}
				}
			]}]}]
		}`))
	}))
	defer server.Close()

	manifest := cloneKeywordManifest(t, "keyword-ideas-v1", server.URL)
	result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"keywords": []any{"running shoes"},
	}, "")
	if execErr != nil {
		t.Fatalf("keyword ideas failed: %v", execErr)
	}
	if len(received) != 1 {
		t.Fatalf("expected one-element request array, got %#v", received)
	}
	body, ok := received[0].(map[string]any)
	if !ok ||
		body["location_name"] != "United States" ||
		body["language_code"] != "en" ||
		body["limit"] != float64(50) ||
		body["offset"] != float64(0) ||
		body["include_serp_info"] != false ||
		body["include_clickstream_data"] != false {
		t.Fatalf("unexpected keyword ideas request: %#v", received)
	}
	keywords, ok := result.Output["keywords"].([]any)
	if !ok || len(keywords) != 2 {
		t.Fatalf("unexpected projected keywords: %#v", result.Output)
	}
	first := keywords[0].(map[string]any)
	if first["keyword"] != "running shoes" ||
		first["search_volume"] != float64(12100) ||
		first["cpc"] != float64(1.25) ||
		first["competition"] != "HIGH" ||
		first["intent"] != "commercial" {
		t.Fatalf("declared fields were not projected: %#v", first)
	}
	if _, exists := first["must_not_leak"]; exists {
		t.Fatalf("undeclared upstream field leaked: %#v", first)
	}
	if len(first) != 6 {
		t.Fatalf("output must contain only six declared fields, got %#v", first)
	}
	second := keywords[1].(map[string]any)
	if second["intent"] != nil || second["monthly_searches"] != nil {
		t.Fatalf("missing optional fields should project as null: %#v", second)
	}
}

func TestKeywordVolumeRequestAndProjectedOutput(t *testing.T) {
	allowLocalTestUpstreams(t)
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	var received []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AISA-Max-Price-USD") != "0.16" {
			t.Errorf("unexpected max price header: %s", r.Header.Get("X-AISA-Max-Price-USD"))
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := common.Unmarshal(raw, &received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "78480")
		_, _ = w.Write([]byte(`{
			"status_code":20000,
			"tasks":[{"status_code":20000,"result":[{"items":[
				{
					"keyword":"laufschuhe",
					"search_volume":9900,
					"cpc":0.89,
					"competition":"MEDIUM",
					"monthly_searches":[{"year":2026,"month":10,"search_volume":9900}],
					"search_intent_info":{"main_intent":"transactional"},
					"keyword_info":{"must_not_leak":true},
					"serp_info":{"must_not_leak":true}
				}
			]}]}]
		}`))
	}))
	defer server.Close()

	manifest := cloneKeywordManifest(t, "keyword-volume-v1", server.URL)
	result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"keywords":      []any{"laufschuhe", "laufschuhe kaufen"},
		"location_name": "Germany",
		"language_code": "de",
	}, "")
	if execErr != nil {
		t.Fatalf("keyword volume failed: %v", execErr)
	}
	body, ok := received[0].(map[string]any)
	if !ok ||
		body["location_name"] != "Germany" ||
		body["language_code"] != "de" ||
		body["include_serp_info"] != false ||
		body["include_clickstream_data"] != false {
		t.Fatalf("unexpected keyword volume request: %#v", received)
	}
	keywords := result.Output["keywords"].([]any)
	item := keywords[0].(map[string]any)
	if item["keyword"] != "laufschuhe" ||
		item["search_volume"] != float64(9900) ||
		item["cpc"] != float64(0.89) ||
		item["competition"] != "MEDIUM" ||
		item["intent"] != "transactional" {
		t.Fatalf("unexpected projected item: %#v", item)
	}
	if len(item) != 6 {
		t.Fatalf("output must contain only six declared fields, got %#v", item)
	}
}

func TestKeywordArrayLimitsAreValidated(t *testing.T) {
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	ideas, _ := Get("keyword-ideas-v1")
	volume, _ := Get("keyword-volume-v1")
	tests := []struct {
		name  string
		m     *Manifest
		input map[string]any
		valid bool
	}{
		{
			name:  "ideas 20 seeds",
			m:     ideas,
			input: map[string]any{"keywords": repeatedKeywords(20)},
			valid: true,
		},
		{
			name:  "ideas 21 seeds",
			m:     ideas,
			input: map[string]any{"keywords": repeatedKeywords(21)},
		},
		{
			name:  "volume 200 keywords",
			m:     volume,
			input: map[string]any{"keywords": repeatedKeywords(200)},
			valid: true,
		},
		{
			name:  "volume 201 keywords",
			m:     volume,
			input: map[string]any{"keywords": repeatedKeywords(201)},
		},
		{
			name:  "non-array",
			m:     volume,
			input: map[string]any{"keywords": "one keyword"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execErr := ValidateSync(test.m, test.input)
			if test.valid && execErr != nil {
				t.Fatalf("expected input to be accepted: %v", execErr)
			}
			if !test.valid && (execErr == nil || execErr.Code != "invalid_request" || execErr.HTTPStatus != http.StatusBadRequest) {
				t.Fatalf("expected invalid_request, got %#v", execErr)
			}
		})
	}
}

func repeatedKeywords(count int) []any {
	keywords := make([]any, count)
	for i := range keywords {
		keywords[i] = "keyword " + strings.Repeat("x", i+1)
	}
	return keywords
}

func TestKeywordInternalStatusAndErrorMapDoNotLeak(t *testing.T) {
	allowLocalTestUpstreams(t)
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantLog    string
	}{
		{
			name:       "internal task failure",
			status:     http.StatusOK,
			body:       `{"tasks":[{"status_code":40501,"status_message":"secret task detail"}]}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_task_failed",
		},
		{
			name:       "balance",
			status:     http.StatusPaymentRequired,
			body:       `{"error":{"code":"insufficient_wallet_balance","message":"secret wallet detail"}}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_wallet_balance",
		},
		{
			name:       "cost cap",
			status:     http.StatusPaymentRequired,
			body:       `{"error":{"code":"estimated_price_exceeds_max_price","message":"secret cap detail"}}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "cost_cap_exceeded",
			wantLog:    "upstream_cost_cap_exceeded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			manifest := cloneKeywordManifest(t, "keyword-volume-v1", server.URL)
			_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
				"keywords": []any{"running shoes"},
			}, "")
			if execErr == nil || execErr.HTTPStatus != test.wantStatus ||
				execErr.Code != test.wantCode || execErr.LogCode != test.wantLog {
				t.Fatalf("unexpected error: %#v", execErr)
			}
			if calls != 1 {
				t.Fatalf("internal or mapped failure must not retry, got %d calls", calls)
			}
			if strings.Contains(execErr.Message, "secret") || strings.Contains(execErr.Error(), "secret") {
				t.Fatalf("upstream message leaked: %v", execErr)
			}
		})
	}
}
