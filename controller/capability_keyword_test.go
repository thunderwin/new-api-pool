package controller

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func cloneKeywordControllerManifest(t *testing.T, id string, sourceID string, endpoint string) *capability.Manifest {
	t.Helper()
	embedded, ok := capability.Get(sourceID)
	if !ok {
		t.Fatalf("%s manifest is missing", sourceID)
	}
	raw, err := common.Marshal(embedded)
	if err != nil {
		t.Fatalf("clone %s: %v", sourceID, err)
	}
	var manifest capability.Manifest
	if err := common.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("clone %s: %v", sourceID, err)
	}
	manifest.ID = id
	manifest.Endpoint = endpoint
	return &manifest
}

func TestKeywordCapabilityOverLimitFailsBeforeReserveOrUpstream(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":20000,"tasks":[{"status_code":20000,"result":[{"items":[]}]}]}`))
	}))
	defer upstream.Close()

	tests := []struct {
		name     string
		sourceID string
		testID   string
		count    int
	}{
		{name: "ideas over 20 seeds", sourceID: "keyword-ideas-v1", testID: "keyword-ideas-limit-test-v1", count: 21},
		{name: "volume over 200 keywords", sourceID: "keyword-volume-v1", testID: "keyword-volume-limit-test-v1", count: 201},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneKeywordControllerManifest(t, test.testID, test.sourceID, upstream.URL)
			registerWebUnlockTestManifest(t, manifest)

			keywords := make([]any, test.count)
			for i := range keywords {
				keywords[i] = "keyword " + strings.Repeat("x", i+1)
			}
			ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
				"input": map[string]any{"keywords": keywords},
			}, user, token)
			ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
			ExecuteCapability(ctx)

			var response struct {
				ErrorCode string `json:"error_code"`
			}
			if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusBadRequest || response.ErrorCode != "invalid_request" {
				t.Fatalf("expected pre-billing invalid_request: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatalf("over-limit input called upstream %d times", calls.Load())
			}
			var fresh model.User
			if err := db.First(&fresh, user.Id).Error; err != nil {
				t.Fatalf("load user: %v", err)
			}
			if fresh.Quota != user.Quota {
				t.Fatalf("over-limit input changed quota from %d to %d", user.Quota, fresh.Quota)
			}
		})
	}
}

func TestKeywordCapabilityHeaderBillingAndReserveFallback(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_AISA_API_KEY", "test-aisa-key")
	enableCapabilityConsumeLogs(t)

	tests := []struct {
		name        string
		sourceID    string
		testID      string
		costHeader  string
		response    string
		wantCostUSD float64
		wantSource  string
	}{
		{
			name:       "ideas actual header cost",
			sourceID:   "keyword-ideas-v1",
			testID:     "keyword-ideas-billing-test-v1",
			costHeader: "132000",
			response: `{"status_code":20000,"tasks":[{"status_code":20000,"result":[{"items":[
				{"keyword":"running shoes","keyword_info":{"search_volume":12100,"cpc":1.2,"competition_level":"HIGH"}}
			]}]}]}`,
			wantCostUSD: 0.132,
			wantSource:  "response_header",
		},
		{
			name:     "volume missing header uses reserve",
			sourceID: "keyword-volume-v1",
			testID:   "keyword-volume-billing-test-v1",
			response: `{"status_code":20000,"tasks":[{"status_code":20000,"result":[{"items":[
				{"keyword":"running shoes","search_volume":12100,"cpc":1.2,"competition":"HIGH"}
			]}]}]}`,
			wantCostUSD: 0.16,
			wantSource:  "reserve_fallback",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupCapabilityControllerTestDB(t)
			user, token := seedCapabilityUserAndToken(t, db)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "keyword-fixture-request")
				if test.costHeader != "" {
					w.Header().Set("X-AISA-Customer-Cost-Micros-USD", test.costHeader)
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer upstream.Close()

			manifest := cloneKeywordControllerManifest(t, test.testID, test.sourceID, upstream.URL)
			registerWebUnlockTestManifest(t, manifest)
			ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
				"input": map[string]any{"keywords": []any{"running shoes"}},
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
			if recorder.Code != http.StatusOK || !response.Success ||
				math.Abs(response.CostUSD-test.wantCostUSD) > 1e-12 {
				t.Fatalf("unexpected billing response: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var fresh model.User
			if err := db.First(&fresh, user.Id).Error; err != nil {
				t.Fatalf("load user: %v", err)
			}
			expectedQuota := user.Quota - int(math.Round(test.wantCostUSD*common.QuotaPerUnit))
			if fresh.Quota != expectedQuota {
				t.Fatalf("expected quota %d, got %d", expectedQuota, fresh.Quota)
			}
			log := latestCapabilityLog(t, db, manifest.ID)
			var other map[string]any
			if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
				t.Fatalf("decode consume log: %v", err)
			}
			if other["usage_source"] != test.wantSource ||
				strings.Contains(log.Other, "Authorization") ||
				strings.Contains(log.Other, "running shoes") {
				t.Fatalf("unexpected or sensitive consume log: %s", log.Other)
			}
		})
	}
}

func TestCapabilityDirectoryIncludesKeywordCapabilitiesAndExistingEntries(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ListCapabilities(ctx)

	var response struct {
		Data []struct {
			ID      string `json:"id"`
			Billing struct {
				MaxUSD float64 `json:"max_usd"`
			} `json:"billing"`
		} `json:"data"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode capability list: %v", err)
	}
	entries := make(map[string]float64, len(response.Data))
	for _, item := range response.Data {
		entries[item.ID] = item.Billing.MaxUSD
	}
	if len(entries) != 22 {
		t.Fatalf("expected 22 directory entries, got %d", len(entries))
	}
	if entries["keyword-ideas-v1"] != 0.27 || entries["keyword-volume-v1"] != 0.16 {
		t.Fatalf("new keyword capabilities have unexpected max_usd: %#v", entries)
	}
	for _, id := range []string{
		"weather-v1",
		"ssl-analyze-v1",
		"web-unlock-v1",
		"web-search-v1",
		"domain-rank-overview-v1",
		"backlinks-summary-v1",
		"keyword-gap-v1",
		"serp-google-v1",
	} {
		if _, ok := entries[id]; !ok {
			t.Fatalf("existing capability %s is missing from directory", id)
		}
	}
}
