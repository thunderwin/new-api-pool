package controller

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupCapabilityControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTokenControllerTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.Task{}, &model.CapabilityIdempotency{}, &model.Log{}); err != nil {
		t.Fatalf("failed to migrate capability tables: %v", err)
	}
	return db
}

func seedCapabilityUserAndToken(t *testing.T, db *gorm.DB) (*model.User, *model.Token) {
	t.Helper()
	user := &model.User{
		Id:       1,
		Username: "cap-user",
		Password: "password123",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    5000000,
		AffCode:  "aff-cap-user",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	token := &model.Token{
		UserId:         user.Id,
		Name:           "cap-token",
		Key:            "captest1234567890captest1234567890",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("failed to seed token: %v", err)
	}
	return user, token
}

func newCapabilityContext(t *testing.T, method string, path string, body any, user *model.User, token *model.Token) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, method, path, body, user.Id)
	ctx.Set("token_id", token.Id)
	ctx.Set("token_key", token.Key)
	ctx.Set("token_name", token.Name)
	ctx.Set("token_unlimited_quota", true)
	ctx.Set("username", user.Username)
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(ctx, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(ctx, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
	return ctx, recorder
}

func TestExecuteCapabilitySync(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"hello"}`))
	}))
	defer upstream.Close()

	m := &capability.Manifest{
		ID:       "test-sync-api-v1",
		Provider: "test",
		Mode:     capability.ModeSync,
		Endpoint: upstream.URL,
		Method:   http.MethodGet,
		Response: &capability.ResponseSpec{Output: map[string]string{"value": "$.value"}},
		Billing:  capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0},
		Limits:   capability.LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	if err := capability.Register(m); err != nil {
		t.Fatal(err)
	}
	defer capability.Unregister(m.ID)

	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+m.ID, map[string]any{"input": map[string]any{}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: m.ID}}
	ExecuteCapability(ctx)

	var response struct {
		Success    bool           `json:"success"`
		Capability string         `json:"capability"`
		Output     map[string]any `json:"output"`
		CostUSD    float64        `json:"cost_usd"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success || response.Capability != m.ID || response.Output["value"] != "hello" || response.CostUSD != 0 {
		t.Fatalf("unexpected capability response: %#v", response)
	}
}

