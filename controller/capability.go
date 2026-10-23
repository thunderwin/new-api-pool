package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayhelper "github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type capabilityRequest struct {
	Input          map[string]any `json:"input"`
	Options        map[string]any `json:"options"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type asyncCapabilityRequest struct {
	Capability     string         `json:"capability"`
	Input          map[string]any `json:"input"`
	Options        map[string]any `json:"options"`
	IdempotencyKey string         `json:"idempotency_key"`
}

func ListCapabilities(c *gin.Context) {
	items := make([]gin.H, 0)
	for _, m := range capability.List() {
		unit := m.Billing.Unit
		if unit == "" && m.Billing.Type == capability.BillingPerCall {
			unit = "call"
		}
		items = append(items, gin.H{
			"id":          m.ID,
			"provider":    m.Provider,
			"mode":        m.Mode,
			"description": m.Description,
			"billing": gin.H{
				"type":      m.Billing.Type,
				"unit":      unit,
				"price_usd": m.Billing.PriceUSD,
			},
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": items})
}

// ExecuteCapability runs a synchronous capability. It uses the same sk- token
// auth, user balance and consume logs as the relay routes.
func ExecuteCapability(c *gin.Context) {
	m, ok := capability.Get(c.Param("capability"))
	if !ok || m.Mode != capability.ModeSync {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "capability_not_found",
			Message:    "capability does not exist or is not synchronous",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req capabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "invalid_request",
			Message:    "request body is invalid",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	if execErr := capability.ValidateSync(m, req.Input); execErr != nil {
		respondCapabilityError(c, execErr)
		return
	}

	info, priceData, execErr := prepareCapabilityBilling(c, m, req.Input)
	if execErr != nil {
		respondCapabilityError(c, execErr)
		return
	}

	result, execErr := capability.ExecuteSync(c.Request.Context(), m, req.Input, req.IdempotencyKey)
	if execErr != nil {
		refundCapabilityBilling(c, info)
		logCapabilityConsume(c, m, info, 0, "failed", execErr.Code, "", "call", 0, "sync")
		respondCapabilityError(c, execErr)
		return
	}

	actualQuota, usageUnit, usageQty, usageErr := capabilitySyncUsage(m, priceData, result.Raw)
	if usageErr != nil {
		refundCapabilityBilling(c, info)
		logCapabilityConsume(c, m, info, 0, "failed", "missing_usage", result.UpstreamRequestID, usageUnit, 0, "sync")
		respondCapabilityError(c, usageErr)
		return
	}
	if err := service.SettleBilling(c, info, actualQuota); err != nil {
		logCapabilityConsume(c, m, info, 0, "failed", "billing_failed", result.UpstreamRequestID, usageUnit, usageQty, "sync")
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "billing_failed",
			Message:    "billing settlement failed",
			HTTPStatus: http.StatusInternalServerError,
		})
		return
	}

	logCapabilityConsume(c, m, info, actualQuota, "succeeded", "", result.UpstreamRequestID, usageUnit, usageQty, "sync")
	c.JSON(http.StatusOK, gin.H{
		"success":             true,
		"id":                  info.RequestId,
		"capability":          m.ID,
		"output":              result.Output,
		"usage":               gin.H{"unit": usageUnit, "quantity": usageQty},
		"cost_usd":            quotaToUSD(actualQuota),
		"provider_request_id": result.UpstreamRequestID,
	})
}

// SubmitCapabilityTask submits an async capability and returns a task id.
// Billing for the pilot is per-call; the quota is reserved/charged at submit.
func SubmitCapabilityTask(c *gin.Context) {
	var req asyncCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "invalid_request",
			Message:    "request body is invalid",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	m, ok := capability.Get(req.Capability)
	if !ok || m.Mode != capability.ModeAsync {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "capability_not_found",
			Message:    "capability does not exist or is not asynchronous",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}

	userId := c.GetInt("id")
	keyHash := ""
	var claim *model.CapabilityIdempotency
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		keyHash = hashCapabilityKey(req.IdempotencyKey)
		row, claimed, err := model.ClaimCapabilityIdempotency(userId, m.ID, keyHash)
		if err != nil {
			respondCapabilityError(c, &capability.ExecutionError{
				Code:       "idempotency_failed",
				Message:    "could not claim idempotency key",
				HTTPStatus: http.StatusInternalServerError,
			})
			return
		}
		if !claimed {
			if row.TaskId == "" {
				c.JSON(http.StatusConflict, gin.H{
					"success":    false,
					"error_code": "task_in_progress",
					"message":    "a task for this idempotency key is being submitted",
				})
				return
			}
			task, err := loadCapabilityTask(row.TaskId, userId)
			if err != nil {
				respondCapabilityError(c, &capability.ExecutionError{
					Code:       "task_not_found",
					Message:    "task for this idempotency key was not found",
					HTTPStatus: http.StatusInternalServerError,
				})
				return
			}
			c.JSON(http.StatusOK, capabilityTaskPayload(task, true))
			return
		}
		claim = row
	}

	info, priceData, execErr := prepareCapabilityBilling(c, m, req.Input)
	if execErr != nil {
		dropCapabilityClaim(userId, m.ID, keyHash, claim)
		respondCapabilityError(c, execErr)
		return
	}

	submit, execErr := capability.SubmitAsync(c.Request.Context(), m, req.Input, req.IdempotencyKey)
	if execErr != nil {
		refundCapabilityBilling(c, info)
		dropCapabilityClaim(userId, m.ID, keyHash, claim)
		logCapabilityConsume(c, m, info, 0, "failed", execErr.Code, "", "call", 0, "async")
		respondCapabilityError(c, execErr)
		return
	}

	// Fixed per-call async tasks are charged at submit. The pilot price is zero;
	// per-unit async settlement will be added with the full task lifecycle.
	if err := service.SettleBilling(c, info, priceData.Quota); err != nil {
		refundCapabilityBilling(c, info)
		dropCapabilityClaim(userId, m.ID, keyHash, claim)
		logCapabilityConsume(c, m, info, 0, "failed", "billing_failed", submit.UpstreamRequestID, "call", 0, "async")
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "billing_failed",
			Message:    "billing settlement failed",
			HTTPStatus: http.StatusInternalServerError,
		})
		return
	}

	task := &model.Task{
		TaskID:     model.GenerateTaskID(),
		Platform:   constant.TaskPlatformCapability,
		UserId:     userId,
		Group:      info.UsingGroup,
		Quota:      priceData.Quota,
		Action:     m.ID,
		Status:     model.TaskStatusSubmitted,
		SubmitTime: time.Now().Unix(),
		Progress:   "0%",
		Properties: model.Properties{OriginModelName: m.ID},
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: submit.JobID,
			TokenId:        info.TokenId,
			BillingSource:  info.BillingSource,
		},
	}
	unit := m.Billing.Unit
	if m.Billing.Type == capability.BillingPerCall {
		unit = "call"
	}
	task.SetData(map[string]any{
		"input": req.Input,
		"billing": map[string]any{
			"pre_consumed": priceData.Quota,
			"price_usd":    m.Billing.PriceUSD,
			"unit":         unit,
			"group_ratio":  priceData.GroupRatioInfo.GroupRatio,
			"settled":      false,
		},
	})
	if err := model.DB.Create(task).Error; err != nil {
		refundCapabilityBilling(c, info)
		dropCapabilityClaim(userId, m.ID, keyHash, claim)
		logCapabilityConsume(c, m, info, 0, "failed", "task_persist_failed", submit.UpstreamRequestID, "call", 0, "async")
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "task_persist_failed",
			Message:    "could not persist task",
			HTTPStatus: http.StatusInternalServerError,
		})
		return
	}
	if claim != nil {
		if err := model.SetCapabilityIdempotencyTask(userId, m.ID, keyHash, task.TaskID); err != nil {
			// The task exists and is recoverable; do not hide it from the caller.
			common.SysLog(fmt.Sprintf("capability: failed to attach task to idempotency key: user=%d capability=%s", userId, m.ID))
		}
	}
	logUnit, logQuantity := capabilityEstimatedUsage(m, req.Input)
	logCapabilityConsume(c, m, info, priceData.Quota, "submitted", "", submit.UpstreamRequestID, logUnit, logQuantity, "async")
	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"id":      task.TaskID,
		"status":  "queued",
	})
}

func GetCapabilityTask(c *gin.Context) {
	task, err := loadCapabilityTask(c.Param("id"), c.GetInt("id"))
	if err != nil {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "task_not_found",
			Message:    "task was not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	m, ok := capability.Get(task.Action)
	if !ok || m.Mode != capability.ModeAsync {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "capability_not_found",
			Message:    "task capability is no longer available",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		c.JSON(http.StatusOK, capabilityTaskPayload(task, false))
		return
	}

	var data struct {
		Input   map[string]any `json:"input"`
		Output  any            `json:"output"`
		Billing struct {
			PreConsumed int     `json:"pre_consumed"`
			PriceUSD    float64 `json:"price_usd"`
			Unit        string  `json:"unit"`
			GroupRatio  float64 `json:"group_ratio"`
			Settled     bool    `json:"settled"`
		} `json:"billing"`
	}
	_ = task.GetData(&data)
	upstreamID := task.PrivateData.UpstreamTaskID
	if upstreamID == "" {
		upstreamID = task.TaskID
	}
	poll, execErr := capability.PollAsync(c.Request.Context(), m, data.Input, upstreamID)
	if execErr != nil {
		respondCapabilityError(c, execErr)
		return
	}
	if poll.Completed {
		task.FinishTime = time.Now().Unix()
		task.Progress = "100%"
		if poll.ErrorCode != "" {
			task.Status = model.TaskStatusFailure
			task.FailReason = "upstream task failed"
			refundCapabilityTask(task)
			data.Billing.Settled = true
			task.SetData(map[string]any{"input": data.Input, "billing": data.Billing})
			_ = model.DB.Save(task).Error
			c.JSON(http.StatusOK, capabilityTaskPayload(task, false))
			return
		}
		task.Status = model.TaskStatusSuccess
		if m.Billing.Type == capability.BillingPerUnit {
			priceData := &types.PriceData{
				ModelPrice:     data.Billing.PriceUSD,
				UsePrice:       true,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: data.Billing.GroupRatio},
			}
			actualQuota, _, _, usageErr := capabilitySyncUsage(m, priceData, poll.Raw)
			if usageErr != nil {
				task.Status = model.TaskStatusFailure
				task.FailReason = "missing usage"
				refundCapabilityTask(task)
				data.Billing.Settled = true
				task.SetData(map[string]any{"input": data.Input, "billing": data.Billing})
				_ = model.DB.Save(task).Error
				c.JSON(http.StatusOK, capabilityTaskPayload(task, false))
				return
			}
			settleCapabilityTaskDelta(task, task.Quota, actualQuota)
			task.Quota = actualQuota
		}
		data.Billing.Settled = true
		task.SetData(map[string]any{"input": data.Input, "output": poll.Result, "billing": data.Billing})
		_ = model.DB.Save(task).Error
		c.JSON(http.StatusOK, capabilityTaskPayload(task, false))
		return
	}

	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	_ = model.DB.Save(task).Error
	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"id":              task.TaskID,
		"status":          "running",
		"upstream_status": poll.Status,
	})
}

func GetCapabilityTaskContent(c *gin.Context) {
	task, err := loadCapabilityTask(c.Param("id"), c.GetInt("id"))
	if err != nil {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "task_not_found",
			Message:    "task was not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if task.Status != model.TaskStatusSuccess {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "task_not_completed",
			Message:    "task has not completed successfully",
			HTTPStatus: http.StatusConflict,
		})
		return
	}
	m, ok := capability.Get(task.Action)
	if !ok || m.Content == nil {
		respondCapabilityError(c, &capability.ExecutionError{
			Code:       "content_not_available",
			Message:    "this capability does not expose a content endpoint",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	var data struct {
		Input map[string]any `json:"input"`
	}
	_ = task.GetData(&data)
	upstreamID := task.PrivateData.UpstreamTaskID
	if upstreamID == "" {
		upstreamID = task.TaskID
	}
	raw, contentType, execErr := capability.FetchContent(c.Request.Context(), m, data.Input, upstreamID)
	if execErr != nil {
		respondCapabilityError(c, execErr)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, contentType, raw)
}

func prepareCapabilityBilling(c *gin.Context, m *capability.Manifest, input map[string]any) (*relaycommon.RelayInfo, *types.PriceData, *capability.ExecutionError) {
	common.SetContextKey(c, constant.ContextKeyOriginalModel, m.ID)
	info := relaycommon.GenRelayInfoOpenAI(c, nil)
	info.OriginModelName = m.ID
	group := relayhelper.HandleGroupRatio(c, info)

	preQuota := 0
	if m.Billing.Type == capability.BillingPerCall {
		preQuota = int(m.Billing.PriceUSD * float64(common.QuotaPerUnit) * group.GroupRatio)
	} else if m.Billing.Type == capability.BillingPerUnit && m.Billing.EstimatePath != "" {
		if v, ok := capability.Lookup(map[string]any{"input": input}, m.Billing.EstimatePath); ok {
			if qty, ok := capability.NumberValue(v); ok && qty > 0 {
				preQuota = int(m.Billing.PriceUSD * qty * float64(common.QuotaPerUnit) * group.GroupRatio)
			}
		}
	}
	priceData := &types.PriceData{
		ModelPrice:     m.Billing.PriceUSD,
		UsePrice:       true,
		Quota:          preQuota,
		GroupRatioInfo: group,
		FreeModel:      preQuota == 0,
	}
	info.PriceData = *priceData
	info.FinalPreConsumedQuota = preQuota

	// Free capabilities must work even for a zero-balance user; skip the
	// billing session entirely when the manifest price is zero.
	if preQuota == 0 && m.Billing.PriceUSD == 0 {
		return info, priceData, nil
	}

	if apiErr := service.PreConsumeBilling(c, preQuota, info); apiErr != nil {
		common.SysLog(fmt.Sprintf("capability billing pre-consume failed: capability=%s quota=%d error=%v", m.ID, preQuota, apiErr.Err))
		return nil, priceData, &capability.ExecutionError{
			Code:       "quota_insufficient",
			Message:    "insufficient quota or billing is unavailable",
			HTTPStatus: http.StatusPaymentRequired,
		}
	}
	return info, priceData, nil
}

func capabilitySyncUsage(m *capability.Manifest, priceData *types.PriceData, raw map[string]any) (int, string, float64, *capability.ExecutionError) {
	if m.Billing.Type == capability.BillingPerCall {
		return priceData.Quota, "call", 1, nil
	}
	value, ok := capability.Lookup(raw, m.Billing.QuantityPath)
	if !ok {
		return 0, m.Billing.Unit, 0, &capability.ExecutionError{
			Code:       "missing_usage",
			Message:    "upstream response did not contain the billing quantity",
			HTTPStatus: http.StatusBadGateway,
		}
	}
	quantity, ok := capability.NumberValue(value)
	if !ok || quantity < 0 {
		return 0, m.Billing.Unit, 0, &capability.ExecutionError{
			Code:       "invalid_usage",
			Message:    "upstream returned an invalid billing quantity",
			HTTPStatus: http.StatusBadGateway,
		}
	}
	quota := int(m.Billing.PriceUSD * quantity * float64(common.QuotaPerUnit) * priceData.GroupRatioInfo.GroupRatio)
	unit := m.Billing.Unit
	if unit == "" {
		unit = "unit"
	}
	return quota, unit, quantity, nil
}

func capabilityEstimatedUsage(m *capability.Manifest, input map[string]any) (string, float64) {
	if m.Billing.Type == capability.BillingPerCall {
		return "call", 1
	}
	unit := m.Billing.Unit
	if unit == "" {
		unit = "unit"
	}
	if m.Billing.EstimatePath != "" {
		if v, ok := capability.Lookup(map[string]any{"input": input}, m.Billing.EstimatePath); ok {
			if qty, ok := capability.NumberValue(v); ok {
				return unit, qty
			}
		}
	}
	return unit, 0
}

func refundCapabilityBilling(c *gin.Context, info *relaycommon.RelayInfo) {
	if info != nil && info.Billing != nil {
		info.Billing.Refund(c)
	}
}

func refundCapabilityTask(task *model.Task) {
	if task == nil || task.Quota <= 0 {
		return
	}
	result := model.DB.Model(&model.Task{}).
		Where("id = ? AND quota = ?", task.ID, task.Quota).
		Update("quota", 0)
	if result.Error != nil || result.RowsAffected == 0 {
		return
	}
	quota := task.Quota
	_ = model.IncreaseUserQuota(task.UserId, quota, false)
	if task.PrivateData.TokenId > 0 {
		if token, err := model.GetTokenById(task.PrivateData.TokenId); err == nil {
			_ = model.IncreaseTokenQuota(token.Id, token.Key, quota)
		}
	}
	task.Quota = 0
}

func settleCapabilityTaskDelta(task *model.Task, preQuota int, actualQuota int) {
	if task == nil || preQuota == actualQuota {
		return
	}
	// CAS on the stored quota prevents two concurrent status polls from
	// settling the same task twice.
	result := model.DB.Model(&model.Task{}).
		Where("id = ? AND quota = ?", task.ID, preQuota).
		Update("quota", actualQuota)
	if result.Error != nil || result.RowsAffected != 1 {
		return
	}
	delta := actualQuota - preQuota
	if delta > 0 {
		_ = model.DecreaseUserQuota(task.UserId, delta, false)
	} else {
		_ = model.IncreaseUserQuota(task.UserId, -delta, false)
	}
	if task.PrivateData.TokenId > 0 {
		if token, err := model.GetTokenById(task.PrivateData.TokenId); err == nil {
			if delta > 0 {
				_ = model.DecreaseTokenQuota(token.Id, token.Key, delta)
			} else {
				_ = model.IncreaseTokenQuota(token.Id, token.Key, -delta)
			}
		}
	}
}

func logCapabilityConsume(c *gin.Context, m *capability.Manifest, info *relaycommon.RelayInfo, quota int, status string, errCode string, upstreamID string, unit string, quantity float64, mode string) {
	if info == nil {
		return
	}
	c.Set("username", common.GetContextKeyString(c, constant.ContextKeyUserName))
	useTime := 0
	if !info.StartTime.IsZero() {
		useTime = int(time.Since(info.StartTime).Seconds())
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId:      0,
		ModelName:      m.ID,
		TokenName:      c.GetString("token_name"),
		Quota:          quota,
		Content:        fmt.Sprintf("capability %s %s", m.ID, status),
		TokenId:        info.TokenId,
		UseTimeSeconds: useTime,
		Group:          info.UsingGroup,
		Other: map[string]any{
			"capability_id":       m.ID,
			"provider":            m.Provider,
			"mode":                mode,
			"status":              status,
			"error_code":          errCode,
			"upstream_request_id": upstreamID,
			"usage_unit":          unit,
			"usage_quantity":      quantity,
		},
	})
}

func loadCapabilityTask(taskID string, userId int) (*model.Task, error) {
	var task model.Task
	err := model.DB.Where("task_id = ? AND user_id = ? AND platform = ?", taskID, userId, constant.TaskPlatformCapability).
		First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func capabilityTaskPayload(task *model.Task, replayed bool) gin.H {
	status := "running"
	switch task.Status {
	case model.TaskStatusSuccess:
		status = "succeeded"
	case model.TaskStatusFailure:
		status = "failed"
	case model.TaskStatusSubmitted, model.TaskStatusQueued:
		status = "queued"
	}
	payload := gin.H{
		"success":  true,
		"id":       task.TaskID,
		"status":   status,
		"cost_usd": quotaToUSD(task.Quota),
	}
	if replayed {
		payload["replayed"] = true
	}
	var data struct {
		Output any `json:"output"`
	}
	if err := task.GetData(&data); err == nil && data.Output != nil {
		payload["output"] = data.Output
	}
	if task.Status == model.TaskStatusFailure {
		payload["error_code"] = "upstream_task_failed"
		payload["message"] = "upstream task failed"
	}
	return payload
}

func respondCapabilityError(c *gin.Context, err *capability.ExecutionError) {
	if err == nil {
		err = &capability.ExecutionError{Code: "internal_error", Message: "internal error", HTTPStatus: http.StatusInternalServerError}
	}
	status := err.HTTPStatus
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	c.JSON(status, gin.H{
		"success":    false,
		"error_code": err.Code,
		"message":    err.Message,
	})
}

func hashCapabilityKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func dropCapabilityClaim(userId int, capabilityId string, keyHash string, claim *model.CapabilityIdempotency) {
	if claim == nil || keyHash == "" {
		return
	}
	_ = model.DeleteCapabilityIdempotency(userId, capabilityId, keyHash)
}

func quotaToUSD(quota int) float64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	return float64(quota) / float64(common.QuotaPerUnit)
}
