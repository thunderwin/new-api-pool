// Pool Management Controllers
//
// 6 个模块全部接真实数据库：
//   1. Overview      — 健康分 + 今日吞吐 + 分组实况（从 channels/logs 聚合）
//   2. Accounts      — 上游账号 CRUD（pool_accounts）
//   3. Recipes/Jobs  — 注册剧本配置 + 任务历史（pool_recipes / pool_jobs）
//   4. Risk Rules    — 已移除（new-api 主体已有 RPM/TPM/分组限速）
//   5. Billing       — 收支对账（topups + logs 聚合）
//   6. Alerts        — 告警历史 + 立即巡检 + Telegram 配置（pool_alert_history + option）

package controller

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// ─────────────────────────────────────────────────────────────
// 1. Overview — GET /api/pool/overview
// ─────────────────────────────────────────────────────────────

func GetPoolOverview(c *gin.Context) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	// 账号统计
	accTotal, accActive, accWarn, accDisabled, err := model.CountPoolAccountsByStatus()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 渠道统计
	var chTotal, chEnabled, chDisabled int64
	model.DB.Model(&model.Channel{}).Count(&chTotal)
	model.DB.Model(&model.Channel{}).Where("status = ?", common.ChannelStatusEnabled).Count(&chEnabled)
	model.DB.Model(&model.Channel{}).Where("status <> ?", common.ChannelStatusEnabled).Count(&chDisabled)

	// 今日日志聚合（消费 + 错误）
	var todayRequests, todayFailure int64
	var todayQuota int64
	model.LOG_DB.Model(&model.Log{}).
		Where("created_at >= ? AND type = ?", todayStart, model.LogTypeConsume).
		Count(&todayRequests)
	model.LOG_DB.Model(&model.Log{}).
		Where("created_at >= ? AND type = ?", todayStart, model.LogTypeError).
		Count(&todayFailure)
	model.LOG_DB.Model(&model.Log{}).
		Select("COALESCE(SUM(quota),0)").
		Where("created_at >= ? AND type = ?", todayStart, model.LogTypeConsume).
		Row().Scan(&todayQuota)
	todaySuccess := todayRequests - todayFailure
	if todaySuccess < 0 {
		todaySuccess = 0
	}

	// 今日收入（topups 表，仅成功状态）
	var todayRevenue float64
	model.DB.Model(&model.TopUp{}).
		Select("COALESCE(SUM(amount),0)").
		Where("create_time >= ? AND status = 'success'", todayStart).
		Row().Scan(&todayRevenue)

	// 用户消耗（quota）转 USD：1 USD = QuotaPerUnit
	todayConsumeUSD := float64(todayQuota) / common.QuotaPerUnit
	// 上游成本估算：先用配置项 PoolUpstreamCostRatio（默认 0.6 = 上游拿走 60%）
	upstreamRatio := getFloatOptionOrDefault("PoolUpstreamCostRatio", 0.6)
	if upstreamRatio < 0 || upstreamRatio > 1 {
		upstreamRatio = 0.6
	}
	todayCost := todayConsumeUSD * upstreamRatio
	todayProfit := todayConsumeUSD - todayCost

	// 健康分（0-100）：active/total 比例 - 失败率惩罚 - 渠道禁用率惩罚
	healthScore := 100
	if accTotal > 0 {
		activeRatio := float64(accActive) / float64(accTotal)
		healthScore = int(activeRatio * 70) // 占 70 分
	} else {
		healthScore = 60 // 无账号时给 60 基准
	}
	if todayRequests > 0 {
		failRate := float64(todayFailure) / float64(todayRequests)
		penalty := int(failRate * 30) // 失败率最多扣 30 分
		healthScore -= penalty
		healthScore += 30 // 同时加回满分基础（无失败时此项为 30 满分）
	} else {
		healthScore += 30 // 无请求时不扣
	}
	if chTotal > 0 && chDisabled > 0 {
		disabledRatio := float64(chDisabled) / float64(chTotal)
		healthScore -= int(disabledRatio * 10)
	}
	if healthScore < 0 {
		healthScore = 0
	}
	if healthScore > 100 {
		healthScore = 100
	}

	// 分组实况：按渠道 group 聚合
	type groupRow struct {
		Group    string `gorm:"column:group"`
		Channels int64
	}
	var groups []groupRow
	model.DB.Model(&model.Channel{}).
		Select(model.GroupCol() + " as " + model.GroupCol() + ", COUNT(*) as channels").
		Group(model.GroupCol()).
		Order("channels DESC").
		Limit(10).
		Scan(&groups)

	groupResp := make([]gin.H, 0, len(groups))
	for _, g := range groups {
		// 失败率：今日该 group 的 failure / requests
		var gReq, gFail int64
		model.LOG_DB.Model(&model.Log{}).
			Where("created_at >= ? AND type = ? AND "+model.LogGroupCol()+" = ?", todayStart, model.LogTypeConsume, g.Group).
			Count(&gReq)
		model.LOG_DB.Model(&model.Log{}).
			Where("created_at >= ? AND type = ? AND "+model.LogGroupCol()+" = ?", todayStart, model.LogTypeError, g.Group).
			Count(&gFail)
		failRate := 0.0
		if gReq > 0 {
			failRate = float64(gFail) / float64(gReq) * 100
		}
		// RPM/TPM：粗估 = 今日量 / 当前已过去的分钟
		minutes := float64(now.Unix()-todayStart) / 60
		if minutes < 1 {
			minutes = 1
		}
		rpm := int(float64(gReq) / minutes)
		groupResp = append(groupResp, gin.H{
			"name":      g.Group,
			"channels":  g.Channels,
			"rpm":       rpm,
			"tpm":       0, // 简化：暂不算 token-per-minute
			"fail_rate": fmt.Sprintf("%.2f", failRate),
		})
	}

	// 至少给 3 个分组占位（即使没有渠道）
	if len(groupResp) == 0 {
		groupResp = []gin.H{
			{"name": "default", "channels": 0, "rpm": 0, "tpm": 0, "fail_rate": "0.00"},
		}
	}

	common.ApiSuccess(c, gin.H{
		"health_score":      healthScore,
		"accounts_total":    accTotal,
		"accounts_active":   accActive,
		"accounts_warning":  accWarn,
		"accounts_disabled": accDisabled,
		"channels_total":    chTotal,
		"channels_enabled":  chEnabled,
		"channels_disabled": chDisabled,
		"today_requests":    todayRequests,
		"today_success":     todaySuccess,
		"today_failure":     todayFailure,
		"today_revenue":     fmt.Sprintf("%.2f", todayRevenue),
		"today_cost":        fmt.Sprintf("%.4f", todayCost),
		"today_profit":      fmt.Sprintf("%.4f", todayProfit),
		"upstream_ratio":    upstreamRatio,
		"groups":            groupResp,
		"updated_at":        now.Unix(),
	})
}

