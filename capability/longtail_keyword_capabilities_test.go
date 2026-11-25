package capability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type longTailKeywordCapability struct {
	id           string
	endpoint     string
	input        map[string]any
	requestFile  string
	responseFile string
	outputFile   string
}

func longTailKeywordCapabilities() []longTailKeywordCapability {
	return []longTailKeywordCapability{
		{
			id:           "keyword-suggestions-v1",
			endpoint:     "https://api.dataforseo.com/v3/dataforseo_labs/google/keyword_suggestions/live",
			input:        map[string]any{"keyword": "running shoes"},
			requestFile:  "keyword-suggestions-request.json",
			responseFile: "keyword-suggestions.json",
			outputFile:   "keyword-suggestions-output.json",
		},
		{
			id:           "related-keywords-v1",
			endpoint:     "https://api.dataforseo.com/v3/dataforseo_labs/google/related_keywords/live",
			input:        map[string]any{"keyword": "running shoes"},
			requestFile:  "related-keywords-request.json",
			responseFile: "related-keywords.json",
			outputFile:   "related-keywords-output.json",
		},
	}
}

func TestLongTailKeywordManifestContracts(t *testing.T) {
	seen := map[string]struct{}{}
	for _, test := range longTailKeywordCapabilities() {
		t.Run(test.id, func(t *testing.T) {
			if _, exists := seen[test.id]; exists {
				t.Fatalf("%s is listed twice", test.id)
			}
			seen[test.id] = struct{}{}

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
				m.Billing.ReserveUSD != 0.12 {
				t.Fatalf("unexpected %s billing fields: %#v", test.id, m.Billing)
			}
			if !strings.Contains(m.Description, "$0.12") {
				t.Fatalf("%s description must state the reserve: %q", test.id, m.Description)
			}
			if m.Request.BodyWrap != "array" {
				t.Fatalf("%s must wrap the task in a single-element array", test.id)
			}
			if len(m.Request.Required) != 1 || m.Request.Required[0] != "keyword" {
				t.Fatalf("%s must require exactly one seed keyword: %#v", test.id, m.Request.Required)
			}
			if location, ok := m.Request.Defaults["location_name"].(string); !ok || location != "United States" {
				t.Fatalf("%s location_name default is wrong: %#v", test.id, m.Request.Defaults)
			}
			if language, ok := m.Request.Defaults["language_code"].(string); !ok || language != "en" {
				t.Fatalf("%s language_code default is wrong: %#v", test.id, m.Request.Defaults)
			}
			limitRange := m.Request.Ranges["limit"]
			if limitRange.Min == nil || *limitRange.Min != 1 ||
				limitRange.Max == nil || *limitRange.Max != 1000 || !limitRange.Integer {
				t.Fatalf("unexpected %s limit range: %#v", test.id, limitRange)
			}
			offsetRange := m.Request.Ranges["offset"]
			if offsetRange.Min == nil || *offsetRange.Min != 0 || offsetRange.Max != nil || !offsetRange.Integer {
				t.Fatalf("unexpected %s offset range: %#v", test.id, offsetRange)
			}
			for _, field := range []string{"include_serp_info", "include_clickstream_data"} {
				value, ok := m.Request.Consts[field].(bool)
				if !ok || value {
					t.Fatalf("%s must pin %s=false: %#v", test.id, field, m.Request.Consts)
				}
			}
			if m.Response.Success == nil ||
				m.Response.Success.Path != "$.tasks.0.status_code" ||
				len(m.Response.Success.Values) != 1 ||
				!responseValuesEqual(m.Response.Success.Values[0], 20000) {
				t.Fatalf("unexpected %s success contract: %#v", test.id, m.Response.Success)
			}
			if len(m.Response.Collections) != 1 || m.Response.Collections["keywords"].Path != "$.tasks.0.result.0.items" {
				t.Fatalf("unexpected %s collection contract: %#v", test.id, m.Response.Collections)
			}
			for _, code := range []string{"400", "401", "40200", "40210", "429", "40501", "500", "503", "50401"} {
				if _, ok := m.Response.ErrorMap[code]; !ok {
					t.Fatalf("%s is missing error_map entry %s", test.id, code)
				}
			}
			if len(m.Retry.RetryOn) != 2 || m.Retry.Attempts != 2 ||
				!containsInt(m.Retry.RetryOn, http.StatusTooManyRequests) ||
				!containsInt(m.Retry.RetryOn, http.StatusServiceUnavailable) {
				t.Fatalf("unexpected %s retry policy: %#v", test.id, m.Retry)
			}
		})
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 long-tail keyword capabilities, got %d", len(seen))
	}
}