func TestExecuteCapabilityChargesPerCall(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	originalQuota := user.Quota

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	m := &capability.Manifest{
		ID:       "test-paid-api-v1",
		Provider: "test",
		Mode:     capability.ModeSync,
		Endpoint: upstream.URL,
		Method:   http.MethodGet,
		Response: &capability.ResponseSpec{Output: map[string]string{"ok": "$.ok"}},
		Billing:  capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0.0001},
		Limits:   capability.LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	if err := capability.Register(m); err != nil {
		t.Fatal(err)
	}
	defer capability.Unregister(m.ID)

	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+m.ID, map[string]any{"input": map[string]any{}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: m.ID}}
	ExecuteCapability(ctx)

	var response struct {
		Success   bool    `json:"success"`
		CostUSD   float64 `json:"cost_usd"`
		Message   string  `json:"message"`
		ErrorCode string  `json:"error_code"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success || response.CostUSD != 0.0001 {
		t.Fatalf("unexpected paid capability response: %#v body=%s", response, recorder.Body.String())
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	expectedQuota := originalQuota - int(0.0001*float64(common.QuotaPerUnit))
	if fresh.Quota != expectedQuota {
		t.Fatalf("expected quota %d after charge, got %d", expectedQuota, fresh.Quota)
	}
}

func TestSubmitAndPollCapabilityTaskIdempotently(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/submit":
			_, _ = w.Write([]byte(`{"job":"job-1","status":"QUEUED"}`))
		case "/poll":
			_, _ = w.Write([]byte(`{"status":"READY","result":{"value":"done"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	m := &capability.Manifest{
		ID:       "test-async-api-v1",
		Provider: "test",
		Mode:     capability.ModeAsync,
		Submit: &capability.SubmitSpec{
			Endpoint:  upstream.URL + "/submit",
			Method:    http.MethodGet,
			JobIDPath: "$.job",
		},
		Poll: &capability.PollSpec{
			Endpoint:      upstream.URL + "/poll",
			Method:        http.MethodGet,
			StatusPath:    "$.status",
			SuccessValues: []string{"READY"},
			FailureValues: []string{"ERROR"},
			ResultPath:    "$.result",
		},
		Billing: capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0},
		Limits:  capability.LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	if err := capability.Register(m); err != nil {
		t.Fatal(err)
	}
	defer capability.Unregister(m.ID)

	submitBody := map[string]any{
		"capability":      m.ID,
		"input":           map[string]any{},
		"idempotency_key": "cap-idem-1",
	}
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/tasks", submitBody, user, token)
	SubmitCapabilityTask(ctx)
	var first struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
		Status  string `json:"status"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("failed to decode submit response: %v", err)
	}
	if !first.Success || !strings.HasPrefix(first.ID, "task_") || first.Status != "queued" {
		t.Fatalf("unexpected submit response: %#v", first)
	}

	ctx2, recorder2 := newCapabilityContext(t, http.MethodPost, "/v1/tasks", submitBody, user, token)
	SubmitCapabilityTask(ctx2)
	var second struct {
		Success  bool   `json:"success"`
		ID       string `json:"id"`
		Replayed bool   `json:"replayed"`
	}
	if err := common.Unmarshal(recorder2.Body.Bytes(), &second); err != nil {
		t.Fatalf("failed to decode replay response: %v", err)
	}
	if !second.Success || second.ID != first.ID || !second.Replayed {
		t.Fatalf("expected idempotent task replay, got %#v", second)
	}

	ctx3, recorder3 := newCapabilityContext(t, http.MethodGet, "/v1/tasks/"+first.ID, nil, user, token)
	ctx3.Params = gin.Params{{Key: "id", Value: first.ID}}
	GetCapabilityTask(ctx3)
	var polled struct {
		Success bool           `json:"success"`
		ID      string         `json:"id"`
		Status  string         `json:"status"`
		Output  map[string]any `json:"output"`
	}
	if err := common.Unmarshal(recorder3.Body.Bytes(), &polled); err != nil {
		t.Fatalf("failed to decode poll response: %v", err)
	}
	if !polled.Success || polled.ID != first.ID || polled.Status != "succeeded" || polled.Output["value"] != "done" {
		t.Fatalf("unexpected poll response: %#v", polled)
	}
}

func TestAsyncCapabilityPerUnitSettlementAndContent(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	originalQuota := user.Quota

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/submit":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"job":"job-pages","status":"QUEUED"}`))
		case "/poll":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"READY","pages":3,"result":{"ok":true}}`))
		case "/content":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("file-content"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	m := &capability.Manifest{
		ID:       "test-async-pages-v1",
		Provider: "test",
		Mode:     capability.ModeAsync,
		Submit: &capability.SubmitSpec{
			Endpoint:  upstream.URL + "/submit",
			Method:    http.MethodGet,
			JobIDPath: "$.job",
		},
		Poll: &capability.PollSpec{
			Endpoint:      upstream.URL + "/poll",
			Method:        http.MethodGet,
			StatusPath:    "$.status",
			SuccessValues: []string{"READY"},
			FailureValues: []string{"ERROR"},
			ResultPath:    "$.result",
		},
		Content: &capability.ContentSpec{Endpoint: upstream.URL + "/content", Method: http.MethodGet},
		Billing: capability.BillingSpec{
			Type:         capability.BillingPerUnit,
			Unit:         "page",
			PriceUSD:     0.001,
			QuantityPath: "$.pages",
			EstimatePath: "$.input.requested_pages",
		},
		Limits: capability.LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
	}
	if err := capability.Register(m); err != nil {
		t.Fatal(err)
	}
	defer capability.Unregister(m.ID)

	submitBody := map[string]any{
		"capability": m.ID,
		"input":      map[string]any{"requested_pages": 1},
	}
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/tasks", submitBody, user, token)
	SubmitCapabilityTask(ctx)
	var submitted struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("failed to decode submit response: %v", err)
	}
	if !submitted.Success || submitted.ID == "" {
		t.Fatalf("submit failed: %s", recorder.Body.String())
	}

	pollCtx, pollRecorder := newCapabilityContext(t, http.MethodGet, "/v1/tasks/"+submitted.ID, nil, user, token)
	pollCtx.Params = gin.Params{{Key: "id", Value: submitted.ID}}
	GetCapabilityTask(pollCtx)
	var polled struct {
		Success bool           `json:"success"`
		ID      string         `json:"id"`
		Status  string         `json:"status"`
		Output  map[string]any `json:"output"`
		CostUSD float64        `json:"cost_usd"`
	}
	if err := common.Unmarshal(pollRecorder.Body.Bytes(), &polled); err != nil {
		t.Fatalf("failed to decode poll response: %v", err)
	}
	if !polled.Success || polled.Status != "succeeded" || polled.CostUSD != 0.003 {
		t.Fatalf("unexpected per-unit poll response: %#v body=%s", polled, pollRecorder.Body.String())
	}

	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	expectedQuota := originalQuota - int(0.003*float64(common.QuotaPerUnit))
	if fresh.Quota != expectedQuota {
		t.Fatalf("expected quota %d after per-unit settlement, got %d", expectedQuota, fresh.Quota)
	}

	contentCtx, contentRecorder := newCapabilityContext(t, http.MethodGet, "/v1/tasks/"+submitted.ID+"/content", nil, user, token)
	contentCtx.Params = gin.Params{{Key: "id", Value: submitted.ID}}
	GetCapabilityTaskContent(contentCtx)
	if contentRecorder.Code != http.StatusOK || contentRecorder.Body.String() != "file-content" {
		t.Fatalf("unexpected content response: code=%d body=%q", contentRecorder.Code, contentRecorder.Body.String())
	}
	if !strings.HasPrefix(contentRecorder.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("unexpected content type: %s", contentRecorder.Header().Get("Content-Type"))
	}
}

func newWebUnlockTestManifest(endpoint string) *capability.Manifest {
	return &capability.Manifest{
		ID:       "test-web-unlock-api-v1",
		Provider: "test",
		Mode:     capability.ModeSync,
		Endpoint: endpoint,
		Method:   http.MethodPost,
		Request: &capability.RequestSpec{
			Required: []string{"url"},
			URLInputs: map[string]capability.URLInputSpec{
				"url": {AllowedHosts: []string{"wayfair.com"}},
			},
			Body: map[string]string{
				"url":    "$.input.url",
				"zone":   "$.input.zone",
				"format": "$.input.format",
			},
			Consts: map[string]string{"format": "json"},
			Env:    map[string]string{"zone": "CAPABILITY_TEST_ZONE"},
		},
		Response: &capability.ResponseSpec{
			Success: &capability.SuccessSpec{
				Path:        "$.status_code",
				Values:      []any{float64(200)},
				NonEmpty:    "$.body",
				FailureCode: "upstream_target_status",
				EmptyCode:   "upstream_empty_body",
			},
			Output: map[string]string{
				"status": "$.status_code",
				"html":   "$.body",
			},
		},
		Billing: capability.BillingSpec{
			Type:     capability.BillingPerCall,
			Unit:     "call",
			PriceUSD: 0.003,
		},
		Limits: capability.LimitsSpec{Timeout: "5s", MaxResponseBytes: 8 * 1024 * 1024},
		Retry:  capability.RetrySpec{Attempts: 2, RetryOn: []int{429, 502, 503, 504}},
	}
}

func registerWebUnlockTestManifest(t *testing.T, manifest *capability.Manifest) {
	t.Helper()
	if err := capability.Register(manifest); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { capability.Unregister(manifest.ID) })
}

func waitForCapabilityQuota(t *testing.T, db *gorm.DB, userID int, expected int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var user model.User
		if err := db.First(&user, userID).Error; err != nil {
			t.Fatalf("load user: %v", err)
		}
		if user.Quota == expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected quota %d, got %d", expected, user.Quota)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecuteWebUnlockChargesOnceOnTargetSuccess(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_TEST_ZONE", "wayfair_unlocker")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	originalQuota := user.Quota

	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if err := common.Unmarshal(raw, &receivedBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"headers":{},"body":"<html>wayfair</html>"}`))
	}))
	defer upstream.Close()

	manifest := newWebUnlockTestManifest(upstream.URL)
	registerWebUnlockTestManifest(t, manifest)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
		"input": map[string]any{
			"url":    "https://www.wayfair.com/product.html",
			"zone":   "user-zone",
			"format": "raw",
		},
	}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	var response struct {
		Success bool           `json:"success"`
		Output  map[string]any `json:"output"`
		CostUSD float64        `json:"cost_usd"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusOK || !response.Success || response.CostUSD != 0.003 ||
		response.Output["status"] != float64(200) || response.Output["html"] != "<html>wayfair</html>" {
		t.Fatalf("unexpected success response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if receivedBody["zone"] != "wayfair_unlocker" || receivedBody["format"] != "json" {
		t.Fatalf("constant body fields did not override input: %#v", receivedBody)
	}

	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	expectedQuota := originalQuota - int(0.003*float64(common.QuotaPerUnit))
	if fresh.Quota != expectedQuota {
		t.Fatalf("expected exactly one charge (%d), got %d", expectedQuota, fresh.Quota)
	}
}

func TestWebUnlockPreflightDoesNotCallOrCharge(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_TEST_ZONE", "wayfair_unlocker")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	originalQuota := user.Quota
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	manifest := newWebUnlockTestManifest(upstream.URL)
	registerWebUnlockTestManifest(t, manifest)
	tests := []struct {
		name  string
		input map[string]any
	}{
		{name: "missing URL", input: map[string]any{}},
		{name: "foreign host", input: map[string]any{"url": "https://example.com/product.html"}},
		{name: "lookalike host", input: map[string]any{"url": "https://wayfair.com.evil.com/product.html"}},
		{name: "userinfo", input: map[string]any{"url": "https://user@wayfair.com/product.html"}},
		{name: "IP literal", input: map[string]any{"url": "https://127.0.0.1/product.html"}},
		{name: "non-default port", input: map[string]any{"url": "https://www.wayfair.com:8443/product.html"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{"input": test.input}, user, token)
			ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
			ExecuteCapability(ctx)
			var response struct {
				ErrorCode string `json:"error_code"`
			}
			if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusBadRequest || response.ErrorCode != "invalid_request" {
				t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("preflight errors called upstream %d times", calls)
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if fresh.Quota != originalQuota {
		t.Fatalf("preflight changed quota from %d to %d", originalQuota, fresh.Quota)
	}
}

func TestWebUnlockMissingCredentialDoesNotCallOrCharge(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_TEST_ZONE", "wayfair_unlocker")
	t.Setenv("CAPABILITY_TEST_MISSING_SECRET", "")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	originalQuota := user.Quota
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	manifest := newWebUnlockTestManifest(upstream.URL)
	manifest.Auth = &capability.AuthSpec{Type: "bearer", SecretRef: "CAPABILITY_TEST_MISSING_SECRET"}
	registerWebUnlockTestManifest(t, manifest)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
		"input": map[string]any{"url": "https://www.wayfair.com/product.html"},
	}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	var response struct {
		ErrorCode string `json:"error_code"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError || response.ErrorCode != "credential_error" {
		t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if calls != 0 {
		t.Fatalf("missing credential called upstream %d times", calls)
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if fresh.Quota != originalQuota {
		t.Fatalf("missing credential changed quota from %d to %d", originalQuota, fresh.Quota)
	}
}

func TestWebUnlockMissingZoneDoesNotCallOrCharge(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	t.Setenv("CAPABILITY_TEST_ZONE", "")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	originalQuota := user.Quota
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	manifest := newWebUnlockTestManifest(upstream.URL)
	registerWebUnlockTestManifest(t, manifest)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
		"input": map[string]any{"url": "https://www.wayfair.com/product.html"},
	}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	var response struct {
		ErrorCode string `json:"error_code"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError || response.ErrorCode != "credential_error" {
		t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if calls != 0 {
		t.Fatalf("missing zone called upstream %d times", calls)
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if fresh.Quota != originalQuota {
		t.Fatalf("missing zone changed quota from %d to %d", originalQuota, fresh.Quota)
	}
}

func TestWebUnlockBusinessFailuresRefund(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		errorCode  string
	}{
		{name: "target 403", statusCode: http.StatusOK, body: `{"status_code":403,"body":"secret-page-body"}`, errorCode: "upstream_target_status"},
		{name: "target 404", statusCode: http.StatusOK, body: `{"status_code":404,"body":"secret-page-body"}`, errorCode: "upstream_target_status"},
		{name: "empty body", statusCode: http.StatusOK, body: `{"status_code":200,"body":"  "}`, errorCode: "upstream_empty_body"},
		{name: "brightdata non-2xx", statusCode: http.StatusUnauthorized, body: "secret-page-body", errorCode: "upstream_http_401"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
			t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
			t.Setenv("CAPABILITY_TEST_ZONE", "wayfair_unlocker")
			db := setupCapabilityControllerTestDB(t)
			user, token := seedCapabilityUserAndToken(t, db)
			originalQuota := user.Quota
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()

			manifest := newWebUnlockTestManifest(upstream.URL)
			manifest.Retry.Attempts = 1
			registerWebUnlockTestManifest(t, manifest)
			ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{
				"input": map[string]any{"url": "https://www.wayfair.com/product.html"},
			}, user, token)
			ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
			ExecuteCapability(ctx)

			var response struct {
				ErrorCode string `json:"error_code"`
			}
			if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusBadGateway || response.ErrorCode != test.errorCode {
				t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret-page-body") {
				t.Fatalf("upstream body leaked in response: %s", recorder.Body.String())
			}
			waitForCapabilityQuota(t, db, user.Id, originalQuota)
		})
	}
}

func newHeaderBillingTestManifest(endpoint string) *capability.Manifest {
	return &capability.Manifest{
		ID:       "header-billing-test-v1",
		Provider: "aisa",
		Mode:     capability.ModeSync,
		Endpoint: endpoint,
		Method:   http.MethodGet,
		Response: &capability.ResponseSpec{
			Output: map[string]string{"ok": "$.ok"},
		},
		Billing: capability.BillingSpec{
			Type:           capability.BillingPerUnit,
			Unit:           "micro_usd",
			PriceUSD:       0.000001,
			QuantityHeader: "X-AISA-Customer-Cost-Micros-USD",
			ReserveUSD:     0.10,
		},
		Limits: capability.LimitsSpec{Timeout: "5s", MaxResponseBytes: 4096},
		Retry:  capability.RetrySpec{Attempts: 1, RetryOn: []int{429, 503}},
	}
}

func executeHeaderBillingCapability(t *testing.T, manifest *capability.Manifest) (*httptest.ResponseRecorder, *model.User, *gorm.DB) {
	t.Helper()
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	oldLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	oldBatchUpdate := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldLogConsume })
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatchUpdate })
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	registerWebUnlockTestManifest(t, manifest)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{"input": map[string]any{}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)
	return recorder, user, db
}

func latestCapabilityLog(t *testing.T, db *gorm.DB, capabilityID string) model.Log {
	t.Helper()
	var log model.Log
	if err := db.Where("model_name = ?", capabilityID).Order("id DESC").First(&log).Error; err != nil {
		t.Fatalf("load capability log: %v", err)
	}
	return log
}

func TestHeaderBillingSettlesActualMicroUSD(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "12345")
		_, _ = w.Write([]byte(`{"ok":true,"secret_body":"do-not-log"}`))
	}))
	defer upstream.Close()

	manifest := newHeaderBillingTestManifest(upstream.URL)
	recorder, user, db := executeHeaderBillingCapability(t, manifest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	expectedQuota := user.Quota - int(math.Round(12345*0.000001*common.QuotaPerUnit))
	if fresh.Quota != expectedQuota {
		t.Fatalf("expected quota %d from response header, got %d", expectedQuota, fresh.Quota)
	}
	log := latestCapabilityLog(t, db, manifest.ID)
	var other map[string]any
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		t.Fatalf("decode log fields: %v", err)
	}
	if other["usage_source"] != "response_header" || other["usage_quantity"] != float64(12345) {
		t.Fatalf("unexpected usage log fields: %#v", other)
	}
	if strings.Contains(log.Other, "do-not-log") || strings.Contains(log.Other, "Authorization") {
		t.Fatalf("sensitive request or response data leaked into log: %s", log.Other)
	}
}

func TestHeaderBillingMissingAndInvalidHeaderUseReserveFallback(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "invalid", header: "not-a-number"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if test.header != "" {
					w.Header().Set("X-AISA-Customer-Cost-Micros-USD", test.header)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()

			manifest := newHeaderBillingTestManifest(upstream.URL)
			recorder, user, db := executeHeaderBillingCapability(t, manifest)
			if recorder.Code != http.StatusOK {
				t.Fatalf("reserve fallback must succeed: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var fresh model.User
			if err := db.First(&fresh, user.Id).Error; err != nil {
				t.Fatalf("load user: %v", err)
			}
			expectedQuota := user.Quota - int(math.Round(0.10*common.QuotaPerUnit))
			if fresh.Quota != expectedQuota {
				t.Fatalf("expected reserve charge %d, got %d", expectedQuota, fresh.Quota)
			}
			log := latestCapabilityLog(t, db, manifest.ID)
			var other map[string]any
			if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
				t.Fatalf("decode log fields: %v", err)
			}
			if other["usage_source"] != "reserve_fallback" {
				t.Fatalf("fallback usage was not marked: %#v", other)
			}
		})
	}
}

func TestHeaderBillingZeroAndFailure(t *testing.T) {
	t.Run("zero cost", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "0")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer upstream.Close()

		manifest := newHeaderBillingTestManifest(upstream.URL)
		recorder, user, db := executeHeaderBillingCapability(t, manifest)
		if recorder.Code != http.StatusOK {
			t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var fresh model.User
		if err := db.First(&fresh, user.Id).Error; err != nil {
			t.Fatalf("load user: %v", err)
		}
		if fresh.Quota != user.Quota {
			t.Fatalf("zero cost changed quota from %d to %d", user.Quota, fresh.Quota)
		}
	})

	t.Run("upstream failure refunds", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"provider request failed","message":"secret provider detail"}}`))
		}))
		defer upstream.Close()

		manifest := newHeaderBillingTestManifest(upstream.URL)
		manifest.Response.ErrorCodePath = "$.error.code"
		manifest.Response.ErrorMap = map[string]capability.ErrorMapSpec{
			"502": {Code: "upstream_unavailable", HTTPStatus: 503, Message: "The upstream data service is temporarily unavailable.", LogCode: "upstream_provider_failure"},
		}
		recorder, user, db := executeHeaderBillingCapability(t, manifest)
		if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "secret") {
			t.Fatalf("unexpected sanitized failure: code=%d body=%s", recorder.Code, recorder.Body.String())
		}
		waitForCapabilityQuota(t, db, user.Id, user.Quota)
		log := latestCapabilityLog(t, db, manifest.ID)
		if !strings.Contains(log.Other, "upstream_provider_failure") || strings.Contains(log.Other, "secret") {
			t.Fatalf("unexpected error log: %s", log.Other)
		}
	})

	t.Run("missing header without reserve fails and refunds", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer upstream.Close()

		manifest := newHeaderBillingTestManifest(upstream.URL)
		manifest.Billing.ReserveUSD = 0
		manifest.Billing.PriceUSD = 0.000001
		recorder, user, db := executeHeaderBillingCapability(t, manifest)
		var response struct {
			ErrorCode string `json:"error_code"`
		}
		if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if recorder.Code != http.StatusBadGateway || response.ErrorCode != "missing_usage" {
			t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
		}
		waitForCapabilityQuota(t, db, user.Id, user.Quota)
	})
}