// ─────────────────────────────────────────────────────────────
// 2. Accounts — pool_accounts CRUD
// ─────────────────────────────────────────────────────────────

type poolAccountReq struct {
	Id          int     `json:"id"`
	Name        string  `json:"name"`
	Provider    string  `json:"provider"`
	AccountType string  `json:"account_type"`
	Status      int     `json:"status"`
	KeyRaw      string  `json:"key_raw"` // 新增/更新原始 Key；只用于同步到关联 Channel，不入 pool_accounts 表
	BalanceUSD  float64 `json:"balance_usd"`
	ExpireAt    int64   `json:"expire_at"`
	ChannelId   int     `json:"channel_id"`
	GroupName   string  `json:"group_name"`
	Notes       string  `json:"notes"`
	// 自动建渠道相关：当 channel_id=0 且 auto_create_channel=true，根据 channel_type/channel_base_url 自动新建 new-api 渠道
	AutoCreateChannel bool   `json:"auto_create_channel"`
	ChannelType       int    `json:"channel_type"`
	ChannelBaseURL    string `json:"channel_base_url"`
}

func GetPoolAccounts(c *gin.Context) {
	keyword := c.Query("keyword")
	provider := c.Query("provider")
	accountType := c.Query("account_type")
	status, _ := strconv.Atoi(c.Query("status"))
	pageInfo := common.GetPageQuery(c)

	items, total, err := model.SearchPoolAccounts(keyword, provider, accountType, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func GetPoolAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	a, err := model.GetPoolAccountByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, a)
}

func AddPoolAccount(c *gin.Context) {
	var req poolAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Name == "" {
		common.ApiErrorMsg(c, "账号名称不能为空")
		return
	}
	if req.Provider == "" {
		common.ApiErrorMsg(c, "请选择 Provider")
		return
	}

	channelId := req.ChannelId
	groupName := req.GroupName
	if groupName == "" {
		groupName = req.Provider
	}
	// 自动建渠道：channel_id=0 + auto_create_channel + key + channel_type 都给齐
	if channelId == 0 && req.AutoCreateChannel && req.KeyRaw != "" && req.ChannelType > 0 {
		poolTag := "pool"
		// 默认 group=default,<provider>，让 default 用户立即能路由 + 号池仍能聚合
		chGroup := "default"
		if groupName != "" && groupName != "default" {
			chGroup = "default," + groupName
		}
		// 尝试从同 ChannelType 的 recipe 取 DefaultModels（号池模式优先用剧本配置）
		var defaultModels string
		var anyRecipe model.PoolRecipe
		if err := model.DB.Where("channel_type = ? AND default_models <> ''",
			req.ChannelType).Order("id ASC").First(&anyRecipe).Error; err == nil {
			defaultModels = anyRecipe.DefaultModels
		}
		ch := &model.Channel{
			Type:        req.ChannelType,
			Key:         req.KeyRaw,
			Status:      common.ChannelStatusEnabled,
			Name:        req.Name,
			Group:       chGroup,
			Models:      defaultModels,
			CreatedTime: common.GetTimestamp(),
			Tag:         &poolTag,
		}
		if req.ChannelBaseURL != "" {
			base := req.ChannelBaseURL
			ch.BaseURL = &base
		}
		// channel.Insert() 会同步在 abilities 表写入对应模型，路由才能立即可用
		if err := ch.Insert(); err != nil {
			common.ApiErrorMsg(c, "自动创建渠道失败: "+err.Error())
			return
		}
		channelId = ch.Id
	}

	a := &model.PoolAccount{
		Name:        req.Name,
		Provider:    req.Provider,
		AccountType: req.AccountType,
		Status:      req.Status,
		KeyMasked:   model.MaskPoolAccountKey(req.KeyRaw),
		BalanceUSD:  req.BalanceUSD,
		ExpireAt:    req.ExpireAt,
		ChannelId:   channelId,
		GroupName:   groupName,
		Notes:       req.Notes,
	}
	if err := a.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, a)
}

func UpdatePoolAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req poolAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	a, err := model.GetPoolAccountByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Name != "" {
		a.Name = req.Name
	}
	if req.Provider != "" {
		a.Provider = req.Provider
	}
	if req.AccountType != "" {
		a.AccountType = req.AccountType
	}
	if req.Status > 0 {
		a.Status = req.Status
	}
	if req.KeyRaw != "" {
		a.KeyMasked = model.MaskPoolAccountKey(req.KeyRaw)
		// 同步到关联 Channel：用户在号池里"换 Key"时，新-api 那边对应渠道也要更新
		if a.ChannelId > 0 {
			var ch model.Channel
			if err := model.DB.First(&ch, a.ChannelId).Error; err == nil {
				ch.Key = req.KeyRaw
				_ = ch.Update()
			}
		}
	}
	a.BalanceUSD = req.BalanceUSD
	a.ExpireAt = req.ExpireAt
	a.ChannelId = req.ChannelId
	if req.GroupName != "" {
		a.GroupName = req.GroupName
	}
	a.Notes = req.Notes
	if err := a.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, a)
}

// BindPoolAccountChannel 一键绑定关联渠道
//
//	POST /api/pool/accounts/:id/bind
//	body: { channel_id?: number }
//
// 行为：
//   - body 带 channel_id 且 > 0：校验该 channel 存在 → 直接绑
//   - 不带 channel_id：自动按 name + provider→type 模糊查 channels：
//     1) 唯一命中 → 绑
//     2) 多个候选 → 返回 candidates 列表给前端让用户选
//     3) 0 个 → 提示去编辑账号录入 Key（走 auto_create_channel 流程）
func BindPoolAccountChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	a, err := model.GetPoolAccountByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var body struct {
		ChannelId int `json:"channel_id"`
	}
	_ = c.ShouldBindJSON(&body)

	if body.ChannelId > 0 {
		var ch model.Channel
		if err := model.DB.First(&ch, body.ChannelId).Error; err != nil {
			common.ApiErrorMsg(c, "未找到渠道 #"+strconv.Itoa(body.ChannelId))
			return
		}
		a.ChannelId = ch.Id
		if err := a.Update(); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{
			"bound":        true,
			"channel_id":   ch.Id,
			"channel_name": ch.Name,
			"channel_type": ch.Type,
		})
		return
	}

	// 自动匹配
	q := model.DB.Model(&model.Channel{})
	if a.Name != "" {
		q = q.Where("name LIKE ?", a.Name)
	}
	var candidates []model.Channel
	if err := q.Limit(20).Find(&candidates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	switch len(candidates) {
	case 0:
		common.ApiErrorMsg(c, "没找到可自动绑定的渠道。请到「编辑」里填 Key 并勾「同步建/绑渠道」自动建一条；或先在「渠道管理」建好渠道再回来手动指定 channel_id。")
		return
	case 1:
		ch := candidates[0]
		a.ChannelId = ch.Id
		if err := a.Update(); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{
			"bound":        true,
			"channel_id":   ch.Id,
			"channel_name": ch.Name,
			"channel_type": ch.Type,
		})
	default:
		// 多个候选 → 返回列表让前端选
		out := make([]gin.H, 0, len(candidates))
		for _, ch := range candidates {
			out = append(out, gin.H{
				"id":   ch.Id,
				"name": ch.Name,
				"type": ch.Type,
			})
		}
		common.ApiSuccess(c, gin.H{
			"bound":      false,
			"candidates": out,
			"hint":       "命中多个渠道，请在前端选择一个",
		})
	}
}