func TestLongTailKeywordOutputFieldPaths(t *testing.T) {
	tests := []struct {
		id          string
		wantPaths   map[string]string
		noDepthPath bool
	}{
		{
			id: "keyword-suggestions-v1",
			wantPaths: map[string]string{
				"keyword":            "$.keyword",
				"search_volume":      "$.keyword_info.search_volume",
				"cpc":                "$.keyword_info.cpc",
				"competition":        "$.keyword_info.competition_level",
				"keyword_difficulty": "$.keyword_properties.keyword_difficulty",
				"intent":             "$.search_intent_info.main_intent",
				"monthly_searches":   "$.keyword_info.monthly_searches",
			},
		},
		{
			id: "related-keywords-v1",
			wantPaths: map[string]string{
				"keyword":            "$.keyword_data.keyword",
				"search_volume":      "$.keyword_data.keyword_info.search_volume",
				"cpc":                "$.keyword_data.keyword_info.cpc",
				"competition":        "$.keyword_data.keyword_info.competition_level",
				"keyword_difficulty": "$.keyword_data.keyword_properties.keyword_difficulty",
				"intent":             "$.keyword_data.search_intent_info.main_intent",
				"monthly_searches":   "$.keyword_data.keyword_info.monthly_searches",
				"depth":              "$.depth",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			m, ok := Get(test.id)
			if !ok {
				t.Fatalf("%s manifest is missing", test.id)
			}
			collection := m.Response.Collections["keywords"]
			if len(collection.FieldPaths) != len(test.wantPaths) {
				t.Fatalf("%s exposes %d fields, want %d: %#v", test.id, len(collection.FieldPaths), len(test.wantPaths), collection.FieldPaths)
			}
			for field, wantPath := range test.wantPaths {
				if collection.FieldPaths[field] != wantPath {
					t.Fatalf("%s field %s maps to %q, want %q", test.id, field, collection.FieldPaths[field], wantPath)
				}
			}
		})
	}
}

func TestLongTailKeywordRequestsAndProjectedOutputs(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	for _, test := range longTailKeywordCapabilities() {
		t.Run(test.id, func(t *testing.T) {
			responseBody := readKeywordFixture(t, test.responseFile)
			wantRequest := strings.TrimSpace(string(readKeywordFixture(t, test.requestFile)))
			var actualRequest string
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
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
			if gotPath != "/" {
				t.Fatalf("%s must POST to the manifest endpoint root, got %s", test.id, gotPath)
			}
			if actualRequest != wantRequest {
				t.Fatalf("%s request body is wrong:\nwant: %s\n got: %s", test.id, wantRequest, actualRequest)
			}
			actualOutput := marshalTestOutput(t, result.Output)
			wantOutput := strings.TrimSpace(string(readKeywordFixture(t, test.outputFile)))
			if actualOutput != wantOutput {
				t.Fatalf("%s projected output is wrong:\nwant: %s\n got: %s", test.id, wantOutput, actualOutput)
			}
		})
	}
}

func TestLongTailKeywordOptionalInputsAreForwarded(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	tests := []struct {
		name  string
		id    string
		input map[string]any
		want  []string
	}{
		{
			name: "suggestions with every optional input",
			id:   "keyword-suggestions-v1",
			input: map[string]any{
				"keyword":              "laufschuhe",
				"location_name":        "Germany",
				"language_code":        "de",
				"limit":                1000,
				"offset":               20,
				"include_seed_keyword": true,
			},
			want: []string{
				`"keyword":"laufschuhe"`,
				`"location_name":"Germany"`,
				`"language_code":"de"`,
				`"limit":1000`,
				`"offset":20`,
				`"include_seed_keyword":true`,
				`"include_serp_info":false`,
				`"include_clickstream_data":false`,
			},
		},
		{
			name: "related keywords with explicit depth",
			id:   "related-keywords-v1",
			input: map[string]any{
				"keyword":       "laufschuhe",
				"location_name": "Germany",
				"language_code": "de",
				"depth":         4,
				"limit":         1000,
				"offset":        10,
			},
			want: []string{
				`"keyword":"laufschuhe"`,
				`"location_name":"Germany"`,
				`"language_code":"de"`,
				`"depth":4`,
				`"limit":1000`,
				`"offset":10`,
				`"include_serp_info":false`,
				`"include_clickstream_data":false`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var actualRequest string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				actualRequest = string(raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(readKeywordFixture(t, strings.TrimSuffix(test.id, "-v1")+".json"))
			}))
			defer server.Close()

			manifest := cloneKeywordManifest(t, test.id, server.URL)
			if _, execErr := ExecuteSync(context.Background(), manifest, test.input, ""); execErr != nil {
				t.Fatalf("%s failed: %v", test.id, execErr)
			}
			for _, want := range test.want {
				if !strings.Contains(actualRequest, want) {
					t.Fatalf("%s request body is missing %s: %s", test.id, want, actualRequest)
				}
			}
		})
	}
}