func TestClientDisconnectStillSettlesHeaderBilling(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "12345")
		w.Header().Set("X-Request-ID", "disconnect-request")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	manifest := newHeaderBillingTestManifest(upstream.URL)
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	oldLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	oldBatchUpdate := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldLogConsume })
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatchUpdate })
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	registerWebUnlockTestManifest(t, manifest)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+manifest.ID, map[string]any{"input": map[string]any{}}, user, token)
	requestCtx, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	ctx.Params = gin.Params{{Key: "capability", Value: manifest.ID}}
	ExecuteCapability(ctx)

	if calls.Load() != 1 {
		t.Fatalf("expected the detached upstream call to complete once, got %d", calls.Load())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("a disconnected caller must not receive a response body: %s", recorder.Body.String())
	}
	expectedQuota := user.Quota - int(math.Round(12345*0.000001*common.QuotaPerUnit))
	waitForCapabilityQuota(t, db, user.Id, expectedQuota)
	log := latestCapabilityLog(t, db, manifest.ID)
	var other map[string]any
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if log.Quota != user.Quota-expectedQuota || other["status"] != "client_gone" ||
		other["usage_source"] != "response_header" || other["upstream_request_id"] != "disconnect-request" {
		t.Fatalf("unexpected disconnected-call log: quota=%d other=%s", log.Quota, log.Other)
	}
}

