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

const controllerRelaySelfKey = "controller-relay-self-key"

func cloneRelayAIAnswerControllerManifest(t *testing.T, id string, sourceID string, endpoint string) *capability.Manifest {
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

// TestRelayAIAnswerBillsTotalTokens covers the per_unit contract of the three
// relay-backed answer capabilities: the billed quantity is
// usage.total_tokens and a missing usage falls back to the manifest reserve.
func TestRelayAIAnswerBillsTotalTokens(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_RELAY_SELF_KEY", controllerRelaySelfKey)
	enableCapabilityConsumeLogs(t)

	tests := []struct {
		sourceID   string
		model      string
		priceUSD   float64
		reserveUSD float64
	}{
		{sourceID: "ai-answer-grok-v1", model: "grok-4.7", priceUSD: 2.5e-7, reserveUSD: 0.10},
		{sourceID: "ai-answer-kimi-v1", model: "kimi-k3", priceUSD: 8e-7, reserveUSD: 0.03},
		{sourceID: "ai-answer-doubao-v1", model: "doubao-seed-2.1-pro", priceUSD: 5e-7, reserveUSD: 0.03},
	}
	for _, test := range tests {
		for _, usageMode := range []string{"present", "missing"} {
			t.Run(test.sourceID+"/"+usageMode+" usage", func(t *testing.T) {
				db := setupCapabilityControllerTestDB(t)
				user, token := seedCapabilityUserAndToken(t, db)

				body := `{"status":"completed","model":"` + test.model + `",` +
					`"output":[{"type":"web_search_call"},{"type":"message"}],` +
					`"output_text":"fixture answer"`
				if usageMode == "present" {
					body += `,"usage":{"input_tokens":39000,"output_tokens":1000,"total_tokens":40000}`
				}
				body += `}`

				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer "+controllerRelaySelfKey {
						t.Errorf("unexpected Authorization header")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Request-ID", "relay-answer-fixture")
					_, _ = w.Write([]byte(body))
				}))
				defer upstream.Close()

				manifest := cloneRelayAIAnswerControllerManifest(t, test.sourceID+"-billing-test-v1", test.sourceID, upstream.URL)
				registerWebUnlockTestManifest(t, manifest)
				ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
					"input": map[string]any{"prompt": "best wooden toy set"},
				}, user, token)
				ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
				ExecuteCapability(ctx)

				var response struct {
					Success           bool           `json:"success"`
					CostUSD           float64        `json:"cost_usd"`
					ErrorCode         string         `json:"error_code"`
					ProviderRequestID string         `json:"provider_request_id"`
					Output            map[string]any `json:"output"`
					Usage             struct {
						Unit     string  `json:"unit"`
						Quantity float64 `json:"quantity"`
					} `json:"usage"`
				}
				if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}

				wantUSD := test.priceUSD * 40000
				wantQuantity := 40000.0
				wantSource := "response_body"
				if usageMode == "missing" {
					wantUSD = test.reserveUSD
					wantQuantity = test.reserveUSD / test.priceUSD
					wantSource = "reserve_fallback"
				}
				wantQuota := int(math.Round(wantUSD * common.QuotaPerUnit))
				wantCostUSD := float64(wantQuota) / common.QuotaPerUnit
				if recorder.Code != http.StatusOK || !response.Success ||
					math.Abs(response.CostUSD-wantCostUSD) > 1e-12 ||
					response.Usage.Unit != "token" ||
					math.Abs(response.Usage.Quantity-wantQuantity) > 1e-6 ||
					response.ProviderRequestID != "relay-answer-fixture" {
					t.Fatalf("unexpected billing response: code=%d body=%s", recorder.Code, recorder.Body.String())
				}

				var fresh model.User
				if err := db.First(&fresh, user.Id).Error; err != nil {
					t.Fatalf("load user: %v", err)
				}
				if fresh.Quota != user.Quota-wantQuota {
					t.Fatalf("expected quota %d, got %d", user.Quota-wantQuota, fresh.Quota)
				}

				log := latestCapabilityLog(t, db, manifest.ID)
				var other map[string]any
				if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
					t.Fatalf("decode consume log: %v", err)
				}
				if other["usage_source"] != wantSource {
					t.Fatalf("unexpected usage source: %#v", other)
				}
				for _, sensitive := range []string{controllerRelaySelfKey, "Authorization"} {
					if strings.Contains(recorder.Body.String(), sensitive) ||
						strings.Contains(log.Other, sensitive) {
						t.Fatalf("credential or auth header leaked: response=%s log=%s",
							recorder.Body.String(), log.Other)
					}
				}
			})
		}
	}
}

func TestRelayAIAnswerMissingCredentialFailsBeforeReserve(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_RELAY_SELF_KEY", "")
	enableCapabilityConsumeLogs(t)

	for _, sourceID := range []string{"ai-answer-grok-v1", "ai-answer-kimi-v1", "ai-answer-doubao-v1"} {
		t.Run(sourceID, func(t *testing.T) {
			db := setupCapabilityControllerTestDB(t)
			user, token := seedCapabilityUserAndToken(t, db)

			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(`{"status":"completed","output":[]}`))
			}))
			defer upstream.Close()

			manifest := cloneRelayAIAnswerControllerManifest(t, sourceID+"-missing-key-test-v1", sourceID, upstream.URL)
			registerWebUnlockTestManifest(t, manifest)
			ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
				"input": map[string]any{"prompt": "best wooden toy set"},
			}, user, token)
			ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
			ExecuteCapability(ctx)

			if recorder.Code != http.StatusInternalServerError ||
				!strings.Contains(recorder.Body.String(), `"error_code":"credential_error"`) {
				t.Fatalf("expected credential_error: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatalf("missing credential called upstream %d times", calls.Load())
			}
			var fresh model.User
			if err := db.First(&fresh, user.Id).Error; err != nil {
				t.Fatalf("load user: %v", err)
			}
			if fresh.Quota != user.Quota {
				t.Fatalf("missing credential changed quota from %d to %d", user.Quota, fresh.Quota)
			}
			var logCount int64
			if err := db.Model(&model.Log{}).Where("model_name = ?", manifest.ID).Count(&logCount).Error; err != nil {
				t.Fatalf("count logs: %v", err)
			}
			if logCount != 0 {
				t.Fatalf("credential precheck wrote %d consume logs", logCount)
			}
		})
	}
}
