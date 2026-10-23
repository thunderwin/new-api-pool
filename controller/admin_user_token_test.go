package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type adminCreatedTokenData struct {
	Id  int    `json:"id"`
	Key string `json:"key"`
}

func setupAdminUserTokenTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTokenControllerTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Token{}); err != nil {
		t.Fatalf("failed to migrate user/token tables: %v", err)
	}
	return db
}

func seedAdminTestUser(t *testing.T, db *gorm.DB, id int, username string, role int, status int, accessToken string) *model.User {
	t.Helper()
	user := &model.User{
		Id:          id,
		Username:    username,
		Password:    "password123",
		DisplayName: username,
		Role:        role,
		Status:      status,
		Group:       "default",
		AffCode:     fmt.Sprintf("aff-%d", id),
	}
	if accessToken != "" {
		user.AccessToken = &accessToken
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to seed user id=%d: %v", id, err)
	}
	return user
}

func adminTokenRequestBody(name string) map[string]any {
	return map[string]any{
		"name":                 name,
		"unlimited_quota":      true,
		"remain_quota":         0,
		"expired_time":         -1,
		"model_limits_enabled": false,
		"model_limits":         "",
		"allow_ips":            "",
		"group":                "",
		"cross_group_retry":    false,
	}
}

func newAdminCreateTokenContext(t *testing.T, callerId int, callerRole int, targetId int, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPost,
		"/api/user/"+strconv.Itoa(targetId)+"/token",
		body,
		callerId,
	)
	ctx.Set("role", callerRole)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(targetId)}}
	return ctx, recorder
}

func decodeAdminCreatedToken(t *testing.T, recorder *httptest.ResponseRecorder) adminCreatedTokenData {
	t.Helper()
	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected token creation to succeed, got message: %s", response.Message)
	}
	var data adminCreatedTokenData
	if err := common.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("failed to decode created token data: %v", err)
	}
	return data
}

func tokenKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func TestAdminCreateUserTokenSuccess(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	target := seedAdminTestUser(t, db, 2, "target", common.RoleCommonUser, common.UserStatusEnabled, "")

	name := "rakazo-stable-user-1"
	ctx, recorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody(name))
	AdminCreateUserToken(ctx)

	data := decodeAdminCreatedToken(t, recorder)
	if data.Id == 0 {
		t.Fatal("expected token id in response")
	}
	if !strings.HasPrefix(data.Key, "sk-") {
		t.Fatal("expected full sk- key in creation response")
	}

	var stored model.Token
	if err := db.First(&stored, data.Id).Error; err != nil {
		t.Fatalf("failed to load created token: %v", err)
	}
	if stored.UserId != target.Id {
		t.Fatalf("expected target user id %d, got %d", target.Id, stored.UserId)
	}
	if stored.Name != name {
		t.Fatalf("expected token name %q, got %q", name, stored.Name)
	}
	if !stored.UnlimitedQuota || stored.RemainQuota != 0 || stored.ExpiredTime != -1 {
		t.Fatal("created token did not preserve unlimited quota defaults")
	}
	if stored.Status != common.TokenStatusEnabled {
		t.Fatalf("expected enabled token, got status %d", stored.Status)
	}
	if stored.IdempotencyName == nil || *stored.IdempotencyName != name {
		t.Fatal("expected idempotency name to be stored")
	}
	if stored.Key == "" || "sk-"+stored.Key != data.Key {
		t.Fatal("stored key does not match the full key returned by the API")
	}
}