func TestPost2xxFailuresSettleHeaderCost(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		maxBytes  int64
		success   *capability.SuccessSpec
		output    map[string]string
		errorCode string
	}{
		{
			name:      "too large",
			body:      `{"ok":true,"padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`,
			maxBytes:  16,
			output:    map[string]string{"ok": "$.ok"},
			errorCode: "upstream_response_too_large",
		},
		{
			name:      "invalid json",
			body:      `{not-json`,
			maxBytes:  4096,
			output:    map[string]string{"ok": "$.ok"},
			errorCode: "invalid_upstream_json",
		},
		{
			name:      "success check",
			body:      `{"ok":false}`,
			maxBytes:  4096,
			success:   &capability.SuccessSpec{Path: "$.ok", Values: []any{true}, FailureCode: "target_failed"},
			output:    map[string]string{"ok": "$.ok"},
			errorCode: "target_failed",
		},
		{
			name:      "missing output",
			body:      `{"other":true}`,
			maxBytes:  4096,
			output:    map[string]string{"ok": "$.ok"},
			errorCode: "missing_output_field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "12345")
				w.Header().Set("X-Request-ID", "post-2xx-request")
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()

			manifest := newHeaderBillingTestManifest(upstream.URL)
			manifest.Response.Output = test.output
			manifest.Response.Success = test.success
			manifest.Limits.MaxResponseBytes = test.maxBytes
			manifest.Retry.Attempts = 3
			recorder, user, db := executeHeaderBillingCapability(t, manifest)
			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				ErrorCode string  `json:"error_code"`
				CostUSD   float64 `json:"cost_usd"`
			}
			if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			expectedChargedQuota := int(math.Round(12345 * 0.000001 * common.QuotaPerUnit))
			if response.ErrorCode != test.errorCode || math.Abs(response.CostUSD-quotaToUSD(expectedChargedQuota)) > 0.0000001 {
				t.Fatalf("unexpected charged error: %#v body=%s", response, recorder.Body.String())
			}
			expectedQuota := user.Quota - expectedChargedQuota
			waitForCapabilityQuota(t, db, user.Id, expectedQuota)
			log := latestCapabilityLog(t, db, manifest.ID)
			var other map[string]any
			if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			if log.Quota != expectedChargedQuota || other["usage_source"] != "response_header" ||
				other["upstream_request_id"] != "post-2xx-request" {
				t.Fatalf("unexpected charged error log: quota=%d other=%s", log.Quota, log.Other)
			}
		})
	}
}

