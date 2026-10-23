package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// adminCreateUserTokenRequest 只接受允许管理员写入的字段。
// 不能直接 bind model.Token，否则调用者可以伪造 UserId / Key / Status / Id。
type adminCreateUserTokenRequest struct {
	Name               string `json:"name"`
	UnlimitedQuota     *bool  `json:"unlimited_quota"`
	RemainQuota        int    `json:"remain_quota"`
	ExpiredTime        *int64 `json:"expired_time"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
	ModelLimits        string `json:"model_limits"`
	AllowIps           string `json:"allow_ips"`
	Group              string `json:"group"`
	CrossGroupRetry    bool   `json:"cross_group_retry"`
}

// AdminCreateUserToken 让管理员替指定用户创建 New API token。
//
// 路由：POST /api/user/:id/token
// 鉴权：middleware.AdminAuth()（Authorization 为管理员 access token，
// New-Api-User 必须等于管理员自己；两件事都由 AdminAuth 校验）。
//
// 幂等：同一 target user + name 只会有一条未删除 token。
// 并发下靠 tokens(user_id, idempotency_name) 唯一索引兜底；输的那一方
// 在 insert 失败后回查并返回同一个 key。
func AdminCreateUserToken(c *gin.Context) {
	callerId := c.GetInt("id")
	callerRole := c.GetInt("role")

	targetId, err := strconv.Atoi(c.Param("id"))
	if err != nil || targetId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return
	}

	target, err := model.GetUserById(targetId, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorI18n(c, i18n.MsgUserNotExists)
			return
		}
		// 不返回/打印 err：可能含 SQL、表名或内部路径。
		common.SysLog(fmt.Sprintf("admin create token: load target user failed: caller=%d target=%d", callerId, targetId))
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if target.Status != common.UserStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserDisabled)
		return
	}

	// 必须是严格高于目标角色；root 也不能替另一个 root 创建。
	if callerRole <= target.Role {
		if callerRole == target.Role {
			common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		} else {
			common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		}
		return
	}

	var req adminCreateUserTokenRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		common.ApiErrorI18n(c, i18n.MsgNameCannotBeEmpty)
		return
	}

	// 幂等快路径：同名 token 已存在时直接返回完整 key，不重复创建。
	existing, err := model.GetActiveTokenByUserIdAndName(targetId, name)
	if err == nil {
		writeAdminCreatedToken(c, existing)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.SysLog(fmt.Sprintf("admin create token: lookup existing failed: caller=%d target=%d", callerId, targetId))
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}

	unlimited := true
	if req.UnlimitedQuota != nil {
		unlimited = *req.UnlimitedQuota
	}
	expiredTime := int64(-1)
	if req.ExpiredTime != nil {
		expiredTime = *req.ExpiredTime
	}
	allowIps := req.AllowIps
	input := &model.Token{
		Name:               name,
		UnlimitedQuota:     unlimited,
		RemainQuota:        req.RemainQuota,
		ExpiredTime:        expiredTime,
		ModelLimitsEnabled: req.ModelLimitsEnabled,
		ModelLimits:        req.ModelLimits,
		AllowIps:           &allowIps,
		Group:              req.Group,
		CrossGroupRetry:    req.CrossGroupRetry,
	}

	if validationErr := validateTokenForCreate(targetId, input); validationErr != nil {
		writeTokenCreateValidationError(c, validationErr)
		return
	}

	idempotencyName := name
	created, err := createTokenForUser(targetId, input, &idempotencyName)
	if err != nil {
		// 并发下另一个请求可能刚插入成功；回查并返回它的 key。
		existing, lookupErr := model.GetActiveTokenByUserIdAndName(targetId, name)
		if lookupErr == nil {
			writeAdminCreatedToken(c, existing)
			return
		}
		// 不打印 err：GORM 的 insert 错误可能包含完整 key。
		common.SysLog(fmt.Sprintf("admin create token: insert failed: caller=%d target=%d", callerId, targetId))
		common.ApiErrorI18n(c, i18n.MsgCreateFailed)
		return
	}

	writeAdminCreatedToken(c, created)
}

// writeAdminCreatedToken 返回创建/幂等读取的完整 key。
// 这是唯一返回完整 key 的地方；列表/详情仍走 masked 响应。
func writeAdminCreatedToken(c *gin.Context, token *model.Token) {
	key := token.GetFullKey()
	if !strings.HasPrefix(key, "sk-") {
		key = "sk-" + key
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":  token.Id,
			"key": key,
		},
	})
}