func TestAdminCreateUserTokenMiddlewareRejectsNonAdminAndHeaderMismatch(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	commonAccess := strings.Repeat("c", 32)
	adminAccess := strings.Repeat("a", 32)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, adminAccess)
	seedAdminTestUser(t, db, 2, "common", common.RoleCommonUser, common.UserStatusEnabled, commonAccess)
	target := seedAdminTestUser(t, db, 3, "target", common.RoleCommonUser, common.UserStatusEnabled, "")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("test-session-secret"))
	router.Use(sessions.Sessions("session", store))
	router.POST("/api/user/:id/token", middleware.AdminAuth(), AdminCreateUserToken)

	call := func(accessToken, newAPIUser, name string) tokenAPIResponse {
		body, err := common.Marshal(adminTokenRequestBody(name))
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/user/"+strconv.Itoa(target.Id)+"/token", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("New-Api-User", newAPIUser)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return decodeAPIResponse(t, recorder)
	}

	if response := call(commonAccess, "2", "rakazo-non-admin"); response.Success {
		t.Fatal("expected non-admin caller to be rejected")
	}
	if response := call(adminAccess, "999", "rakazo-wrong-header"); response.Success {
		t.Fatal("expected New-Api-User mismatch to be rejected")
	}
	response := call(adminAccess, "1", "rakazo-admin-ok")
	if !response.Success {
		t.Fatalf("expected admin access-token call to succeed, got message: %s", response.Message)
	}
	var data adminCreatedTokenData
	if err := common.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("failed to decode created token data: %v", err)
	}
	if data.Id == 0 || !strings.HasPrefix(data.Key, "sk-") {
		t.Fatal("expected admin middleware call to return a full key")
	}
}

func TestAdminCreateUserTokenRejectsMissingAndDisabledTarget(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	disabled := seedAdminTestUser(t, db, 2, "disabled", common.RoleCommonUser, common.UserStatusDisabled, "")

	missingCtx, missingRecorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, 999, adminTokenRequestBody("rakazo-missing"))
	AdminCreateUserToken(missingCtx)
	missingResponse := decodeAPIResponse(t, missingRecorder)
	if missingResponse.Success || missingResponse.Message == "" {
		t.Fatal("expected missing target to be rejected with a stable message")
	}
	if strings.Contains(missingRecorder.Body.String(), "sk-") {
		t.Fatal("missing target response leaked a key")
	}

	disabledCtx, disabledRecorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, disabled.Id, adminTokenRequestBody("rakazo-disabled"))
	AdminCreateUserToken(disabledCtx)
	disabledResponse := decodeAPIResponse(t, disabledRecorder)
	if disabledResponse.Success || disabledResponse.Message == "" {
		t.Fatal("expected disabled target to be rejected with a stable message")
	}
	if strings.Contains(disabledRecorder.Body.String(), "sk-") {
		t.Fatal("disabled target response leaked a key")
	}
}

func TestAdminCreateUserTokenEnforcesStrictlyHigherRole(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	peerAdmin := seedAdminTestUser(t, db, 2, "peer-admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	root := seedAdminTestUser(t, db, 3, "root", common.RoleRootUser, common.UserStatusEnabled, "")
	anotherRoot := seedAdminTestUser(t, db, 4, "root-2", common.RoleRootUser, common.UserStatusEnabled, "")

	cases := []struct {
		name       string
		callerId   int
		callerRole int
		targetId   int
	}{
		{"admin cannot target same-level admin", 1, common.RoleAdminUser, peerAdmin.Id},
		{"admin cannot target root", 1, common.RoleAdminUser, root.Id},
		{"root cannot target another root", root.Id, common.RoleRootUser, anotherRoot.Id},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := newAdminCreateTokenContext(t, tc.callerId, tc.callerRole, tc.targetId, adminTokenRequestBody("rakazo-forbidden"))
			AdminCreateUserToken(ctx)
			response := decodeAPIResponse(t, recorder)
			if response.Success {
				t.Fatal("expected role hierarchy check to reject")
			}
		})
	}

	ctx, recorder := newAdminCreateTokenContext(t, root.Id, common.RoleRootUser, peerAdmin.Id, adminTokenRequestBody("rakazo-root-allowed"))
	AdminCreateUserToken(ctx)
	_ = decodeAdminCreatedToken(t, recorder)
}

