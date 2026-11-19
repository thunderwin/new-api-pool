package controller

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// TestAIOptimizationCapabilitiesBillingMatchesUpstreamCost covers the
// per_unit `quantity_path` contract of the DataForSEO AI optimization
// capabilities: the response body `cost` is the billed amount, and a missing
// `cost` falls back to the manifest reserve.
func TestAIOptimizationCapabilitiesBillingMatchesUpstreamCost(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	setControllerDataForSEOAuth(t)
	enableCapabilityConsumeLogs(t)

	tests := []struct {
		sourceID string
		fixture  string
		input    map[string]any
		wantUSD  float64
		reserve  float64
	}{
		{
			sourceID: "ai-answer-chatgpt-v1",
			fixture:  "ai-answer-chatgpt.json",
			input:    map[string]any{"prompt": "best wooden toy set"},
			wantUSD:  0.036124,
			reserve:  0.20,
		},
		{
			sourceID: "ai-answer-claude-v1",
			fixture:  "ai-answer-claude.json",
			input:    map[string]any{"prompt": "best wooden toy set"},
			wantUSD:  0.0461,
			reserve:  0.20,
		},
		{
			sourceID: "ai-answer-gemini-v1",
			fixture:  "ai-answer-gemini.json",
			input:    map[string]any{"prompt": "best wooden toy set"},
			wantUSD:  0.021,
			reserve:  0.10,
		},
		{
			sourceID: "ai-answer-perplexity-v1",
			fixture:  "ai-answer-perplexity.json",
			input:    map[string]any{"prompt": "best wooden toy set"},
			wantUSD:  0.012381,
			reserve:  0.10,
		},
		{
			sourceID: "ai-answer-scrape-chatgpt-v1",
			fixture:  "ai-answer-scrape-chatgpt.json",
			input:    map[string]any{"keyword": "best wooden toy set"},
			wantUSD:  0.004,
			reserve:  0.01,
		},
		{
			sourceID: "ai-answer-scrape-gemini-v1",
			fixture:  "ai-answer-scrape-gemini.json",
			input:    map[string]any{"keyword": "best wooden toy set"},
			wantUSD:  0.004,
			reserve:  0.01,
		},
		{
			sourceID: "serp-google-ai-mode-v1",
			fixture:  "serp-google-ai-mode.json",
			input:    map[string]any{"keyword": "best wooden toy set"},
			wantUSD:  0.004,
			reserve:  0.01,
		},
		{
			sourceID: "llm-mentions-search-v1",
			fixture:  "llm-mentions-search.json",
			input:    map[string]any{"target": []any{map[string]any{"domain": "fixture-brand.example"}}},
			wantUSD:  0.103,
			reserve:  0.25,
		},
		{
			sourceID: "llm-mentions-top-domains-v1",
			fixture:  "llm-mentions-top-domains.json",
			input:    map[string]any{"target": []any{map[string]any{"keyword": "wooden toy set"}}},
			wantUSD:  0.101,
			reserve:  0.25,
		},
		{
			sourceID: "llm-mentions-top-pages-v1",
			fixture:  "llm-mentions-top-pages.json",
			input:    map[string]any{"target": []any{map[string]any{"keyword": "wooden toy set"}}},
			wantUSD:  0.101,
			reserve:  0.25,
		},
		{
			sourceID: "ai-keyword-volume-v1",
			fixture:  "ai-keyword-volume.json",
			input:    map[string]any{"keywords": []any{"wooden toy set"}},
			wantUSD:  0.0102,
			reserve:  0.05,
		},
	}

	for _, test := range tests {
		for _, costMode := range []string{"actual", "missing"} {
			name := test.sourceID + " " + costMode + " cost"
			t.Run(name, func(t *testing.T) {
				db := setupCapabilityControllerTestDB(t)
				user, token := seedCapabilityUserAndToken(t, db)

				body := readControllerDataForSEOFixture(t, test.fixture)
				if costMode == "missing" {
					var parsed map[string]any
					if err := common.Unmarshal(body, &parsed); err != nil {
						t.Fatalf("decode fixture: %v", err)
					}
					delete(parsed, "cost")
					body, _ = common.Marshal(parsed)
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != controllerDataForSEOBasicHeader() {
						t.Errorf("basic auth header is incorrect")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}))
				defer upstream.Close()

				manifest := cloneKeywordControllerManifest(t, test.sourceID+"-billing-test-v1", test.sourceID, upstream.URL)
				registerWebUnlockTestManifest(t, manifest)
				ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
					"input": test.input,
				}, user, token)
				ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
				ExecuteCapability(ctx)

				var response struct {
					Success   bool    `json:"success"`
					CostUSD   float64 `json:"cost_usd"`
					ErrorCode string  `json:"error_code"`
				}
				if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				wantUSD := test.wantUSD
				wantSource := "response_body"
				if costMode == "missing" {
					wantUSD = test.reserve
					wantSource = "reserve_fallback"
				}
				wantQuota := int(math.Round(wantUSD * common.QuotaPerUnit))
				wantCostUSD := float64(wantQuota) / common.QuotaPerUnit
				if recorder.Code != http.StatusOK || !response.Success ||
					math.Abs(response.CostUSD-wantCostUSD) > 1e-12 {
					t.Fatalf("unexpected billing response: code=%d body=%s", recorder.Code, recorder.Body.String())
				}

				var fresh model.User
				if err := db.First(&fresh, user.Id).Error; err != nil {
					t.Fatalf("load user: %v", err)
				}
				expectedQuota := user.Quota - wantQuota
				if fresh.Quota != expectedQuota {
					t.Fatalf("expected quota %d, got %d", expectedQuota, fresh.Quota)
				}

				log := latestCapabilityLog(t, db, manifest.ID)
				var other map[string]any
				if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
					t.Fatalf("decode consume log: %v", err)
				}
				if other["usage_source"] != wantSource {
					t.Fatalf("unexpected usage source: %#v", other)
				}
				for _, sensitive := range []string{
					controllerDataForSEOLogin,
					controllerDataForSEOPassword,
					controllerDataForSEOBasicHeader(),
					"Authorization",
					"fixture-task",
				} {
					if strings.Contains(recorder.Body.String(), sensitive) ||
						strings.Contains(log.Other, sensitive) {
						t.Fatalf("sensitive upstream detail or credential leaked: response=%s log=%s",
							recorder.Body.String(), log.Other)
					}
				}
			})
		}
	}
}
