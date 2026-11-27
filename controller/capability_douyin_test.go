package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func TestDouyinSearchSettlement(t *testing.T) {
	const secret = "synthetic-credential-do-not-echo"
	t.Setenv("CAPABILITY_TIKHUB_API_KEY", secret)
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	enableCapabilityConsumeLogs(t)
	fixture, err := os.ReadFile("../capability/testdata/tikhub-douyin-search-success.json")
	if err != nil {
		t.Fatal(err)
	}
	embedded, ok := capability.Get("douyin-search-v1")
	if !ok || embedded.Billing.Type != capability.BillingPerCall || embedded.Billing.PriceUSD != 0.01 {
		t.Fatal("missing Douyin search per-call pricing")
	}
	for _, tc := range []struct {
		name, body, wantError string
		status, wantStatus    int
		cost                  float64
	}{
		{"success", string(fixture), "", 200, 200, 0.01},
		{"empty", `{"code":200,"data":{"business_data":[],"cursor":0,"has_more":0}}`, "", 200, 200, 0},
		{"only-cards", `{"code":200,"data":{"business_data":[{"data":{"card":true}}],"cursor":4,"has_more":1}}`, "", 200, 200, 0},
		{"rejected", `{"message":"` + secret + `"}`, "invalid_request", 422, 400, 0},
		{"failed", `{"message":"` + secret + `"}`, "upstream_unavailable", 500, 503, 0},
		{"business-failed", `{"code":500,"message":"` + secret + `"}`, "upstream_unavailable", 200, 503, 0},
		{"deadline", "", "upstream_timeout", 0, 504, 0},
		{"oversize", strings.Repeat("x", 4097), "upstream_response_too_large", 200, 502, 0},
		{"missing", `{"code":200,"data":{"cursor":0,"has_more":0}}`, "missing_output_field", 200, 502, 0},
		{"invalid-keyword", "", "invalid_request", 0, 400, 0},
		{"missing-key", "", "credential_error", 0, 500, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupCapabilityControllerTestDB(t)
			user, token := seedCapabilityUserAndToken(t, db)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Request-ID", "synthetic-douyin-request")
				if tc.status == 0 {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer upstream.Close()
			m := *embedded
			m.ID, m.Endpoint = "mock-douyin-search-v1", upstream.URL
			m.Limits.Timeout, m.Limits.MaxResponseBytes = "1s", 4096
			if tc.name == "deadline" {
				m.Limits.Timeout = "100ms"
			}
			registerWebUnlockTestManifest(t, &m)
			keyword := "合成关键词"
			wantCalls := int32(1)
			if tc.name == "invalid-keyword" {
				keyword = strings.Repeat("测", 51)
				wantCalls = 0
			}
			if tc.name == "missing-key" {
				t.Setenv("CAPABILITY_TIKHUB_API_KEY", "")
				wantCalls = 0
			}
			ctx, rec := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+m.ID, gin.H{"input": gin.H{"keyword": keyword}}, user, token)
			ctx.Params = gin.Params{{Key: "capability", Value: m.ID}}
			ExecuteCapability(ctx)
			if rec.Code != tc.wantStatus || calls.Load() != wantCalls || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("unexpected status, call count, cache policy or secret echo: status=%d calls=%d", rec.Code, calls.Load())
			}
			var response map[string]any
			if err := common.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" {
				if response["error_code"] != tc.wantError {
					t.Fatalf("wanted %s, got %v", tc.wantError, response["error_code"])
				}
			} else if response["cost_usd"] != tc.cost {
				t.Fatalf("wanted cost %v, got %v", tc.cost, response["cost_usd"])
			}
			if tc.name == "only-cards" {
				output, ok := response["output"].(map[string]any)
				if !ok || output["search_id"] != "" || output["backtrace"] != "" || output["has_more"] != float64(1) {
					t.Fatalf("unexpected pagination for missing tokens: %#v", output)
				}
			}
			wantQuota := int(tc.cost * common.QuotaPerUnit)
			if wantCalls != 0 {
				log := latestCapabilityLog(t, db, m.ID)
				if log.Quota != wantQuota || strings.Contains(log.Other, secret) || strings.Contains(log.Content, secret) {
					t.Fatal("wrong quota or credential leaked into consumption log")
				}
				if tc.wantError == "" && tc.cost == 0 && !strings.Contains(log.Other, `"usage_source":"empty_response"`) {
					t.Fatal("empty success was not accounted as such")
				}
			}
			waitForCapabilityQuota(t, db, user.Id, user.Quota-wantQuota)
		})
	}
}