func TestAdminCreateUserTokenIsIdempotentAndAllowsRecreateAfterDelete(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	target := seedAdminTestUser(t, db, 2, "target", common.RoleCommonUser, common.UserStatusEnabled, "")
	name := "rakazo-idempotent"

	ctx1, recorder1 := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody(name))
	AdminCreateUserToken(ctx1)
	first := decodeAdminCreatedToken(t, recorder1)

	ctx2, recorder2 := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody(name))
	AdminCreateUserToken(ctx2)
	second := decodeAdminCreatedToken(t, recorder2)

	if first.Id != second.Id || tokenKeyFingerprint(first.Key) != tokenKeyFingerprint(second.Key) {
		t.Fatal("idempotent retry returned a different token")
	}
	var activeCount int64
	if err := db.Model(&model.Token{}).Where("user_id = ? AND name = ?", target.Id, name).Count(&activeCount).Error; err != nil {
		t.Fatalf("failed to count active tokens: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active token, got %d", activeCount)
	}

	if err := model.DeleteTokenById(first.Id, target.Id); err != nil {
		t.Fatalf("failed to delete token: %v", err)
	}
	ctx3, recorder3 := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody(name))
	AdminCreateUserToken(ctx3)
	third := decodeAdminCreatedToken(t, recorder3)
	if third.Id == first.Id {
		t.Fatal("expected a new token after deleting the previous one")
	}

	var deleted model.Token
	if err := db.Unscoped().First(&deleted, first.Id).Error; err != nil {
		t.Fatalf("failed to load deleted token: %v", err)
	}
	if deleted.IdempotencyName != nil {
		t.Fatal("expected deleted token to release its idempotency name")
	}
}

func TestAdminCreateUserTokenConcurrentFirstCallCreatesOneToken(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	target := seedAdminTestUser(t, db, 2, "target", common.RoleCommonUser, common.UserStatusEnabled, "")

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	name := "rakazo-concurrent"
	const goroutines = 8
	type result struct {
		ok      bool
		id      int
		fp      string
		message string
	}
	results := make(chan result, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, recorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody(name))
			AdminCreateUserToken(ctx)
			var response tokenAPIResponse
			if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				results <- result{message: "decode failed"}
				return
			}
			if !response.Success {
				results <- result{message: response.Message}
				return
			}
			var data adminCreatedTokenData
			if err := common.Unmarshal(response.Data, &data); err != nil {
				results <- result{message: "data decode failed"}
				return
			}
			results <- result{ok: true, id: data.Id, fp: tokenKeyFingerprint(data.Key)}
		}()
	}
	wg.Wait()
	close(results)

	var expectedId int
	var expectedFingerprint string
	for result := range results {
		if !result.ok {
			t.Fatalf("concurrent request failed: %s", result.message)
		}
		if expectedId == 0 {
			expectedId = result.id
			expectedFingerprint = result.fp
			continue
		}
		if result.id != expectedId || result.fp != expectedFingerprint {
			t.Fatal("concurrent requests returned different tokens")
		}
	}

	var count int64
	if err := db.Unscoped().Model(&model.Token{}).Where("user_id = ? AND name = ?", target.Id, name).Count(&count).Error; err != nil {
		t.Fatalf("failed to count tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one token row, got %d", count)
	}
}

