package capability

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

const (
	dataForSEOLoginFixture    = "dataforseo-login"
	dataForSEOPasswordFixture = "dataforseo-password"
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

func setDataForSEOAuth(t *testing.T) {
	t.Helper()
	t.Setenv("CAPABILITY_DATAFORSEO_LOGIN", dataForSEOLoginFixture)
	t.Setenv("CAPABILITY_DATAFORSEO_PASSWORD", dataForSEOPasswordFixture)
}

func readKeywordFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "dataforseo", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func TestKeywordCapabilityManifests(t *testing.T) {
	tests := []struct {
		id          string
		endpoint    string
		maxUSD      float64
		arrayMax    float64
		hasLimit    bool
		description string
	}{
		{
			id:          "keyword-ideas-v1",
			endpoint:    "https://api.dataforseo.com/v3/dataforseo_labs/google/keyword_ideas/live",
			maxUSD:      0.04,
			arrayMax:    20,
			hasLimit:    true,
			description: "keywords, location_name, language_code, limit (default 50, max 200), and offset",
		},
		{
			id:          "keyword-volume-v1",
			endpoint:    "https://api.dataforseo.com/v3/dataforseo_labs/google/keyword_overview/live",
			maxUSD:      0.04,
			arrayMax:    200,
			description: "keywords, location_name, and language_code",
		},
		{
			id:          "serp-google-v1",
			endpoint:    "https://api.dataforseo.com/v3/serp/google/organic/live/advanced",
			maxUSD:      0.21,
			description: "keyword, location, language, device, and depth",
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
			if m.Provider != "dataforseo" || m.Mode != ModeSync || m.Endpoint != test.endpoint ||
				m.Request == nil || m.Response == nil {
				t.Fatalf("unexpected %s contract: %#v", test.id, m)
			}
			if m.Auth == nil || m.Auth.Type != "basic" ||
				m.Auth.SecretRef != "CAPABILITY_DATAFORSEO_LOGIN" ||
				m.Auth.PasswordRef != "CAPABILITY_DATAFORSEO_PASSWORD" {
				t.Fatalf("unexpected %s auth: %#v", test.id, m.Auth)
			}
			if m.Billing.Type != BillingPerUnit ||
				m.Billing.Unit != "usd" ||
				m.Billing.PriceUSD != 1 ||
				m.Billing.QuantityPath != "$.cost" ||
				m.Billing.QuantityHeader != "" ||
				m.Billing.ReserveUSD != test.maxUSD {
				t.Fatalf("unexpected %s billing fields: %#v", test.id, m.Billing)
			}
			if m.Request.Headers["X-AISA-Max-Price-USD"] != "" ||
				!strings.Contains(m.Description, test.description) ||
				!strings.Contains(m.Description, "$"+strconv.FormatFloat(test.maxUSD, 'f', -1, 64)) {
				t.Fatalf("unexpected %s description or AISA header: %#v", test.id, m)
			}
			if m.Response.Success == nil ||
				m.Response.Success.Path != "$.tasks.0.status_code" ||
				len(m.Response.Success.Values) != 1 ||
				!responseValuesEqual(m.Response.Success.Values[0], 20000) {
				t.Fatalf("unexpected %s success contract: %#v", test.id, m.Response.Success)
			}
			if len(m.Retry.RetryOn) != 2 || m.Retry.Attempts != 2 ||
				!containsInt(m.Retry.RetryOn, http.StatusTooManyRequests) ||
				!containsInt(m.Retry.RetryOn, http.StatusServiceUnavailable) {
				t.Fatalf("unexpected %s retry policy: %#v", test.id, m.Retry)
			}
			if test.arrayMax > 0 {
				arrayRange := m.Request.ArrayLengths["keywords"]
				if arrayRange.Min == nil || *arrayRange.Min != 1 ||
					arrayRange.Max == nil || *arrayRange.Max != test.arrayMax {
					t.Fatalf("unexpected %s keyword count range: %#v", test.id, arrayRange)
				}
			}
			if test.hasLimit {
				limitRange := m.Request.Ranges["limit"]
				if limitRange.Min == nil || *limitRange.Min != 1 ||
					limitRange.Max == nil || *limitRange.Max != 200 || !limitRange.Integer {
					t.Fatalf("unexpected %s limit range: %#v", test.id, limitRange)
				}
				if limit, ok := NumberValue(m.Request.Defaults["limit"]); !ok || limit != 50 {
					t.Fatalf("keyword ideas default limit should be 50: %#v", m.Request.Defaults)
				}
			}
		})
	}
}