// GetPoolAccountFullKey 返回关联 Channel 的明文 Key（前端"复制完整 Key"按钮用）
//
//	GET /api/pool/accounts/:id/full_key
func GetPoolAccountFullKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	a, err := model.GetPoolAccountByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if a.ChannelId <= 0 {
		common.ApiErrorMsg(c, "该账号未关联渠道，无法查看完整 Key（请先在编辑里指定 channel_id 或勾选「自动建渠道」重新录入）")
		return
	}
	var ch model.Channel
	if err := model.DB.First(&ch, a.ChannelId).Error; err != nil {
		common.ApiErrorMsg(c, "未找到关联渠道 #"+strconv.Itoa(a.ChannelId)+": "+err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"channel_id":       ch.Id,
		"channel_name":     ch.Name,
		"channel_type":     ch.Type,
		"channel_base_url": ch.BaseURL,
		"key":              ch.Key,
	})
}

func DeletePoolAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	a, err := model.GetPoolAccountByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := a.Delete(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"deleted": true})
}

// ─────────────────────────────────────────────────────────────
// 3. Recipes & Jobs
// ─────────────────────────────────────────────────────────────

func GetPoolRecipes(c *gin.Context) {
	recipes, err := model.GetAllPoolRecipes()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	jobs, _, err := model.SearchPoolJobs("", "", 0, 50)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"recipes": recipes,
		"jobs":    jobs,
	})
}

type poolRecipeReq struct {
	Id                int    `json:"id"`
	Key               string `json:"key"`
	Name              string `json:"name"`
	Provider          string `json:"provider"`
	Version           string `json:"version"`
	ManualMode        *bool  `json:"manual_mode"`
	Description       string `json:"description"`
	DocURL            string `json:"doc_url"`
	RequiredMaterials string `json:"required_materials"`
	ManualSteps       string `json:"manual_steps"`
	OutputFormat      string `json:"output_format"`
	Difficulty        string `json:"difficulty"`
	IPRequirement     string `json:"ip_requirement"`
	ChannelType       int    `json:"channel_type"`
	ChannelBaseURL    string `json:"channel_base_url"`
	WebhookURL        string `json:"webhook_url"`
	ConfigJSON        string `json:"config_json"`
	Enabled           bool   `json:"enabled"`
}

func UpdatePoolRecipe(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req poolRecipeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	r, err := model.GetPoolRecipeByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Name != "" {
		r.Name = req.Name
	}
	if req.Description != "" {
		r.Description = req.Description
	}
	if req.WebhookURL != "" {
		r.WebhookURL = req.WebhookURL
	}
	if req.ConfigJSON != "" {
		r.ConfigJSON = req.ConfigJSON
	}
	if req.Version != "" {
		r.Version = req.Version
	}
	if req.DocURL != "" {
		r.DocURL = req.DocURL
	}
	if req.RequiredMaterials != "" {
		r.RequiredMaterials = req.RequiredMaterials
	}
	if req.ManualSteps != "" {
		r.ManualSteps = req.ManualSteps
	}
	if req.OutputFormat != "" {
		r.OutputFormat = req.OutputFormat
	}
	if req.Difficulty != "" {
		r.Difficulty = req.Difficulty
	}
	if req.IPRequirement != "" {
		r.IPRequirement = req.IPRequirement
	}
	if req.ChannelType > 0 {
		r.ChannelType = req.ChannelType
	}
	if req.ChannelBaseURL != "" {
		r.ChannelBaseURL = req.ChannelBaseURL
	}
	if req.ManualMode != nil {
		r.ManualMode = *req.ManualMode
	}
	r.Enabled = req.Enabled
	if err := r.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, r)
}