func TestPost2xxMissingHeaderProcessingFailureSettlesReserve(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not-json`))
	}))
	defer upstream.Close()

	manifest := newHeaderBillingTestManifest(upstream.URL)
	recorder, user, db := executeHeaderBillingCapability(t, manifest)
	var response struct {
		ErrorCode   string  `json:"error_code"`
		CostUSD     float64 `json:"cost_usd"`
		UsageSource string  `json:"usage_source"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ErrorCode != "invalid_upstream_json" || response.CostUSD != 0.10 ||
		response.UsageSource != "reserve_fallback" {
		t.Fatalf("unexpected reserve fallback error: %#v", response)
	}
	reservedQuota := int(math.Round(0.10 * common.QuotaPerUnit))
	waitForCapabilityQuota(t, db, user.Id, user.Quota-reservedQuota)
	log := latestCapabilityLog(t, db, manifest.ID)
	var other map[string]any
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if log.Quota != reservedQuota || other["usage_source"] != "reserve_fallback" {
		t.Fatalf("unexpected reserve fallback log: quota=%d other=%s", log.Quota, log.Other)
	}
}

func TestPost2xxBodyReadTimeoutSettlesHeaderCost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "12345")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	manifest := newHeaderBillingTestManifest(upstream.URL)
	manifest.Limits.Timeout = "20ms"
	recorder, user, db := executeHeaderBillingCapability(t, manifest)
	var response struct {
		ErrorCode string  `json:"error_code"`
		CostUSD   float64 `json:"cost_usd"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusBadGateway || response.ErrorCode != "upstream_read_failed" ||
		response.CostUSD != quotaToUSD(6173) {
		t.Fatalf("unexpected read failure: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	waitForCapabilityQuota(t, db, user.Id, user.Quota-6173)
}

func TestWrittenTimeoutSettlesReserveOnce(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	manifest := newHeaderBillingTestManifest(upstream.URL)
	manifest.Limits.Timeout = "20ms"
	manifest.Retry.Attempts = 3
	recorder, user, db := executeHeaderBillingCapability(t, manifest)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("unexpected response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		ErrorCode string  `json:"error_code"`
		CostUSD   float64 `json:"cost_usd"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ErrorCode != "upstream_timeout" || response.CostUSD != 0.10 || calls.Load() != 1 {
		t.Fatalf("unexpected uncertain timeout: response=%#v calls=%d", response, calls.Load())
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	reservedQuota := int(math.Round(0.10 * common.QuotaPerUnit))
	expectedQuota := user.Quota - reservedQuota
	if fresh.Quota != expectedQuota {
		t.Fatalf("expected reserve charge %d, got %d", user.Quota-expectedQuota, user.Quota-fresh.Quota)
	}
	log := latestCapabilityLog(t, db, manifest.ID)
	var other map[string]any
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if log.Quota != reservedQuota {
		t.Fatalf("unexpected timeout log quota: %d", log.Quota)
	}
	if other["usage_source"] != "uncertain_reserve" || other["status"] != "failed" {
		t.Fatalf("unexpected timeout log: %s", log.Other)
	}
}

func TestUnwrittenConnectionFailureRefundsAfterRetry(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := closed.URL
	closed.Close()
	manifest := newHeaderBillingTestManifest(endpoint)
	manifest.Retry.Attempts = 2
	recorder, user, db := executeHeaderBillingCapability(t, manifest)
	var response struct {
		ErrorCode string `json:"error_code"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusBadGateway || response.ErrorCode != "upstream_unreachable" {
		t.Fatalf("unexpected unwritten connection failure: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	waitForCapabilityQuota(t, db, user.Id, user.Quota)
	log := latestCapabilityLog(t, db, manifest.ID)
	if log.Quota != 0 {
		t.Fatalf("unwritten connection failure was charged: quota=%d", log.Quota)
	}
}

func TestSettlementFailureKeepsReserveAndLogsReceivable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "12345")
		w.Header().Set("X-Request-ID", "settlement-failed-request")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	originalSettle := settleCapabilityBillingRequest
	var attempts atomic.Int32
	settleCapabilityBillingRequest = func(*gin.Context, *relaycommon.RelayInfo, int) error {
		attempts.Add(1)
		return errors.New("forced settlement failure")
	}
	t.Cleanup(func() { settleCapabilityBillingRequest = originalSettle })

	manifest := newHeaderBillingTestManifest(upstream.URL)
	recorder, user, db := executeHeaderBillingCapability(t, manifest)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"billing_failed"`) {
		t.Fatalf("unexpected billing failure response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var fresh model.User
	if err := db.First(&fresh, user.Id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	reservedQuota := int(math.Round(0.10 * common.QuotaPerUnit))
	if fresh.Quota != user.Quota-reservedQuota {
		t.Fatalf("failed settlement must retain the pre-consumed reserve: expected=%d got=%d", user.Quota-reservedQuota, fresh.Quota)
	}
	log := latestCapabilityLog(t, db, manifest.ID)
	var other map[string]any
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	expectedChargedQuota := int(math.Round(12345 * 0.000001 * common.QuotaPerUnit))
	if log.Quota != expectedChargedQuota || other["status"] != "billing_failed" ||
		other["upstream_request_id"] != "settlement-failed-request" {
		t.Fatalf("unexpected settlement failure log: quota=%d other=%s", log.Quota, log.Other)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected three settlement attempts, got %d", attempts.Load())
	}
}

func TestPerCallPost2xxFailureStillRefunds(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not-json`))
	}))
	defer upstream.Close()
	m := &capability.Manifest{
		ID:       "per-call-invalid-json-v1",
		Provider: "test",
		Mode:     capability.ModeSync,
		Endpoint: upstream.URL,
		Method:   http.MethodGet,
		Response: &capability.ResponseSpec{Output: map[string]string{"ok": "$.ok"}},
		Billing:  capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0.0001},
		Limits:   capability.LimitsSpec{Timeout: "1s", MaxResponseBytes: 4096},
	}
	registerWebUnlockTestManifest(t, m)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+m.ID, map[string]any{"input": map[string]any{}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: m.ID}}
	ExecuteCapability(ctx)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("unexpected per-call response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	waitForCapabilityQuota(t, db, user.Id, user.Quota)
}

func TestPerCallUncertainTimeoutStillRefunds(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	m := &capability.Manifest{
		ID:       "per-call-timeout-v1",
		Provider: "test",
		Mode:     capability.ModeSync,
		Endpoint: upstream.URL,
		Method:   http.MethodGet,
		Response: &capability.ResponseSpec{Output: map[string]string{"ok": "$.ok"}},
		Billing:  capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0.0001},
		Limits:   capability.LimitsSpec{Timeout: "20ms", MaxResponseBytes: 4096},
		Retry:    capability.RetrySpec{Attempts: 3, RetryOn: []int{429, 503}},
	}
	registerWebUnlockTestManifest(t, m)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/capabilities/"+m.ID, map[string]any{"input": map[string]any{}}, user, token)
	ctx.Params = gin.Params{{Key: "capability", Value: m.ID}}
	ExecuteCapability(ctx)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("unexpected per-call timeout response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	waitForCapabilityQuota(t, db, user.Id, user.Quota)
}

func TestAsyncSubmitPost2xxFailureSettlesPerUnitCost(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	oldBatchUpdate := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatchUpdate })
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-AISA-Customer-Cost-Micros-USD", "23456")
		_, _ = w.Write([]byte(`{not-json`))
	}))
	defer upstream.Close()
	m := &capability.Manifest{
		ID:       "async-submit-uncertain-v1",
		Provider: "aisa",
		Mode:     capability.ModeAsync,
		Submit:   &capability.SubmitSpec{Endpoint: upstream.URL, Method: http.MethodGet, JobIDPath: "$.job"},
		Poll: &capability.PollSpec{
			Endpoint:   upstream.URL,
			Method:     http.MethodGet,
			StatusPath: "$.status",
			ResultPath: "$.result",
		},
		Billing: capability.BillingSpec{
			Type:           capability.BillingPerUnit,
			Unit:           "micro_usd",
			PriceUSD:       0.000001,
			QuantityHeader: "X-AISA-Customer-Cost-Micros-USD",
			EstimatePath:   "$.input.pages",
		},
		Limits: capability.LimitsSpec{Timeout: "1s", MaxResponseBytes: 4096},
	}
	registerWebUnlockTestManifest(t, m)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/tasks", map[string]any{
		"capability": m.ID,
		"input":      map[string]any{"pages": 1},
	}, user, token)
	SubmitCapabilityTask(ctx)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("unexpected async submit response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	expectedChargedQuota := int(math.Round(23456 * 0.000001 * common.QuotaPerUnit))
	waitForCapabilityQuota(t, db, user.Id, user.Quota-expectedChargedQuota)
}

func TestAsyncSubmitClientDisconnectStillCreatesPaidTask(t *testing.T) {
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	oldBatchUpdate := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatchUpdate })
	db := setupCapabilityControllerTestDB(t)
	user, token := seedCapabilityUserAndToken(t, db)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":"job-disconnected","status":"QUEUED"}`))
	}))
	defer upstream.Close()
	m := &capability.Manifest{
		ID:       "async-disconnect-v1",
		Provider: "test",
		Mode:     capability.ModeAsync,
		Submit:   &capability.SubmitSpec{Endpoint: upstream.URL, Method: http.MethodGet, JobIDPath: "$.job"},
		Poll: &capability.PollSpec{
			Endpoint:   upstream.URL,
			Method:     http.MethodGet,
			StatusPath: "$.status",
			ResultPath: "$.result",
		},
		Billing: capability.BillingSpec{Type: capability.BillingPerCall, PriceUSD: 0.001},
		Limits:  capability.LimitsSpec{Timeout: "1s", MaxResponseBytes: 4096},
	}
	registerWebUnlockTestManifest(t, m)
	ctx, recorder := newCapabilityContext(t, http.MethodPost, "/v1/tasks", map[string]any{
		"capability": m.ID,
		"input":      map[string]any{},
	}, user, token)
	requestCtx, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	SubmitCapabilityTask(ctx)
	if recorder.Body.Len() != 0 {
		t.Fatalf("disconnected async submit returned a body: %s", recorder.Body.String())
	}
	expectedQuota := user.Quota - int(math.Round(0.001*common.QuotaPerUnit))
	waitForCapabilityQuota(t, db, user.Id, expectedQuota)
	var task model.Task
	if err := db.Where("platform = ? AND action = ?", constant.TaskPlatformCapability, m.ID).First(&task).Error; err != nil {
		t.Fatalf("paid async task was not persisted: %v", err)
	}
}

func TestCapabilityListIncludesPerUnitMaxUSD(t *testing.T) {
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
	found := false
	for _, item := range response.Data {
		if item.ID == "site-referrals-v1" {
			found = true
			if item.Billing.MaxUSD != 1.50 {
				t.Fatalf("unexpected max_usd: %#v", item.Billing)
			}
		}
	}
	if !found {
		t.Fatal("site-referrals-v1 is missing from capability list")
	}
}
