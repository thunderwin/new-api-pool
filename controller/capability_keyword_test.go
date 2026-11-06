package controller

import (
	"encoding/base64"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const (
	controllerDataForSEOLogin    = "controller-dataforseo-login"
	controllerDataForSEOPassword = "controller-dataforseo-password"
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

func setControllerDataForSEOAuth(t *testing.T) {
	t.Helper()
	t.Setenv("CAPABILITY_DATAFORSEO_LOGIN", controllerDataForSEOLogin)
	t.Setenv("CAPABILITY_DATAFORSEO_PASSWORD", controllerDataForSEOPassword)
}

func controllerDataForSEOBasicHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(controllerDataForSEOLogin+":"+controllerDataForSEOPassword),
	)
}

func readControllerDataForSEOFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "capability", "testdata", "dataforseo", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func TestKeywordCapabilityOverLimitFailsBeforeReserveOrUpstream(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	setControllerDataForSEOAuth(t)
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readControllerDataForSEOFixture(t, "keyword-volume.json"))
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

func TestDataForSEOBasicAuthMissingCredentialFailsBeforeReserve(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	enableCapabilityConsumeLogs(t)

	tests := []struct {
		name     string
		login    string
		password string
	}{
		{name: "missing login", login: "", password: "present-password"},
		{name: "missing password", login: "present-login", password: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CAPABILITY_DATAFORSEO_LOGIN", test.login)
			t.Setenv("CAPABILITY_DATAFORSEO_PASSWORD", test.password)
			db := setupCapabilityControllerTestDB(t)
			user, token := seedCapabilityUserAndToken(t, db)

			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write(readControllerDataForSEOFixture(t, "keyword-volume.json"))
			}))
			defer upstream.Close()

			manifest := cloneKeywordControllerManifest(t, "keyword-volume-missing-auth-test-v1", "keyword-volume-v1", upstream.URL)
			registerWebUnlockTestManifest(t, manifest)
			ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
				"input": map[string]any{"keywords": []any{"running shoes"}},
			}, user, token)
			ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
			ExecuteCapability(ctx)

			if recorder.Code != http.StatusInternalServerError ||
				!strings.Contains(recorder.Body.String(), `"error_code":"credential_error"`) {
				t.Fatalf("expected credential_error: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatalf("missing credentials called upstream %d times", calls.Load())
			}
			var fresh model.User
			if err := db.First(&fresh, user.Id).Error; err != nil {
				t.Fatalf("load user: %v", err)
			}
			if fresh.Quota != user.Quota {
				t.Fatalf("missing credentials changed quota from %d to %d", user.Quota, fresh.Quota)
			}
			var logCount int64
			if err := db.Model(&model.Log{}).Where("model_name = ?", manifest.ID).Count(&logCount).Error; err != nil {
				t.Fatalf("count logs: %v", err)
			}
			if logCount != 0 {
				t.Fatalf("credential precheck wrote %d consume logs", logCount)
			}
			for _, sensitive := range []string{"present-password", "present-login", "Authorization"} {
				if strings.Contains(recorder.Body.String(), sensitive) {
					t.Fatalf("credential or auth header leaked in response: %s", recorder.Body.String())
				}
			}
		})
	}
}

