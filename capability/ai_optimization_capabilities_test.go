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

type aiOptimizationCapability struct {
	id           string
	endpoint     string
	reserveUSD   float64
	input        map[string]any
	requestFile  string
	responseFile string
	outputFile   string
}

func aiOptimizationCapabilities() []aiOptimizationCapability {
	answer := func(id string, se string) aiOptimizationCapability {
		base := strings.TrimSuffix(id, "-v1")
		return aiOptimizationCapability{
			id:           id,
			endpoint:     "https://api.dataforseo.com/v3/ai_optimization/" + se + "/llm_responses/live",
			input:        map[string]any{"prompt": "best wooden toy set"},
			requestFile:  base + "-request.json",
			responseFile: base + ".json",
			outputFile:   base + "-output.json",
		}
	}
	scrape := func(id string, se string) aiOptimizationCapability {
		base := strings.TrimSuffix(id, "-v1")
		return aiOptimizationCapability{
			id:           id,
			endpoint:     "https://api.dataforseo.com/v3/ai_optimization/" + se + "/llm_scraper/live/advanced",
			input:        map[string]any{"keyword": "best wooden toy set"},
			requestFile:  base + "-request.json",
			responseFile: base + ".json",
			outputFile:   base + "-output.json",
		}
	}
	mentions := func(id string, fn string, key string) aiOptimizationCapability {
		base := strings.TrimSuffix(id, "-v1")
		target := map[string]any{"domain": "fixture-brand.example"}
		input := map[string]any{"target": []any{target}}
		if key != "" {
			input = map[string]any{
				"target":   []any{map[string]any{"keyword": "wooden toy set"}},
				"platform": "google",
			}
		}
		return aiOptimizationCapability{
			id:           id,
			endpoint:     "https://api.dataforseo.com/v3/ai_optimization/llm_mentions/" + fn + "/live",
			input:        input,
			requestFile:  base + "-request.json",
			responseFile: base + ".json",
			outputFile:   base + "-output.json",
		}
	}

	chatgpt := answer("ai-answer-chatgpt-v1", "chat_gpt")
	chatgpt.reserveUSD = 0.20
	claude := answer("ai-answer-claude-v1", "claude")
	claude.reserveUSD = 0.20
	gemini := answer("ai-answer-gemini-v1", "gemini")
	gemini.reserveUSD = 0.10
	perplexity := answer("ai-answer-perplexity-v1", "perplexity")
	perplexity.reserveUSD = 0.10

	scrapeChatGPT := scrape("ai-answer-scrape-chatgpt-v1", "chat_gpt")
	scrapeChatGPT.reserveUSD = 0.01
	scrapeGemini := scrape("ai-answer-scrape-gemini-v1", "gemini")
	scrapeGemini.reserveUSD = 0.01

	search := mentions("llm-mentions-search-v1", "search", "")
	search.reserveUSD = 0.25
	topDomains := mentions("llm-mentions-top-domains-v1", "top_domains", "keyword")
	topDomains.reserveUSD = 0.25
	topPages := mentions("llm-mentions-top-pages-v1", "top_pages", "keyword")
	topPages.reserveUSD = 0.25

	return []aiOptimizationCapability{
		chatgpt,
		claude,
		gemini,
		perplexity,
		scrapeChatGPT,
		scrapeGemini,
		{
			id:           "serp-google-ai-mode-v1",
			endpoint:     "https://api.dataforseo.com/v3/serp/google/ai_mode/live/advanced",
			reserveUSD:   0.01,
			input:        map[string]any{"keyword": "best wooden toy set"},
			requestFile:  "serp-google-ai-mode-request.json",
			responseFile: "serp-google-ai-mode.json",
			outputFile:   "serp-google-ai-mode-output.json",
		},
		search,
		topDomains,
		topPages,
		{
			id:           "ai-keyword-volume-v1",
			endpoint:     "https://api.dataforseo.com/v3/ai_optimization/ai_keyword_data/keywords_search_volume/live",
			reserveUSD:   0.05,
			input:        map[string]any{"keywords": []any{"wooden toy set"}},
			requestFile:  "ai-keyword-volume-request.json",
			responseFile: "ai-keyword-volume.json",
			outputFile:   "ai-keyword-volume-output.json",
		},
	}
}