func TestLongTailKeywordInputRangesAreValidated(t *testing.T) {
	setDataForSEOAuth(t)
	tests := []struct {
		id    string
		input map[string]any
		valid bool
	}{
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes"}, valid: true},
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes", "limit": 1, "offset": 0}, valid: true},
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes", "limit": 1000}, valid: true},
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes", "limit": 0}},
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes", "limit": 1001}},
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes", "limit": 10.5}},
		{id: "keyword-suggestions-v1", input: map[string]any{"keyword": "shoes", "offset": -1}},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "depth": 0}, valid: true},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "depth": 4}, valid: true},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "depth": -1}},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "depth": 5}},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "depth": 1.5}},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "limit": 1001}},
		{id: "related-keywords-v1", input: map[string]any{"keyword": "shoes", "offset": -1}},
	}
	for _, test := range tests {
		t.Run(test.id+"/"+inputLabel(test.input), func(t *testing.T) {
			m, ok := Get(test.id)
			if !ok {
				t.Fatalf("%s manifest is missing", test.id)
			}
			execErr := ValidateSync(m, test.input)
			if test.valid && execErr != nil {
				t.Fatalf("%s should accept %#v: %v", test.id, test.input, execErr)
			}
			if !test.valid && (execErr == nil || execErr.Code != "invalid_request" ||
				execErr.HTTPStatus != http.StatusBadRequest) {
				t.Fatalf("%s expected invalid_request for %#v, got %#v", test.id, test.input, execErr)
			}
		})
	}
}

func inputLabel(input map[string]any) string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := input[key]
		if number, ok := requestNumberValue(value); ok {
			parts = append(parts, key+"="+strconv.FormatFloat(number, 'f', -1, 64))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	return strings.Join(parts, ",")
}

func TestLongTailKeywordRequiredSeedKeywordIsValidated(t *testing.T) {
	setDataForSEOAuth(t)
	for _, test := range longTailKeywordCapabilities() {
		m, ok := Get(test.id)
		if !ok {
			t.Fatalf("%s manifest is missing", test.id)
		}
		inputs := map[string]map[string]any{
			"missing": {},
			"blank":   {"keyword": "   "},
			"empty":   {"keyword": ""},
		}
		for name, input := range inputs {
			t.Run(test.id+"/"+name+" keyword", func(t *testing.T) {
				execErr := ValidateSync(m, input)
				if execErr == nil || execErr.Code != "invalid_request" ||
					execErr.HTTPStatus != http.StatusBadRequest {
					t.Fatalf("%s expected invalid_request, got %#v", test.id, execErr)
				}
			})
		}
	}
}