// EnqueuePoolRecipe 入队一个注册任务
//   - ManualMode = true: 创建 manual_pending Job，前端展示操作步骤，用户线下完成后调用 manual-result 回填
//   - ManualMode = false: 调用外部 worker webhook（需配置 WebhookURL）
func EnqueuePoolRecipe(c *gin.Context) {
	key := c.Param("key")
	r, err := model.GetPoolRecipeByKey(key)
	if err != nil {
		common.ApiErrorMsg(c, "未找到 Recipe: "+key)
		return
	}
	if !r.Enabled {
		common.ApiErrorMsg(c, "该 Recipe 未启用，请先在「编辑」中启用")
		return
	}

	var paramsBody map[string]any
	_ = c.ShouldBindJSON(&paramsBody)
	paramsJSON, _ := json.Marshal(paramsBody)

	job := &model.PoolJob{
		RecipeKey:  r.Key,
		ParamsJSON: string(paramsJSON),
		OperatorId: getUserId(c),
	}

	if r.ManualMode {
		job.Status = model.PoolJobStatusManualPending
		if err := job.Insert(); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{
			"job_id":           job.Id,
			"mode":             "manual",
			"status":           job.Status,
			"manual_steps":     r.ManualSteps,
			"required":         r.RequiredMaterials,
			"doc_url":          r.DocURL,
			"output_format":    r.OutputFormat,
			"difficulty":       r.Difficulty,
			"ip_requirement":   r.IPRequirement,
			"channel_type":     r.ChannelType,
			"channel_base_url": r.ChannelBaseURL,
			"hint":             "请按照操作步骤线下完成注册，得到 Key/Token 后回到「Job 列表」点「录入结果」",
		})
		return
	}

	// 自动模式入队优先级：
	//   1) 已注册内部 Runner（service.GetRunner）→ 由本机 Worker pool_worker.go 直接跑
	//   2) 没 Runner 但配了 WebhookURL  → 退回外部 worker 模式
	//   3) 都没有 → 报错让用户开 ManualMode 或填 webhook
	hasInternal := service.GetRunner(r.Key) != nil
	if !hasInternal && r.WebhookURL == "" {
		common.ApiErrorMsg(c,
			"该 Recipe 自动模式无法运行：未注册内部 Runner，也未配 Webhook URL。"+
				"建议在「编辑」中开启「半自动模式」走人工流程；或为该 Recipe 实现 Runner / 配置外部 Webhook。")
		return
	}
	job.Status = model.PoolJobStatusPending
	if err := job.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	if !hasInternal {
		go service.NotifyExternalRecipeWorker(r, job)
	}
	mode := "auto-internal"
	if !hasInternal {
		mode = "auto-webhook"
	}
	common.ApiSuccess(c, gin.H{"job_id": job.Id, "mode": mode, "queued": true})
}

