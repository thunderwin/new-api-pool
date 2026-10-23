package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

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