func TestLongTailKeywordUpstreamErrorsAreMapped(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantLog    string
		wantCalls  int
	}{
		{
			name:       "invalid status code 40501",
			status:     http.StatusOK,
			body:       `{"status_code":20000,"cost":0,"tasks":[{"status_code":40501,"status_message":"secret task detail"}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantLog:    "upstream_error_mapped",
			wantCalls:  1,
		},
		{
			name:       "http 400",
			status:     http.StatusBadRequest,
			body:       `{"status_code":40000,"status_message":"secret request detail","tasks":[]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantLog:    "upstream_error_mapped",
			wantCalls:  1,
		},
		{
			name:       "balance 40210",
			status:     http.StatusOK,
			body:       `{"status_code":20000,"cost":0,"tasks":[{"status_code":40210,"status_message":"secret balance detail"}]}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_balance_unavailable",
			wantCalls:  1,
		},
		{
			name:       "http 500",
			status:     http.StatusInternalServerError,
			body:       `{"status_code":50000,"status_message":"secret provider detail","tasks":[]}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_provider_failure",
			wantCalls:  1,
		},
		{
			name:       "http 503 is retried once",
			status:     http.StatusServiceUnavailable,
			body:       `{"status_code":50301,"status_message":"secret unavailable detail","tasks":[]}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_unavailable",
			wantCalls:  2,
		},
	}
	for _, capability := range longTailKeywordCapabilities() {
		for _, test := range tests {
			t.Run(capability.id+"/"+test.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(test.status)
					_, _ = w.Write([]byte(test.body))
				}))
				defer server.Close()

				manifest := cloneKeywordManifest(t, capability.id, server.URL)
				_, execErr := ExecuteSync(context.Background(), manifest, capability.input, "")
				if execErr == nil || execErr.Code != test.wantCode ||
					execErr.HTTPStatus != test.wantStatus || execErr.LogCode != test.wantLog {
					t.Fatalf("%s unexpected error: %#v", capability.id, execErr)
				}
				if calls != test.wantCalls {
					t.Fatalf("%s made %d upstream calls, want %d", capability.id, calls, test.wantCalls)
				}
				errorText := execErr.Message + " " + execErr.Error()
				for _, secret := range []string{
					"secret",
					dataForSEOLoginFixture,
					dataForSEOPasswordFixture,
					wantAuthorizationFixture(),
				} {
					if strings.Contains(errorText, secret) {
						t.Fatalf("%s leaked a sensitive value: %v", capability.id, execErr)
					}
				}
			})
		}
	}
}

func TestLongTailKeywordTimeoutIsReportedAsUncertain(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	for _, test := range longTailKeywordCapabilities() {
		t.Run(test.id, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				time.Sleep(1400 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(readKeywordFixture(t, test.responseFile))
			}))
			defer server.Close()

			manifest := cloneKeywordManifest(t, test.id, server.URL)
			manifest.Limits.Timeout = "1s"
			_, execErr := ExecuteSync(context.Background(), manifest, test.input, "")
			if execErr == nil || execErr.Code != "upstream_timeout" ||
				execErr.HTTPStatus != http.StatusGatewayTimeout ||
				!execErr.RequestWritten || !execErr.MayHaveCharged {
				t.Fatalf("%s expected an uncertain upstream timeout, got %#v", test.id, execErr)
			}
			if calls != 1 {
				t.Fatalf("%s retried after a timeout: %d calls", test.id, calls)
			}
		})
	}
}

// A seed with no results comes back as an empty result list, missing items or
// `items: null`; all three are an empty success, not a failed call.
func TestLongTailKeywordEmptyResultsSucceed(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	bodies := map[string]string{
		"result array is empty": `{"status_code":20000,"cost":0.01,"tasks":[{"status_code":20000,"result":[]}]}`,
		"items are missing":     `{"status_code":20000,"cost":0.01,"tasks":[{"status_code":20000,"result":[{"se_type":"google"}]}]}`,
		"items are null":        `{"status_code":20000,"cost":0.01,"tasks":[{"status_code":20000,"result":[{"se_type":"google","items":null}]}]}`,
	}
	for _, capability := range longTailKeywordCapabilities() {
		for name, body := range bodies {
			t.Run(capability.id+"/"+name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()

				manifest := cloneKeywordManifest(t, capability.id, server.URL)
				result, execErr := ExecuteSync(context.Background(), manifest, capability.input, "")
				if execErr != nil {
					t.Fatalf("%s empty result should succeed: %#v", capability.id, execErr)
				}
				items, ok := result.Output["keywords"].([]any)
				if !ok || len(items) != 0 {
					t.Fatalf("%s expected an empty keyword list: %#v", capability.id, result.Output)
				}
			})
		}
	}
}

func TestLongTailKeywordMissingUpstreamFieldsFail(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "tasks are missing",
			body:       `{"status_code":20000,"cost":0.01,"tasks":[]}`,
			wantCode:   "upstream_task_failed",
			wantStatus: http.StatusBadGateway,
		},
	}
	for _, capability := range longTailKeywordCapabilities() {
		for _, test := range tests {
			t.Run(capability.id+"/"+test.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(test.body))
				}))
				defer server.Close()

				manifest := cloneKeywordManifest(t, capability.id, server.URL)
				_, execErr := ExecuteSync(context.Background(), manifest, capability.input, "")
				if execErr == nil || execErr.Code != test.wantCode ||
					execErr.HTTPStatus != test.wantStatus {
					t.Fatalf("%s expected %s, got %#v", capability.id, test.wantCode, execErr)
				}
			})
		}
	}
}

func TestLongTailKeywordCollectionFieldsMayBeNull(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	for _, test := range longTailKeywordCapabilities() {
		t.Run(test.id, func(t *testing.T) {
			// The second fixture item has no intent, difficulty or trend data;
			// projections must keep the keys with null values instead of failing.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(readKeywordFixture(t, test.responseFile))
			}))
			defer server.Close()

			manifest := cloneKeywordManifest(t, test.id, server.URL)
			result, execErr := ExecuteSync(context.Background(), manifest, test.input, "")
			if execErr != nil {
				t.Fatalf("%s failed: %v", test.id, execErr)
			}
			items, ok := result.Output["keywords"].([]any)
			if !ok || len(items) != 2 {
				t.Fatalf("%s unexpected collection: %#v", test.id, result.Output["keywords"])
			}
			second, ok := items[1].(map[string]any)
			if !ok {
				t.Fatalf("%s unexpected item: %#v", test.id, items[1])
			}
			for _, field := range []string{"intent", "keyword_difficulty", "monthly_searches"} {
				if value, exists := second[field]; !exists || value != nil {
					t.Fatalf("%s field %s should be present and null: %#v", test.id, field, second)
				}
			}
			raw, err := common.Marshal(second)
			if err != nil {
				t.Fatalf("encode item: %v", err)
			}
			if !strings.Contains(string(raw), `"intent":null`) {
				t.Fatalf("%s null fields must stay in the output: %s", test.id, raw)
			}
		})
	}
}