// ManualSubmitJobResult 手动模式回填结果
//   POST /api/pool/jobs/:id/manual-result
//   body: { account_name, key_raw, balance_usd, expire_at, notes, create_channel(bool), group_name }
//
// 行为：
//  1) 在 pool_accounts 表插入一条记录（Key 仅以脱敏串入库）
//  2) 若 create_channel=true 且 Recipe.ChannelType>0，自动在 channels 表新建一条
//  3) 更新 Job 状态为 success，写 result_json，并把 Recipe.success_count + 1
func ManualSubmitJobResult(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var job model.PoolJob
	if err := model.DB.First(&job, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if job.Status != model.PoolJobStatusManualPending {
		common.ApiErrorMsg(c, "该任务状态非 manual_pending，无法手动回填")
		return
	}
	r, err := model.GetPoolRecipeByKey(job.RecipeKey)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req struct {
		AccountName   string  `json:"account_name"`
		KeyRaw        string  `json:"key_raw"`
		BalanceUSD    float64 `json:"balance_usd"`
		ExpireAt      int64   `json:"expire_at"`
		Notes         string  `json:"notes"`
		GroupName     string  `json:"group_name"`
		CreateChannel bool    `json:"create_channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.AccountName == "" || req.KeyRaw == "" {
		common.ApiErrorMsg(c, "account_name 和 key_raw 不能为空")
		return
	}
	groupName := req.GroupName
	if groupName == "" {
		groupName = "default"
	}

	// 1) 写 pool_accounts
	acc := &model.PoolAccount{
		Name:        req.AccountName,
		Provider:    r.Provider,
		AccountType: model.PoolAccountTypeOfficial,
		Status:      model.PoolAccountStatusActive,
		KeyMasked:   model.MaskPoolAccountKey(req.KeyRaw),
		BalanceUSD:  req.BalanceUSD,
		ExpireAt:    req.ExpireAt,
		GroupName:   groupName,
		Notes:       fmt.Sprintf("由 Recipe %s 注册产生。%s", r.Key, req.Notes),
	}
	if err := acc.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}

	// 2) 可选：自动建 channel（持有完整原始 key）
	createdChannelId := 0
	if req.CreateChannel && r.ChannelType > 0 {
		poolTag := "pool-manual"
		// channel.Group 永远写成 "default,<provider_group>"，确保 default 用户立即能路由到，
		// 同时号池/账单按 provider group 还能聚合统计
		chGroup := "default"
		if groupName != "" && groupName != "default" {
			chGroup = "default," + groupName
		}
		models := r.DefaultModels
		ch := &model.Channel{
			Type:        r.ChannelType,
			Key:         req.KeyRaw,
			Status:      common.ChannelStatusEnabled,
			Name:        req.AccountName,
			CreatedTime: common.GetTimestamp(),
			Group:       chGroup,
			Models:      models,
			Tag:         &poolTag,
		}
		if r.ChannelBaseURL != "" {
			base := r.ChannelBaseURL
			ch.BaseURL = &base
		}
		// channel.Insert() 会同步注册 abilities，路由立即可用
		if err := ch.Insert(); err != nil {
			common.SysLog("[pool] create channel failed: " + err.Error())
		} else {
			createdChannelId = ch.Id
			acc.ChannelId = ch.Id
			_ = acc.Update()
		}
	}

	// 3) 关闭 Job，统计成功
	resultJSON, _ := json.Marshal(map[string]any{
		"pool_account_id": acc.Id,
		"channel_id":      createdChannelId,
		"by_user":         getUserId(c),
	})
	_ = job.UpdateStatus(model.PoolJobStatusSuccess, string(resultJSON), "")
	model.DB.Model(&model.PoolRecipe{}).Where(model.KeyCol()+" = ?", r.Key).
		UpdateColumn("success_count", model.DB.Raw("success_count + 1"))

	common.ApiSuccess(c, gin.H{
		"pool_account_id": acc.Id,
		"channel_id":      createdChannelId,
		"job_status":      model.PoolJobStatusSuccess,
	})
}

// GetPoolWorkerStatus 返回全自动 Worker 运行时状态
//   - running: 是否启动
//   - max_concurrent: 并发上限
//   - inflight: 当前运行中的 Job 数
//   - runner_keys: 已注册的全自动 Runner key 列表
func GetPoolWorkerStatus(c *gin.Context) {
	common.ApiSuccess(c, service.GetPoolWorkerStatus())
}

// GetPoolAutomationConfig 拿前端「自动化设置」Modal 所需的所有数据：
//   - email_providers: 已实现的邮箱 provider 列表
//   - sms_providers:   已实现的 SMS 接码 provider 列表
//   - current:         OptionMap 当前各 key 的值（api key 字段做掩码）
//   - sms_balance:     如果配了 SMS provider，返回当前账号余额（USD）
func GetPoolAutomationConfig(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	emailProv := common.OptionMap["PoolEmailProvider"]
	smsProv := common.OptionMap["PoolSmsProvider"]
	smsActKey := common.OptionMap["PoolSmsActivateApiKey"]
	fivesimKey := common.OptionMap["PoolFivesimApiKey"]
	common.OptionMapRWMutex.RUnlock()

	var smsBalance float64 = -1
	smsUnit := "USD"
	smsName := ""
	if p := service.GetSmsProvider(); p != nil {
		smsName = p.Name()
		if smsName == "5sim" {
			smsUnit = "RUB"
		}
		if smsName == "mock" {
			smsUnit = "MOCK"
		}
		if b, err := p.Balance(); err == nil {
			smsBalance = b
		}
	}

	common.ApiSuccess(c, gin.H{
		"email_providers": service.ListEmailProviders(),
		"sms_providers":   service.ListSmsProviders(),
		"runner_keys":     service.ListRunnerKeys(),
		"current": gin.H{
			"PoolEmailProvider":     emailProv,
			"PoolSmsProvider":       smsProv,
			"PoolSmsActivateApiKey": maskAPIKey(smsActKey),
			"PoolFivesimApiKey":     maskAPIKey(fivesimKey),
		},
		"sms_balance_usd":  smsBalance,
		"sms_balance_unit": smsUnit,
		"sms_provider":     smsName,
	})
}

// SetPoolAutomationConfig 保存「自动化设置」Modal 提交的值
// 仅当传入的字段不为 "[unchanged]" 时才更新（避免被掩码值覆盖）
func SetPoolAutomationConfig(c *gin.Context) {
	var req struct {
		PoolEmailProvider     *string `json:"PoolEmailProvider"`
		PoolSmsProvider       *string `json:"PoolSmsProvider"`
		PoolSmsActivateApiKey *string `json:"PoolSmsActivateApiKey"`
		PoolFivesimApiKey     *string `json:"PoolFivesimApiKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	updates := map[string]string{}
	if req.PoolEmailProvider != nil {
		updates["PoolEmailProvider"] = *req.PoolEmailProvider
	}
	if req.PoolSmsProvider != nil {
		updates["PoolSmsProvider"] = *req.PoolSmsProvider
	}
	if req.PoolSmsActivateApiKey != nil && *req.PoolSmsActivateApiKey != "[unchanged]" {
		updates["PoolSmsActivateApiKey"] = *req.PoolSmsActivateApiKey
	}
	if req.PoolFivesimApiKey != nil && *req.PoolFivesimApiKey != "[unchanged]" {
		updates["PoolFivesimApiKey"] = *req.PoolFivesimApiKey
	}
	for k, v := range updates {
		if err := model.UpdateOption(k, v); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, gin.H{"updated": len(updates)})
}

// maskAPIKey 把 api key 转成 ***xxxx 形式（只露最后 4 位），空串返回空串
func maskAPIKey(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// RetryPoolJob 重试一个失败 / 取消的任务（拷贝 params + recipe 创建新 Job）
func RetryPoolJob(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var src model.PoolJob
	if err := model.DB.First(&src, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	r, err := model.GetPoolRecipeByKey(src.RecipeKey)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	newJob := &model.PoolJob{
		RecipeKey:  src.RecipeKey,
		ParamsJSON: src.ParamsJSON,
		OperatorId: getUserId(c),
	}
	if r.ManualMode {
		newJob.Status = model.PoolJobStatusManualPending
	} else {
		newJob.Status = model.PoolJobStatusPending
	}
	if err := newJob.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"job_id": newJob.Id,
		"status": newJob.Status,
		"mode":   ifElse(r.ManualMode, "manual", "auto"),
	})
}

func ifElse(b bool, a, c string) string {
	if b {
		return a
	}
	return c
}

// CancelPoolJob 取消任务
func CancelPoolJob(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var job model.PoolJob
	if err := model.DB.First(&job, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if job.Status == model.PoolJobStatusSuccess || job.Status == model.PoolJobStatusFailed {
		common.ApiErrorMsg(c, "任务已结束")
		return
	}
	_ = job.UpdateStatus(model.PoolJobStatusCancel, "", "manually cancelled")
	common.ApiSuccess(c, gin.H{"cancelled": true})
}

// ReceivePoolJobCallback 外部 worker 回调更新 Job 状态
// POST /api/pool/jobs/:id/callback?token=<shared_secret>
func ReceivePoolJobCallback(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expected := common.OptionMap["PoolWorkerSharedToken"]
	if expected != "" && c.Query("token") != expected {
		common.ApiErrorMsg(c, "invalid worker token")
		return
	}
	var body struct {
		Status     string `json:"status"`
		ResultJSON string `json:"result_json"`
		ErrorMsg   string `json:"error_msg"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		common.ApiError(c, err)
		return
	}
	var job model.PoolJob
	if err := model.DB.First(&job, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := job.UpdateStatus(body.Status, body.ResultJSON, body.ErrorMsg); err != nil {
		common.ApiError(c, err)
		return
	}
	// 成功统计 + 1
	if body.Status == model.PoolJobStatusSuccess {
		model.DB.Model(&model.PoolRecipe{}).Where(model.KeyCol()+" = ?", job.RecipeKey).
			UpdateColumn("success_count", model.DB.Raw("success_count + 1"))
	} else if body.Status == model.PoolJobStatusFailed {
		model.DB.Model(&model.PoolRecipe{}).Where(model.KeyCol()+" = ?", job.RecipeKey).
			UpdateColumn("failure_count", model.DB.Raw("failure_count + 1"))
	}
	common.ApiSuccess(c, gin.H{"updated": true})
}

// ─────────────────────────────────────────────────────────────
// 4. Billing — 收支对账
//   （原「风控规则」按业务诉求已移除：new-api 已有 RPM/TPM/分组限速等机制，号池内重复造轮子价值低）
// ─────────────────────────────────────────────────────────────

func GetPoolBillingSummary(c *gin.Context) {
	period := c.DefaultQuery("period", "today")
	now := time.Now()
	var startTs int64
	switch period {
	case "7d":
		startTs = now.AddDate(0, 0, -7).Unix()
	case "30d":
		startTs = now.AddDate(0, 0, -30).Unix()
	case "today":
		fallthrough
	default:
		startTs = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
		period = "today"
	}

	// 收入
	var revenue float64
	model.DB.Model(&model.TopUp{}).
		Select("COALESCE(SUM(amount),0)").
		Where("create_time >= ? AND status = 'success'", startTs).
		Row().Scan(&revenue)

	// 用户消耗（按 quota 转 USD）
	var consumeQuota int64
	model.LOG_DB.Model(&model.Log{}).
		Select("COALESCE(SUM(quota),0)").
		Where("created_at >= ? AND type = ?", startTs, model.LogTypeConsume).
		Row().Scan(&consumeQuota)
	consumeUSD := float64(consumeQuota) / common.QuotaPerUnit

	upstreamRatio := getFloatOptionOrDefault("PoolUpstreamCostRatio", 0.6)
	upstreamCost := consumeUSD * upstreamRatio
	grossProfit := consumeUSD - upstreamCost
	margin := 0.0
	if consumeUSD > 0 {
		margin = grossProfit / consumeUSD * 100
	}

	// 按模型拆解（top 10）
	type modelRow struct {
		ModelName string `gorm:"column:model_name"`
		Quota     int64  `gorm:"column:quota"`
		Requests  int64  `gorm:"column:requests"`
	}
	var byModel []modelRow
	model.LOG_DB.Model(&model.Log{}).
		Select("model_name, COALESCE(SUM(quota),0) as quota, COUNT(*) as requests").
		Where("created_at >= ? AND type = ?", startTs, model.LogTypeConsume).
		Group("model_name").
		Order("quota DESC").
		Limit(10).
		Scan(&byModel)
	byModelResp := make([]gin.H, 0, len(byModel))
	for _, m := range byModel {
		usd := float64(m.Quota) / common.QuotaPerUnit
		byModelResp = append(byModelResp, gin.H{
			"model_name":   m.ModelName,
			"requests":     m.Requests,
			"consume_usd":  fmt.Sprintf("%.4f", usd),
			"upstream_usd": fmt.Sprintf("%.4f", usd*upstreamRatio),
			"profit_usd":   fmt.Sprintf("%.4f", usd*(1-upstreamRatio)),
		})
	}

	// 按分组拆解
	type groupRow struct {
		Group    string `gorm:"column:group"`
		Quota    int64  `gorm:"column:quota"`
		Requests int64  `gorm:"column:requests"`
	}
	var byGroup []groupRow
	model.LOG_DB.Model(&model.Log{}).
		Select(model.LogGroupCol() + " as " + model.LogGroupCol() + ", COALESCE(SUM(quota),0) as quota, COUNT(*) as requests").
		Where("created_at >= ? AND type = ?", startTs, model.LogTypeConsume).
		Group(model.LogGroupCol()).
		Order("quota DESC").
		Limit(10).
		Scan(&byGroup)
	byGroupResp := make([]gin.H, 0, len(byGroup))
	for _, g := range byGroup {
		usd := float64(g.Quota) / common.QuotaPerUnit
		byGroupResp = append(byGroupResp, gin.H{
			"group":        g.Group,
			"requests":     g.Requests,
			"consume_usd":  fmt.Sprintf("%.4f", usd),
			"upstream_usd": fmt.Sprintf("%.4f", usd*upstreamRatio),
			"profit_usd":   fmt.Sprintf("%.4f", usd*(1-upstreamRatio)),
		})
	}

	common.ApiSuccess(c, gin.H{
		"period":         period,
		"start_ts":       startTs,
		"revenue":        fmt.Sprintf("%.2f", revenue),
		"consume_usd":    fmt.Sprintf("%.4f", consumeUSD),
		"upstream_cost":  fmt.Sprintf("%.4f", upstreamCost),
		"gross_profit":   fmt.Sprintf("%.4f", grossProfit),
		"margin_percent": fmt.Sprintf("%.2f", margin),
		"upstream_ratio": upstreamRatio,
		"by_model":       byModelResp,
		"by_group":       byGroupResp,
	})
}

// ─────────────────────────────────────────────────────────────
// 6. Alerts — 告警历史 + 立即巡检 + Telegram 配置
// ─────────────────────────────────────────────────────────────

func GetPoolAlerts(c *gin.Context) {
	severity := c.Query("severity")
	var resolvedPtr *bool
	if r := c.Query("resolved"); r != "" {
		v := r == "true" || r == "1"
		resolvedPtr = &v
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := model.SearchPoolAlertHistory(severity, resolvedPtr, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	openCount, _ := model.CountPoolAlertOpen()
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, gin.H{
		"history":    pageInfo,
		"open_count": openCount,
		"telegram_set": common.OptionMap["PoolTelegramBotToken"] != "" &&
			common.OptionMap["PoolTelegramChatId"] != "",
	})
}

func TriggerPoolHealthCheck(c *gin.Context) {
	go service.RunPoolHealthCheck("manual")
	common.ApiSuccess(c, gin.H{"started": true})
}

func ResolvePoolAlert(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var h model.PoolAlertHistory
	if err := model.DB.First(&h, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := h.Resolve(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"resolved": true})
}

// GetPoolTelegramConfig 读取 Telegram 配置（脱敏 Token）
func GetPoolTelegramConfig(c *gin.Context) {
	token := common.OptionMap["PoolTelegramBotToken"]
	chatId := common.OptionMap["PoolTelegramChatId"]
	tokenMasked := ""
	if len(token) > 10 {
		tokenMasked = token[:6] + "..." + token[len(token)-4:]
	} else if token != "" {
		tokenMasked = "****"
	}
	common.ApiSuccess(c, gin.H{
		"token_masked": tokenMasked,
		"chat_id":      chatId,
		"enabled":      token != "" && chatId != "",
	})
}

func SetPoolTelegramConfig(c *gin.Context) {
	var req struct {
		Token  string `json:"token"`
		ChatId string `json:"chat_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	// 不修改原 token 时传空
	if req.Token != "" {
		_ = model.UpdateOption("PoolTelegramBotToken", req.Token)
	}
	_ = model.UpdateOption("PoolTelegramChatId", req.ChatId)
	common.ApiSuccess(c, gin.H{"ok": true})
}

// TestPoolTelegram 发送一条测试消息
func TestPoolTelegram(c *gin.Context) {
	token := common.OptionMap["PoolTelegramBotToken"]
	chatId := common.OptionMap["PoolTelegramChatId"]
	if token == "" || chatId == "" {
		common.ApiErrorMsg(c, "Telegram 未配置")
		return
	}
	if err := service.SendTelegramMessage(token, chatId, "✅ new-api 号池告警通道测试成功"); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"sent": true})
}

// ─────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────

func getFloatOptionOrDefault(key string, def float64) float64 {
	v := common.OptionMap[key]
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getUserId(c *gin.Context) int {
	if v, ok := c.Get("id"); ok {
		if id, ok := v.(int); ok {
			return id
		}
	}
	return 0
}
