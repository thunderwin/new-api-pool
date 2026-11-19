package controller

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const nativeCapabilityBodyMarker = "native-capability-body-marker-must-not-be-logged"

// TestExecuteCapabilityNativeHandlerIsFreeAndLogsNoBody covers the shared
// capability surface for a Go-backed (native) Manifest: zero price, success
// envelope and a consume log without any output body.
func TestExecuteCapabilityNativeHandlerIsFreeAndLogsNoBody(t *testing.T) {
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	oldLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = oldLogConsume })

	const handlerName = "test-native-audit"
	if err := capability.RegisterNativeSync(handlerName, capability.NativeSyncSpec{
		Run: func(ctx context.Context, m *capability.Manifest, input map[string]any) (*capability.SyncResult, *capability.ExecutionError) {
			output := map[string]any{
				"site":   "geo.test",
				"checks": []any{map[string]any{"id": "page_title", "status": "pass"}},
				"body":   nativeCapabilityBodyMarker,
			}
			return &capability.SyncResult{Output: output, Raw: output, HTTPStatus: http.StatusOK}, nil
		},
	}); err != nil {
		t.Fatalf("failed to register native handler: %v", err)
	}

	manifest := &capability.Manifest{
		ID:       "test-native-audit-v1",
		Provider: "native",
		Mode:     capability.ModeSync,
		Native:   handlerName,
		Billing:  capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0},
		Limits:   capability.LimitsSpec{Timeout: "10s", MaxResponseBytes: 65536},
	}
	if err := capability.Register(manifest); err != nil {
		t.Fatalf("failed to register manifest: %v", err)
	}
	defer capability.Unregister(manifest.ID)

	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID,
		map[string]any{"input": map[string]any{"url": "https://geo.test/"}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	var response struct {
		Success    bool           `json:"success"`
		Capability string         `json:"capability"`
		Output     map[string]any `json:"output"`
		CostUSD    float64        `json:"cost_usd"`
		Usage      struct {
			Unit     string  `json:"unit"`
			Quantity float64 `json:"quantity"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success || response.Capability != manifest.ID || response.CostUSD != 0 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.Usage.Unit != "call" || response.Usage.Quantity != 1 {
		t.Fatalf("unexpected usage: %#v", response.Usage)
	}
	if response.Output["site"] != "geo.test" {
		t.Fatalf("unexpected output: %#v", response.Output)
	}

	var logs []model.Log
	if err := db.Where("model_name = ?", manifest.ID).Find(&logs).Error; err != nil {
		t.Fatalf("failed to read consume logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected exactly one consume log, got %d", len(logs))
	}
	entry := logs[0]
	if entry.Quota != 0 {
		t.Fatalf("free native capability must log zero quota, got %d", entry.Quota)
	}
	if entry.Type != model.LogTypeConsume || !strings.Contains(entry.Content, "capability "+manifest.ID+" succeeded") {
		t.Fatalf("unexpected log entry: %#v", entry)
	}
	serialized := entry.Content + entry.Other
	if strings.Contains(serialized, nativeCapabilityBodyMarker) {
		t.Fatalf("consume log leaked capability output: %s", serialized)
	}
	if !strings.Contains(entry.Other, `"usage_source":"call"`) {
		t.Fatalf("consume log is missing the usage source: %s", entry.Other)
	}

	var reloaded model.User
	if err := db.Where("id = ?", user.Id).First(&reloaded).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if reloaded.Quota != user.Quota {
		t.Fatalf("free capability must not change user quota: before=%d after=%d", user.Quota, reloaded.Quota)
	}
}

func TestExecuteCapabilityNativeValidationRunsBeforeBilling(t *testing.T) {
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	const handlerName = "test-native-validated"
	if err := capability.RegisterNativeSync(handlerName, capability.NativeSyncSpec{
		Validate: func(m *capability.Manifest, input map[string]any) *capability.ExecutionError {
			if _, ok := input["url"]; !ok {
				return &capability.ExecutionError{
					Code:       "invalid_request",
					Message:    "input.url is required",
					HTTPStatus: http.StatusBadRequest,
				}
			}
			return nil
		},
		Run: func(ctx context.Context, m *capability.Manifest, input map[string]any) (*capability.SyncResult, *capability.ExecutionError) {
			t.Fatal("handler must not run when validation fails")
			return nil, nil
		},
	}); err != nil {
		t.Fatalf("failed to register native handler: %v", err)
	}

	manifest := &capability.Manifest{
		ID:       "test-native-validated-v1",
		Provider: "native",
		Mode:     capability.ModeSync,
		Native:   handlerName,
		Billing:  capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0},
		Limits:   capability.LimitsSpec{Timeout: "10s", MaxResponseBytes: 65536},
	}
	if err := capability.Register(manifest); err != nil {
		t.Fatalf("failed to register manifest: %v", err)
	}
	defer capability.Unregister(manifest.ID)

	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID,
		map[string]any{"input": map[string]any{}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	var response struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"error_code"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Success || response.ErrorCode != "invalid_request" {
		t.Fatalf("unexpected response: %#v (status %d)", response, recorder.Code)
	}
}