func TestKeywordCapabilityCostPathBillingAndReserveFallback(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	setControllerDataForSEOAuth(t)
	enableCapabilityConsumeLogs(t)

	tests := []struct {
		name        string
		sourceID    string
		fixture     string
		wantCostUSD float64
		reserveUSD  float64
	}{
		{name: "ideas", sourceID: "keyword-ideas-v1", fixture: "keyword-ideas.json", wantCostUSD: 0.018, reserveUSD: 0.04},
		{name: "volume", sourceID: "keyword-volume-v1", fixture: "keyword-volume.json", wantCostUSD: 0.01212, reserveUSD: 0.04},
		{name: "serp", sourceID: "serp-google-v1", fixture: "serp-google.json", wantCostUSD: 0.002, reserveUSD: 0.21},
	}
	for _, test := range tests {
		for _, costMode := range []string{"actual", "missing", "invalid"} {
			name := test.name + " cost"
			if costMode == "missing" {
				name = test.name + " missing cost"
			} else if costMode == "invalid" {
				name = test.name + " invalid cost"
			}
			t.Run(name, func(t *testing.T) {
				db := setupCapabilityControllerTestDB(t)
				user, token := seedCapabilityUserAndToken(t, db)
				body := readControllerDataForSEOFixture(t, test.fixture)
				if costMode != "actual" {
					var parsed map[string]any
					if err := common.Unmarshal(body, &parsed); err != nil {
						t.Fatalf("decode fixture: %v", err)
					}
					if costMode == "invalid" {
						parsed["cost"] = "not-a-number"
					} else {
						delete(parsed, "cost")
					}
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
				input := map[string]any{"keywords": []any{"running shoes"}}
				if test.sourceID == "serp-google-v1" {
					input = map[string]any{"keyword": "running shoes"}
				}
				ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
					"input": input,
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
				wantCostUSD := test.wantCostUSD
				wantSource := "response_body"
				if costMode != "actual" {
					wantCostUSD = test.reserveUSD
					wantSource = "reserve_fallback"
				}
				if recorder.Code != http.StatusOK || !response.Success ||
					math.Abs(response.CostUSD-wantCostUSD) > 1e-12 {
					t.Fatalf("unexpected billing response: code=%d body=%s", recorder.Code, recorder.Body.String())
				}
				var fresh model.User
				if err := db.First(&fresh, user.Id).Error; err != nil {
					t.Fatalf("load user: %v", err)
				}
				expectedQuota := user.Quota - int(math.Round(wantCostUSD*common.QuotaPerUnit))
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
				} {
					if strings.Contains(log.Other, sensitive) || strings.Contains(recorder.Body.String(), sensitive) {
						t.Fatalf("credential or auth header leaked: %s", log.Other)
					}
				}
			})
		}
	}
}

func TestDataForSEOUpstreamErrorIsMappedWithoutLeakingCredentials(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	setControllerDataForSEOAuth(t)
	enableCapabilityConsumeLogs(t)
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":40210,"status_message":"secret balance detail","cost":0,"tasks":[{"status_code":40210,"status_message":"secret balance detail"}]}`))
	}))
	defer upstream.Close()

	manifest := cloneKeywordControllerManifest(t, "keyword-volume-error-test-v1", "keyword-volume-v1", upstream.URL)
	registerWebUnlockTestManifest(t, manifest)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
		"input": map[string]any{"keywords": []any{"running shoes"}},
	}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), `"error_code":"upstream_unavailable"`) {
		t.Fatalf("unexpected mapped error: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	log := latestCapabilityLog(t, db, manifest.ID)
	if !strings.Contains(log.Other, `"upstream_error_code":"upstream_balance_unavailable"`) {
		t.Fatalf("unexpected consume log: %s", log.Other)
	}
	for _, sensitive := range []string{
		"secret balance detail",
		controllerDataForSEOLogin,
		controllerDataForSEOPassword,
		controllerDataForSEOBasicHeader(),
		"Authorization",
	} {
		if strings.Contains(recorder.Body.String(), sensitive) || strings.Contains(log.Other, sensitive) {
			t.Fatalf("sensitive upstream detail or credential leaked: response=%s log=%s", recorder.Body.String(), log.Other)
		}
	}
}

func TestCapabilityDirectoryIncludesDataForSEOKeywordCapabilitiesAndExistingEntries(t *testing.T) {
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
	if entries["keyword-ideas-v1"] != 0.04 ||
		entries["keyword-volume-v1"] != 0.04 ||
		entries["serp-google-v1"] != 0.21 {
		t.Fatalf("keyword capabilities have unexpected max_usd: %#v", entries)
	}
	for _, id := range []string{
		"weather-v1",
		"ssl-analyze-v1",
		"web-unlock-v1",
		"web-search-v1",
		"domain-rank-overview-v1",
		"backlinks-summary-v1",
		"keyword-gap-v1",
	} {
		if _, ok := entries[id]; !ok {
			t.Fatalf("existing capability %s is missing from directory", id)
		}
	}
}