func TestAIOptimizationManifestContracts(t *testing.T) {
	seen := map[string]struct{}{}
	for _, test := range aiOptimizationCapabilities() {
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
				m.Billing.ReserveUSD != test.reserveUSD {
				t.Fatalf("unexpected %s billing fields: %#v", test.id, m.Billing)
			}
			if m.Request.BodyWrap != "array" {
				t.Fatalf("%s must wrap the task in a single-element array", test.id)
			}
			if len(m.Request.Required) == 0 {
				t.Fatalf("%s must declare required inputs", test.id)
			}
			if m.Response.Success == nil ||
				m.Response.Success.Path != "$.tasks.0.status_code" ||
				len(m.Response.Success.Values) != 1 ||
				!responseValuesEqual(m.Response.Success.Values[0], 20000) {
				t.Fatalf("unexpected %s success contract: %#v", test.id, m.Response.Success)
			}
			if m.Response.Output["result"] != "$.tasks.0.result" {
				t.Fatalf("unexpected %s output contract: %#v", test.id, m.Response.Output)
			}
			for _, code := range []string{"40200", "40210", "40501", "500", "503"} {
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
	if len(seen) != 11 {
		t.Fatalf("expected 11 AI optimization capabilities, got %d", len(seen))
	}
}

func TestAIOptimizationRequestsAndProjectedOutputs(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	for _, test := range aiOptimizationCapabilities() {
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

func TestAIOptimizationForwardsExplicitOptionalInputs(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	tests := []struct {
		name    string
		id      string
		input   map[string]any
		want    []string
		notWant []string
	}{
		{
			name: "chatgpt answer with every optional input",
			id:   "ai-answer-chatgpt-v1",
			input: map[string]any{
				"prompt":                      "best wooden toy set",
				"model":                       "gpt-6-sol",
				"web_search":                  false,
				"web_search_country_iso_code": "DE",
				"web_search_city":             "Berlin",
				"system_message":              "answer briefly",
				"temperature":                 0.3,
			},
			want: []string{
				`"model_name":"gpt-6-sol"`,
				`"user_prompt":"best wooden toy set"`,
				`"web_search":false`,
				`"web_search_country_iso_code":"DE"`,
				`"web_search_city":"Berlin"`,
				`"system_message":"answer briefly"`,
				`"temperature":0.3`,
			},
			notWant: []string{"max_output_tokens", "top_p"},
		},
		{
			name: "claude answer with a different model",
			id:   "ai-answer-claude-v1",
			input: map[string]any{
				"prompt": "best wooden toy set",
				"model":  "claude-opus-5-1",
			},
			want: []string{
				`"model_name":"claude-opus-5-1"`,
				`"web_search":true`,
				`"web_search_country_iso_code":"US"`,
			},
			notWant: []string{"max_output_tokens", "web_search_city"},
		},
		{
			name: "mentions search with sorting and filters",
			id:   "llm-mentions-search-v1",
			input: map[string]any{
				"target":   []any{map[string]any{"keyword": "wooden toy set", "search_scope": []any{"question"}}},
				"platform": "google",
				"limit":    5,
				"order_by": []any{"ai_search_volume,desc"},
				"filters":  []any{"ai_search_volume", ">", 1000},
			},
			want: []string{
				`"platform":"google"`,
				`"limit":5`,
				`"order_by":["ai_search_volume,desc"]`,
				`"filters":["ai_search_volume",`,
				`1000]`,
				`"search_scope":["question"]`,
			},
			notWant: []string{"items_list_limit", "links_scope"},
		},
		{
			name: "top pages with links scope",
			id:   "llm-mentions-top-pages-v1",
			input: map[string]any{
				"target":           []any{map[string]any{"domain": "fixture-brand.example"}},
				"links_scope":      "search_results",
				"items_list_limit": 3,
			},
			want: []string{
				`"links_scope":"search_results"`,
				`"items_list_limit":3`,
				`"platform":"chat_gpt"`,
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
			for _, notWant := range test.notWant {
				if strings.Contains(actualRequest, notWant) {
					t.Fatalf("%s request body must not contain %s: %s", test.id, notWant, actualRequest)
				}
			}
			if strings.Count(actualRequest, `"temperature"`) > 1 {
				t.Fatalf("%s sent temperature more than once: %s", test.id, actualRequest)
			}
		})
	}
}

func TestAIOptimizationUpstreamErrorsAreMapped(t *testing.T) {
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
			name:       "invalid field 40501",
			status:     http.StatusOK,
			body:       `{"status_code":20000,"cost":0,"tasks":[{"status_code":40501,"status_message":"secret field detail"}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantLog:    "upstream_error_mapped",
			wantCalls:  1,
		},
		{
			name:       "balance 40200",
			status:     http.StatusOK,
			body:       `{"status_code":20000,"cost":0,"tasks":[{"status_code":40200,"status_message":"secret balance detail"}]}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_balance_unavailable",
			wantCalls:  1,
		},
		{
			name:       "http 500",
			status:     http.StatusInternalServerError,
			body:       `{"status_code":50000,"status_message":"secret provider detail"}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_provider_failure",
			wantCalls:  1,
		},
	}
	for _, capability := range aiOptimizationCapabilities() {
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

func TestAIOptimizationRequiredInputsAreValidated(t *testing.T) {
	setDataForSEOAuth(t)

	for _, capability := range aiOptimizationCapabilities() {
		m, ok := Get(capability.id)
		if !ok {
			t.Fatalf("%s manifest is missing", capability.id)
		}
		required := m.Request.Required[0]
		inputs := map[string]map[string]any{
			"missing": {},
			"empty":   {required: emptyInputFor(m.Request.ArrayLengths, required)},
		}
		for name, input := range inputs {
			t.Run(capability.id+"/"+name+" "+required, func(t *testing.T) {
				execErr := ValidateSync(m, input)
				if execErr == nil || execErr.Code != "invalid_request" ||
					execErr.HTTPStatus != http.StatusBadRequest {
					t.Fatalf("%s expected invalid_request, got %#v", capability.id, execErr)
				}
			})
		}
	}
}

func emptyInputFor(arrayLengths map[string]RangeSpec, field string) any {
	if _, ok := arrayLengths[field]; ok {
		return []any{}
	}
	return "   "
}

func TestAIOptimizationInputConstraintsAreValidated(t *testing.T) {
	setDataForSEOAuth(t)

	tests := []struct {
		id    string
		input map[string]any
		valid bool
	}{
		{
			id:    "ai-answer-chatgpt-v1",
			input: map[string]any{"prompt": "hi", "temperature": 2},
			valid: true,
		},
		{
			id:    "ai-answer-chatgpt-v1",
			input: map[string]any{"prompt": "hi", "temperature": 2.1},
		},
		{
			id:    "ai-answer-claude-v1",
			input: map[string]any{"prompt": "hi", "temperature": 1},
			valid: true,
		},
		{
			id:    "ai-answer-claude-v1",
			input: map[string]any{"prompt": "hi", "temperature": 1.1},
		},
		{
			id:    "ai-answer-gemini-v1",
			input: map[string]any{"prompt": "hi", "temperature": 2},
			valid: true,
		},
		{
			id:    "ai-answer-perplexity-v1",
			input: map[string]any{"prompt": "hi", "temperature": 1.9},
			valid: true,
		},
		{
			id:    "ai-answer-perplexity-v1",
			input: map[string]any{"prompt": "hi", "temperature": 2},
		},
		{
			id:    "serp-google-ai-mode-v1",
			input: map[string]any{"keyword": "toys", "device": "mobile"},
			valid: true,
		},
		{
			id:    "serp-google-ai-mode-v1",
			input: map[string]any{"keyword": "toys", "device": "tablet"},
		},
		{
			id:    "llm-mentions-search-v1",
			input: map[string]any{"target": repeatedEntities(10)},
			valid: true,
		},
		{
			id:    "llm-mentions-search-v1",
			input: map[string]any{"target": repeatedEntities(11)},
		},
		{
			id:    "llm-mentions-search-v1",
			input: map[string]any{"target": repeatedEntities(1), "limit": 100},
			valid: true,
		},
		{
			id:    "llm-mentions-search-v1",
			input: map[string]any{"target": repeatedEntities(1), "limit": 101},
		},
		{
			id:    "llm-mentions-search-v1",
			input: map[string]any{"target": repeatedEntities(1), "platform": "bing"},
		},
		{
			id:    "llm-mentions-search-v1",
			input: map[string]any{"target": repeatedEntities(1), "platform": "google"},
			valid: true,
		},
		{
			id:    "llm-mentions-top-domains-v1",
			input: map[string]any{"target": repeatedEntities(1), "links_scope": "everything"},
		},
		{
			id:    "llm-mentions-top-domains-v1",
			input: map[string]any{"target": repeatedEntities(1), "items_list_limit": 11},
		},
		{
			id:    "llm-mentions-top-pages-v1",
			input: map[string]any{"target": repeatedEntities(1), "items_list_limit": 10, "links_scope": "search_results"},
			valid: true,
		},
		{
			id:    "ai-keyword-volume-v1",
			input: map[string]any{"keywords": repeatedKeywords(200)},
			valid: true,
		},
		{
			id:    "ai-keyword-volume-v1",
			input: map[string]any{"keywords": repeatedKeywords(201)},
		},
	}
	for _, test := range tests {
		m, ok := Get(test.id)
		if !ok {
			t.Fatalf("%s manifest is missing", test.id)
		}
		execErr := ValidateSync(m, test.input)
		if test.valid && execErr != nil {
			t.Fatalf("%s should accept the input: %v", test.id, execErr)
		}
		if !test.valid && (execErr == nil || execErr.Code != "invalid_request" ||
			execErr.HTTPStatus != http.StatusBadRequest) {
			t.Fatalf("%s expected invalid_request, got %#v", test.id, execErr)
		}
	}
}

func repeatedEntities(count int) []any {
	entities := make([]any, count)
	for i := range entities {
		entities[i] = map[string]any{"domain": "fixture-" + strings.Repeat("x", i+1) + ".example"}
	}
	return entities
}

func TestSerpGoogleDefaultRequestStaysUnchanged(t *testing.T) {
	allowLocalTestUpstreams(t)
	setDataForSEOAuth(t)

	wantDefault := strings.TrimSpace(string(readKeywordFixture(t, "serp-google-request.json")))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		requests = append(requests, strings.TrimSpace(string(raw)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readKeywordFixture(t, "serp-google.json"))
	}))
	defer server.Close()

	manifest := cloneKeywordManifest(t, "serp-google-v1", server.URL)
	if _, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"keyword": "running shoes"}, ""); execErr != nil {
		t.Fatalf("default call failed: %v", execErr)
	}
	if requests[0] != wantDefault {
		t.Fatalf("default request body changed:\nwant: %s\n got: %s", wantDefault, requests[0])
	}

	if _, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"keyword":                "running shoes",
		"load_async_ai_overview": true,
	}, ""); execErr != nil {
		t.Fatalf("async ai overview call failed: %v", execErr)
	}
	if !strings.Contains(requests[1], `"load_async_ai_overview":true`) {
		t.Fatalf("load_async_ai_overview was not forwarded: %s", requests[1])
	}

	if _, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
		"keyword":                "running shoes",
		"load_async_ai_overview": false,
	}, ""); execErr != nil {
		t.Fatalf("explicit false call failed: %v", execErr)
	}
	if !strings.Contains(requests[2], `"load_async_ai_overview":false`) {
		t.Fatalf("explicit false must be forwarded: %s", requests[2])
	}
}

func marshalTestOutput(t *testing.T, output map[string]any) string {
	t.Helper()
	raw, err := common.Marshal(output)
	if err != nil {
		t.Fatalf("encode output: %v", err)
	}
	return strings.TrimSpace(string(raw))
}