func TestDataForSEOKeywordRequestsAndProjectedOutputs(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	tests := []struct {
		name         string
		id           string
		input        map[string]any
		responseFile string
		requestFile  string
		outputFile   string
	}{
		{
			name:         "keyword ideas",
			id:           "keyword-ideas-v1",
			input:        map[string]any{"keywords": []any{"running shoes"}},
			responseFile: "keyword-ideas.json",
			requestFile:  "keyword-ideas-request.json",
			outputFile:   "keyword-ideas-output.json",
		},
		{
			name: "keyword volume",
			id:   "keyword-volume-v1",
			input: map[string]any{
				"keywords":      []any{"laufschuhe", "laufschuhe kaufen"},
				"location_name": "Germany",
				"language_code": "de",
			},
			responseFile: "keyword-volume.json",
			requestFile:  "keyword-volume-request.json",
			outputFile:   "keyword-volume-output.json",
		},
		{
			name:         "google serp",
			id:           "serp-google-v1",
			input:        map[string]any{"keyword": "running shoes"},
			responseFile: "serp-google.json",
			requestFile:  "serp-google-request.json",
			outputFile:   "serp-google-output.json",
		},
	}
	wantAuthorization := "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(dataForSEOLoginFixture+":"+dataForSEOPasswordFixture),
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseBody := readKeywordFixture(t, test.responseFile)
			wantRequest := strings.TrimSpace(string(readKeywordFixture(t, test.requestFile)))
			var actualRequest string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != wantAuthorization {
					t.Errorf("basic authorization header is incorrect")
				}
				for name := range r.Header {
					if strings.HasPrefix(strings.ToLower(name), "x-aisa-") {
						t.Errorf("AISA header remained after migration: %s", name)
					}
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				actualRequest = strings.TrimSpace(string(raw))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			manifest := cloneKeywordManifest(t, test.id, server.URL)
			result, execErr := ExecuteSync(context.Background(), manifest, test.input, "")
			if execErr != nil {
				t.Fatalf("%s failed: %v", test.id, execErr)
			}
			if actualRequest != wantRequest {
				t.Fatalf("request body changed after migration:\nwant: %s\n got: %s", wantRequest, actualRequest)
			}
			actualOutput, err := common.Marshal(result.Output)
			if err != nil {
				t.Fatalf("encode output: %v", err)
			}
			wantOutput := strings.TrimSpace(string(readKeywordFixture(t, test.outputFile)))
			if string(actualOutput) != wantOutput {
				t.Fatalf("projected output changed after migration:\nwant: %s\n got: %s", wantOutput, actualOutput)
			}
		})
	}
}

func TestKeywordArrayLimitsAreValidated(t *testing.T) {
	setDataForSEOAuth(t)
	ideas, _ := Get("keyword-ideas-v1")
	volume, _ := Get("keyword-volume-v1")
	tests := []struct {
		name  string
		m     *Manifest
		input map[string]any
		valid bool
	}{
		{name: "ideas 20 seeds", m: ideas, input: map[string]any{"keywords": repeatedKeywords(20)}, valid: true},
		{name: "ideas 21 seeds", m: ideas, input: map[string]any{"keywords": repeatedKeywords(21)}},
		{name: "volume 200 keywords", m: volume, input: map[string]any{"keywords": repeatedKeywords(200)}, valid: true},
		{name: "volume 201 keywords", m: volume, input: map[string]any{"keywords": repeatedKeywords(201)}},
		{name: "non-array", m: volume, input: map[string]any{"keywords": "one keyword"}},
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

func TestDataForSEOInternalStatusAndErrorMapDoNotLeak(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantLog    string
	}{
		{
			name:       "invalid task status",
			status:     http.StatusOK,
			body:       `{"status_code":20000,"cost":0,"tasks":[{"status_code":40501,"status_message":"secret task detail"}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantLog:    "upstream_error_mapped",
		},
		{
			name:       "balance",
			status:     http.StatusOK,
			body:       `{"status_code":40210,"cost":0,"tasks":[{"status_code":40210,"status_message":"secret balance detail"}]}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_balance_unavailable",
		},
		{
			name:       "authentication",
			status:     http.StatusUnauthorized,
			body:       `{"status_code":40100,"status_message":"secret auth detail","tasks":[]}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_authentication",
		},
		{
			name:       "rate limit",
			status:     http.StatusOK,
			body:       `{"status_code":40202,"cost":0,"tasks":[{"status_code":40202,"status_message":"secret rate detail"}]}`,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "upstream_rate_limited",
			wantLog:    "upstream_rate_limited",
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
			errorText := execErr.Message + " " + execErr.Error()
			for _, secret := range []string{
				"secret",
				dataForSEOLoginFixture,
				dataForSEOPasswordFixture,
				wantAuthorizationFixture(),
			} {
				if strings.Contains(errorText, secret) {
					t.Fatalf("sensitive value leaked in error: %v", execErr)
				}
			}
		})
	}
}

func wantAuthorizationFixture() string {
	return base64.StdEncoding.EncodeToString([]byte(dataForSEOLoginFixture + ":" + dataForSEOPasswordFixture))
}