func TestAdminCreateUserTokenDoesNotLeakKeyInErrorsOrSysLog(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	target := seedAdminTestUser(t, db, 2, "target", common.RoleCommonUser, common.UserStatusEnabled, "")
	disabled := seedAdminTestUser(t, db, 3, "disabled", common.RoleCommonUser, common.UserStatusDisabled, "")

	oldWriter := gin.DefaultWriter
	oldErrorWriter := gin.DefaultErrorWriter
	var out, errOut bytes.Buffer
	gin.DefaultWriter = &out
	gin.DefaultErrorWriter = &errOut
	defer func() {
		gin.DefaultWriter = oldWriter
		gin.DefaultErrorWriter = oldErrorWriter
	}()

	ctx, recorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody("rakazo-no-leak"))
	AdminCreateUserToken(ctx)
	data := decodeAdminCreatedToken(t, recorder)

	if strings.Contains(out.String(), "sk-") || strings.Contains(errOut.String(), "sk-") {
		t.Fatal("sys log contains a token key prefix")
	}
	if strings.Contains(out.String(), data.Key) || strings.Contains(errOut.String(), data.Key) {
		t.Fatal("sys log contains the full token key")
	}

	disabledCtx, disabledRecorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, disabled.Id, adminTokenRequestBody("rakazo-disabled-leak"))
	AdminCreateUserToken(disabledCtx)
	if strings.Contains(disabledRecorder.Body.String(), "sk-") {
		t.Fatal("error response leaked a token key")
	}

	// 强制 insert 失败，确认错误响应和我们的 sys log 不会带出生成中的 key。
	if err := db.Callback().Create().Before("gorm:create").Register("force_token_create_error", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "tokens" {
			tx.AddError(errors.New("forced token insert failure"))
		}
	}); err != nil {
		t.Fatalf("failed to register forced create error callback: %v", err)
	}
	failedCtx, failedRecorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody("rakazo-forced-failure"))
	AdminCreateUserToken(failedCtx)
	failedResponse := decodeAPIResponse(t, failedRecorder)
	if failedResponse.Success {
		t.Fatal("expected forced insert failure to be reported")
	}
	if strings.Contains(failedRecorder.Body.String(), "sk-") ||
		strings.Contains(out.String(), "sk-") ||
		strings.Contains(errOut.String(), "sk-") {
		t.Fatal("forced insert failure leaked a token key")
	}
}

func TestAdminCreateUserTokenReusesSharedValidation(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedAdminTestUser(t, db, 1, "admin", common.RoleAdminUser, common.UserStatusEnabled, "")
	target := seedAdminTestUser(t, db, 2, "target", common.RoleCommonUser, common.UserStatusEnabled, "")

	originalMax := operation_setting.GetTokenSetting().MaxUserTokens
	operation_setting.GetTokenSetting().MaxUserTokens = 1
	t.Cleanup(func() {
		operation_setting.GetTokenSetting().MaxUserTokens = originalMax
	})
	seedToken(t, db, target.Id, "existing-token", "existing1234567890")

	ctx, recorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody("rakazo-over-limit"))
	AdminCreateUserToken(ctx)
	response := decodeAPIResponse(t, recorder)
	if response.Success || !strings.Contains(response.Message, "最大令牌数量") {
		t.Fatalf("expected shared token-count limit to reject, got message: %s", response.Message)
	}

	longName := strings.Repeat("x", 51)
	longCtx, longRecorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, adminTokenRequestBody(longName))
	AdminCreateUserToken(longCtx)
	if response := decodeAPIResponse(t, longRecorder); response.Success {
		t.Fatal("expected shared name-length validation to reject")
	}

	negativeQuota := adminTokenRequestBody("rakazo-negative-quota")
	negativeQuota["unlimited_quota"] = false
	negativeQuota["remain_quota"] = -1
	quotaCtx, quotaRecorder := newAdminCreateTokenContext(t, 1, common.RoleAdminUser, target.Id, negativeQuota)
	AdminCreateUserToken(quotaCtx)
	if response := decodeAPIResponse(t, quotaRecorder); response.Success {
		t.Fatal("expected shared quota validation to reject")
	}
}

func TestTokenIdempotencyIndexExists(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	if !db.Migrator().HasIndex(&model.Token{}, "idx_tokens_user_idempotency") {
		t.Fatal("expected unique idempotency index to exist")
	}
}

func TestTokenIdempotencyIndexAllowsMultipleNullNames(t *testing.T) {
	db := setupAdminUserTokenTestDB(t)
	seedToken(t, db, 1, "same-name", "regular-key-1111111111")
	seedToken(t, db, 1, "same-name", "regular-key-2222222222")

	var count int64
	if err := db.Model(&model.Token{}).Where("user_id = ? AND name = ?", 1, "same-name").Count(&count).Error; err != nil {
		t.Fatalf("failed to count regular tokens: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected regular NULL idempotency rows to allow duplicate names, got %d", count)
	}
}
