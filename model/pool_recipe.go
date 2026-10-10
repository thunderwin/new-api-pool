// Pool Management — Recipe（注册剧本）+ Job（执行任务）
//
// 设计要点：
//   1. 大部分商用 AI 服务（OpenAI / Anthropic / Gemini / Codex / xAI ...）注册流程包含：
//      手机/邮箱验证、CAPTCHA、信用卡、设备指纹、IP 信誉。无人值守的全自动批量注册
//      在合规和稳定性上都不可行。所以 Recipe 默认走 ManualMode = true，
//      由系统给出操作清单 + 文档链接 + 必备材料，用户线下完成后回填得到的 Key/Token。
//   2. 少数允许自动化的服务（如内网/自建模型注册站点）可以把 ManualMode 设为 false +
//      配置 WebhookURL，由独立外部 worker（自带住宅代理 + 接码服务）执行后回调。

package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	PoolJobStatusPending       = "pending"        // 已创建，待处理
	PoolJobStatusManualPending = "manual_pending" // 等待用户线下操作 + 回填结果
	PoolJobStatusRunning       = "running"        // 外部 worker 自动执行中
	PoolJobStatusSuccess       = "success"        // 已完成（已建账号/渠道）
	PoolJobStatusFailed        = "failed"
	PoolJobStatusCancel        = "cancelled"

	PoolRecipeDifficultyEasy   = "easy"
	PoolRecipeDifficultyMedium = "medium"
	PoolRecipeDifficultyHard   = "hard"
)

type PoolRecipe struct {
	Id       int    `json:"id"`
	Key      string `json:"key" gorm:"size:64;not null;uniqueIndex:uk_pool_recipe_key_delete_at,priority:1"`
	Name     string `json:"name" gorm:"size:128;not null"`
	Provider string `json:"provider" gorm:"size:64;index"`
	Version  string `json:"version" gorm:"size:32;default:'0.1.0'"`
	// 半自动模式：true = 系统只跟踪流程，由用户线下操作并回填；false = 走全自动 Worker (chromedp + mail.tm)
	// 注意：这里不设 default，避免 GORM 把 seed 传入的 false 当成 zero-value 用 default 覆盖。
	// 实际默认行为由 buildDefaultRecipeSeeds 显式给值。
	ManualMode  bool   `json:"manual_mode"`
	Description string `json:"description" gorm:"type:text"`
	// DocURL 注册地址 / 文档
	DocURL string `json:"doc_url" gorm:"size:512"`
	// RequiredMaterials JSON 数组（["邮箱", "境外手机号", "海外信用卡"]）
	RequiredMaterials string `json:"required_materials" gorm:"type:text"`
	// ManualSteps 操作步骤（每步一行的纯文本，前端按 \n 拆分）
	ManualSteps string `json:"manual_steps" gorm:"type:text"`
	// OutputFormat 注册成功后得到的 key 形态说明，如 "sk-xxx (51 chars)"
	OutputFormat string `json:"output_format" gorm:"size:128"`
	// Difficulty easy / medium / hard
	Difficulty string `json:"difficulty" gorm:"size:16;default:'medium'"`
	// IPRequirement: any / residential / specific_country (e.g. "us")
	IPRequirement string `json:"ip_requirement" gorm:"size:32;default:'any'"`
	// AutomationCapability: 自动化可行性评级（仅作为信息字段，给前端/用户决策）
	//   full          : 全自动可行（仅需邮箱，已有 Runner 实现）
	//   sms_paid      : 需要付费接码服务才能自动（已有 Runner，靠 5sim/sms-activate）
	//   oauth_only    : 必须 Google/GitHub OAuth，永远手动
	//   realname_only : 必须实名 + 国内手机号，永远手动
	//   manual        : 其他纯手动场景
	AutomationCapability string `json:"automation_capability" gorm:"size:32;default:'manual'"`
	// SmsCountry / SmsProduct / SmsOperator: 当 Runner 需要接码时，喂给 SmsProvider 的参数
	//   - 5sim 文档示例: country=usa, operator=any, product=openai|claudeai|google|microsoft
	//   - sms-activate 数字 country/service 码（具体看 sms-activate 文档）
	//   - 空值表示由 Runner 内部决定默认值
	SmsCountry  string `json:"sms_country" gorm:"size:32"`
	SmsOperator string `json:"sms_operator" gorm:"size:32"`
	SmsProduct  string `json:"sms_product" gorm:"size:64"`
	// 自动建渠道：成功后自动在 channels 表里建一个 type=ChannelType 的渠道
	ChannelType    int    `json:"channel_type" gorm:"default:0"`
	ChannelBaseURL string `json:"channel_base_url" gorm:"size:255"`
	// DefaultModels 自动建渠道时填充的 channel.models 字段（逗号分隔）。
	// 留空则建出来的 channel 没有 models，需要用户在「渠道管理」→「获取模型列表」补全。
	DefaultModels string `json:"default_models" gorm:"type:text"`

	// 自动模式（ManualMode=false）相关
	WebhookURL string `json:"webhook_url" gorm:"size:512"`
	ConfigJSON string `json:"config_json" gorm:"type:text"`

	Enabled      bool           `json:"enabled" gorm:"default:false;index"`
	SuccessCount int            `json:"success_count" gorm:"default:0"`
	FailureCount int            `json:"failure_count" gorm:"default:0"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime  int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index;uniqueIndex:uk_pool_recipe_key_delete_at,priority:2"`
}

func (r *PoolRecipe) Insert() error {
	now := common.GetTimestamp()
	r.CreatedTime = now
	r.UpdatedTime = now
	return DB.Create(r).Error
}

// GetSmsCountry / GetSmsOperator / GetSmsProduct: 满足 service.acquireSmsForRecipe 的接口约束，
// 让 service 层可以在不导入 model.PoolRecipe 字段的前提下读取 SMS 路由配置。
func (r *PoolRecipe) GetSmsCountry() string  { return r.SmsCountry }
func (r *PoolRecipe) GetSmsOperator() string { return r.SmsOperator }
func (r *PoolRecipe) GetSmsProduct() string  { return r.SmsProduct }

func (r *PoolRecipe) Update() error {
	r.UpdatedTime = common.GetTimestamp()
	return DB.Save(r).Error
}

func (r *PoolRecipe) Delete() error {
	return DB.Delete(r).Error
}

func GetPoolRecipeByID(id int) (*PoolRecipe, error) {
	var r PoolRecipe
	err := DB.First(&r, id).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func GetPoolRecipeByKey(key string) (*PoolRecipe, error) {
	var r PoolRecipe
	// 不要用反引号包标识符：PostgreSQL 把 ` 当运算符字符，`key` = ? 会被解析成
	// 「运算符 key 运算符 = ?」→ syntax error at or near "="。
	// 用 map 条件让 GORM 按当前方言自己加引号（PG → "key"，MySQL → `key`）。
	err := DB.Where(map[string]interface{}{"key": key}).First(&r).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func GetAllPoolRecipes() ([]*PoolRecipe, error) {
	var items []*PoolRecipe
	err := DB.Order("provider ASC, id ASC").Find(&items).Error
	return items, err
}

// PoolJob 单次注册任务
type PoolJob struct {
	Id          int    `json:"id"`
	RecipeKey   string `json:"recipe_key" gorm:"size:64;not null;index"`
	Status      string `json:"status" gorm:"size:16;default:'pending';index"`
	ParamsJSON  string `json:"params_json" gorm:"type:text"`
	ResultJSON  string `json:"result_json" gorm:"type:text"`
	ErrorMsg    string `json:"error_msg" gorm:"type:text"`
	StartedAt   int64  `json:"started_at" gorm:"bigint;default:0"`
	FinishedAt  int64  `json:"finished_at" gorm:"bigint;default:0"`
	OperatorId  int    `json:"operator_id" gorm:"default:0"`
	CreatedTime int64  `json:"created_time" gorm:"bigint;index"`
}

func (j *PoolJob) Insert() error {
	if j.CreatedTime == 0 {
		j.CreatedTime = common.GetTimestamp()
	}
	if j.Status == "" {
		j.Status = PoolJobStatusPending
	}
	return DB.Create(j).Error
}

func (j *PoolJob) UpdateStatus(status string, resultJSON, errMsg string) error {
	j.Status = status
	if resultJSON != "" {
		j.ResultJSON = resultJSON
	}
	if errMsg != "" {
		j.ErrorMsg = errMsg
	}
	now := common.GetTimestamp()
	if status == PoolJobStatusRunning && j.StartedAt == 0 {
		j.StartedAt = now
	}
	if status == PoolJobStatusSuccess || status == PoolJobStatusFailed || status == PoolJobStatusCancel {
		j.FinishedAt = now
	}
	return DB.Save(j).Error
}

// SearchPoolJobs 列表 / 过滤
func SearchPoolJobs(recipeKey, status string, offset, limit int) ([]*PoolJob, int64, error) {
	db := DB.Model(&PoolJob{})
	if recipeKey != "" {
		db = db.Where("recipe_key = ?", recipeKey)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []*PoolJob
	if err := db.Offset(offset).Limit(limit).Order("id DESC").Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// upsertPoolRecipe 按 key 找：不存在则插入，存在则只更新"非用户配置"的字段
// （description / doc_url / manual_steps / required_materials / output_format / difficulty / ip_requirement / channel_type / channel_base_url / automation_capability）。
// 用户已经修改的 enabled / webhook_url / config_json / version 一律保留。
func upsertPoolRecipe(seed PoolRecipe) error {
	existing, err := GetPoolRecipeByKey(seed.Key)
	if err != nil {
		return seed.Insert()
	}
	existing.Name = seed.Name
	existing.Provider = seed.Provider
	existing.Description = seed.Description
	existing.DocURL = seed.DocURL
	existing.RequiredMaterials = seed.RequiredMaterials
	existing.ManualSteps = seed.ManualSteps
	existing.OutputFormat = seed.OutputFormat
	existing.Difficulty = seed.Difficulty
	existing.IPRequirement = seed.IPRequirement
	existing.AutomationCapability = seed.AutomationCapability
	existing.SmsCountry = seed.SmsCountry
	existing.SmsOperator = seed.SmsOperator
	existing.SmsProduct = seed.SmsProduct
	existing.ChannelType = seed.ChannelType
	if existing.ChannelBaseURL == "" {
		existing.ChannelBaseURL = seed.ChannelBaseURL
	}
	// DefaultModels: 用户没改时跟着 seed 同步（用 ConfigJSON 长度近似判断"用户改过没"）
	if existing.DefaultModels == "" {
		existing.DefaultModels = seed.DefaultModels
	}
	// 当 recipe 还没被运行过、且没配 webhook，认为用户没改过 ManualMode，
	// 跟着 seed 同步（修复早期 GORM default 陷阱遗留的错误值）
	if existing.SuccessCount == 0 && existing.FailureCount == 0 && existing.WebhookURL == "" {
		existing.ManualMode = seed.ManualMode
	}
	return existing.Update()
}

// SeedDefaultPoolRecipes 内置主流 AI 服务的注册剧本
// upsert 模式：每次启动会保证元数据（doc/steps/materials）跟代码一致；
// 用户已修改的 enabled / webhook / config / version 不被覆盖。
func SeedDefaultPoolRecipes() error {
	// 自愈：清掉历史遗留的重复配方（同 key 只留 id 最小的一条）。
	//
	// 为什么会有重复：早期 GetPoolRecipeByKey 用了反引号，PostgreSQL 下语法错误 →
	// upsertPoolRecipe 把"查询失败"误当成"配方不存在" → 每次启动又 Insert 一条。
	// 唯一索引 uk_pool_recipe_key_delete_at 是 (key, deleted_at) **复合**索引，
	// 而 deleted_at 为 NULL 时 NULL 互不相等，压根拦不住。
	// 只清理 deleted_at IS NULL 的活跃行，不动软删记录。
	keyCol := KeyCol()
	dedupeSQL := "DELETE FROM pool_recipes WHERE deleted_at IS NULL AND id NOT IN (" +
		"SELECT MIN(id) FROM pool_recipes WHERE deleted_at IS NULL GROUP BY " + keyCol + ")"
	if res := DB.Exec(dedupeSQL); res.Error != nil {
		common.SysLog("[pool] dedupe recipes failed: " + res.Error.Error())
	} else if res.RowsAffected > 0 {
		common.SysLog(fmt.Sprintf("[pool] removed %d duplicated recipe rows", res.RowsAffected))
	}

	// 清理首版的两条空 stub（"claude-oauth" / "gemini-free"），它们已被新版替代
	// （同样不能用反引号，理由见 GetPoolRecipeByKey）
	DB.Unscoped().
		Where(map[string]interface{}{"key": []string{"claude-oauth", "gemini-free"}}).
		Where("manual_steps IS NULL OR manual_steps = ''").
		Delete(&PoolRecipe{})

	// 一次性迁移：v2.0 之后本仓库不再内置任何 chromedp Runner，
	// 把历史上 manual_mode=false 又没配 webhook 的剧本统一刷回 manual_mode=true，
	// 避免 UI 里仍然显示「自动」但实际已无 Runner 跑导致永远失败。
	if err := DB.Model(&PoolRecipe{}).
		Where("manual_mode = ? AND (webhook_url IS NULL OR webhook_url = '')", false).
		Update("manual_mode", true).Error; err != nil {
		common.SysLog("[pool] migrate manual_mode failed: " + err.Error())
	}

	seeds := buildDefaultRecipeSeeds()
	for _, s := range seeds {
		if err := upsertPoolRecipe(s); err != nil {
			common.SysLog("seed pool recipe failed: " + s.Key + " — " + err.Error())
		}
	}
	return nil
}

// buildDefaultRecipeSeeds 默认剧本清单
//
// 维护原则：
//   - 全部走「半自动」(ManualMode=true)：UI 给步骤 + 必备材料 + 文档直链，
//     用户线下花 3-10 分钟搞定，Key 回填后系统自动入号池 + 同步建 Channel。
//   - 不假装能"全自动"。OpenAI / Anthropic / OpenRouter / Together / Mistral / Cohere
//     这些站点 2026-04 实测全部都吃 Cloudflare Turnstile + 设备指纹，chromedp 必败，
//     所以本仓库**不再内置任何 Runner**。
//   - 推荐顺序：先做「⭐ 最易」组（Gemini/Groq/Cerebras/DeepSeek/SiliconFlow），
//     大部分用户 5 分钟内就能在号池看到第一个真号。
func buildDefaultRecipeSeeds() []PoolRecipe {
	return []PoolRecipe{
		// ───────────────────────── ⭐ 最易（5 分钟搞定，无需信用卡/SMS） ─────────────────────────
		{
			Key: "gemini-aistudio-free", Name: "⭐ Google AI Studio (免费 Gemini)", Provider: "gemini",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 24, ChannelBaseURL: "https://generativelanguage.googleapis.com",
			DefaultModels: "gemini-2.5-pro,gemini-2.5-flash,gemini-2.5-flash-lite,gemini-2.0-flash,gemini-2.0-flash-lite,gemini-1.5-pro,gemini-1.5-flash,text-embedding-004",
			DocURL:        "https://aistudio.google.com/app/apikey",
			OutputFormat:  "AIza... (39 字符)",
			Description:   "✅ 推荐首选。免费拿 Gemini-2.5-Pro/Flash，无需信用卡。普通 Google 账号 + 非中国大陆 IP 即可，1500 RPD 免费额度。注册全程 1 分钟。",
			RequiredMaterials: `["Google 账号 (Gmail 即可)","非中国大陆 IP（机场/VPN 都行）"]`,
			ManualSteps: "1. 翻墙 → 打开 https://aistudio.google.com/app/apikey\n2. 登录 Google 账号（首次会提示同意条款）\n3. 点蓝色按钮「Create API key」\n4. 选择「Create API key in new project」\n5. 弹窗复制 AIza... 开头的 39 字符 Key\n6. 回到本系统点该剧本任务的「录入结果」，粘贴 Key 即可",
		},
		{
			Key: "groq-cloud", Name: "⭐ Groq Cloud (免费 Llama/Mixtral)", Provider: "groq",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 1, ChannelBaseURL: "https://api.groq.com/openai/v1",
			DefaultModels: "llama-3.3-70b-versatile,llama-3.1-8b-instant,llama-guard-3-8b,mixtral-8x7b-32768,gemma2-9b-it,deepseek-r1-distill-llama-70b,whisper-large-v3,whisper-large-v3-turbo",
			DocURL:        "https://console.groq.com/keys",
			OutputFormat:  "gsk_... (56 字符)",
			Description:   "✅ 推荐首选。Groq LPU 免费推理 (Llama-3.3-70B / Mixtral / Whisper)，延迟全行业最低。GitHub/Google/邮箱 OAuth 登录即可，无需信用卡。免费额度: 30 RPM / 14400 RPD。",
			RequiredMaterials: `["GitHub 或 Google 账号（任选）"]`,
			ManualSteps: "1. 打开 https://console.groq.com/login\n2. 选「Continue with GitHub」（最快）或 Google\n3. 授权后进入 console，点左侧「API Keys」\n4. 「Create API Key」→ 起个名字\n5. 弹窗仅显示一次，立刻复制 gsk_... 开头的 Key\n6. 回到本系统点「录入结果」，粘贴 Key",
		},
		{
			Key: "cerebras", Name: "⭐ Cerebras Inference (免费 Llama 超快)", Provider: "cerebras",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 1, ChannelBaseURL: "https://api.cerebras.ai/v1",
			DefaultModels: "llama3.1-8b,llama-3.3-70b,llama-4-scout-17b-16e-instruct,qwen-3-32b",
			DocURL:        "https://cloud.cerebras.ai/platform",
			OutputFormat:  "csk-... (56 字符)",
			Description:   "✅ 推荐首选。Cerebras Wafer-Scale 免费推理，Llama-3.1-70B ~2200 tok/s，比 Groq 还快。Google/GitHub 登录即可，无需信用卡。",
			RequiredMaterials: `["GitHub 或 Google 账号"]`,
			ManualSteps: "1. 打开 https://cloud.cerebras.ai/\n2. 选「Sign in with Google」或 GitHub\n3. 进入 Platform，左侧 API Keys\n4. 「Generate API Key」→ 复制 csk-...\n5. 回填",
		},
		{
			Key: "deepseek-platform", Name: "⭐ DeepSeek Platform (国内便宜)", Provider: "deepseek",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 43, ChannelBaseURL: "https://api.deepseek.com",
			DefaultModels: "deepseek-chat,deepseek-reasoner",
			DocURL:        "https://platform.deepseek.com/api_keys",
			OutputFormat:  "sk-... (35 字符)",
			Description:   "✅ 国内首选。DeepSeek-V3 0.27/1.10 USD/M tokens，极便宜。国内手机号 + 实名后充值 ≥ ¥1 即可拿 key。",
			RequiredMaterials: `["国内手机号","¥1 充值（最低）"]`,
			ManualSteps: "1. https://platform.deepseek.com/sign_up\n2. 手机号注册并验证\n3. 实名（如要求）\n4. 充值 → 至少 ¥1\n5. API Keys → Create new key\n6. 复制 sk-... 回填",
		},
		{
			Key: "siliconflow", Name: "⭐ SiliconFlow（硅基流动）", Provider: "siliconflow",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 40, ChannelBaseURL: "https://api.siliconflow.cn",
			DefaultModels: "deepseek-ai/DeepSeek-V3,deepseek-ai/DeepSeek-R1,Qwen/Qwen2.5-72B-Instruct,Qwen/Qwen2.5-Coder-32B-Instruct,Qwen/QwQ-32B-Preview,THUDM/glm-4-9b-chat,meta-llama/Meta-Llama-3.1-8B-Instruct,BAAI/bge-m3",
			DocURL:        "https://cloud.siliconflow.cn/account/ak",
			OutputFormat:  "sk-... 开头",
			Description:   "✅ 国内首选。聚合 DeepSeek / Qwen / GLM 等开源模型，新用户送 ¥14 体验金，部分模型免费层。",
			RequiredMaterials: `["国内手机号"]`,
			ManualSteps: "1. https://cloud.siliconflow.cn/account/sk\n2. 手机号注册（送 14 元）\n3. 账户管理 → API 密钥 → 新建\n4. 复制 → 回填",
		},

		// ───────────────────────── 国内（手机号实名） ─────────────────────────
		{
			Key: "moonshot-kimi", Name: "Moonshot (Kimi)", Provider: "moonshot",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 25, ChannelBaseURL: "https://api.moonshot.cn",
			DefaultModels: "moonshot-v1-8k,moonshot-v1-32k,moonshot-v1-128k,kimi-k2-0905-preview,kimi-latest",
			DocURL:        "https://platform.moonshot.cn/console/api-keys",
			OutputFormat:  "sk-... (51 字符)",
			Description:   "月之暗面 Kimi 官方 API。新用户赠 ¥15。",
			RequiredMaterials: `["国内手机号"]`,
			ManualSteps: "1. https://platform.moonshot.cn/\n2. 手机号注册（新用户送 15 元）\n3. 用户中心 → API Key 管理 → 新建\n4. 回填 sk-...",
		},
		{
			Key: "zhipu-glm", Name: "智谱 GLM", Provider: "zhipu",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 26, ChannelBaseURL: "https://open.bigmodel.cn",
			DefaultModels: "glm-4-plus,glm-4,glm-4-air,glm-4-flash,glm-4v-plus,glm-z1-air",
			DocURL:        "https://open.bigmodel.cn/usercenter/apikeys",
			OutputFormat:  "{id}.{secret}",
			Description:   "智谱 AI（GLM-4 / GLM-4-Plus）。实名后 GLM-4-Flash 免费。",
			RequiredMaterials: `["国内手机号 / 实名认证（领免费额度需要）"]`,
			ManualSteps: "1. https://open.bigmodel.cn/\n2. 注册 + 实名\n3. 用户中心 → API Keys → 添加\n4. 回填 {id}.{secret}",
		},
		{
			Key: "dashscope", Name: "阿里 DashScope（通义）", Provider: "ali",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyMedium,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 17, ChannelBaseURL: "https://dashscope.aliyuncs.com",
			DefaultModels: "qwen-max,qwen-plus,qwen-turbo,qwen-long,qwen2.5-72b-instruct,qwen2.5-coder-32b-instruct,qwen-vl-plus,qwen-vl-max",
			DocURL:        "https://dashscope.console.aliyun.com/apiKey",
			OutputFormat:  "sk-... (32 字符)",
			Description:   "阿里云通义千问。需阿里云账号 + 实名；通义千问-Turbo 等多模型免费额度。",
			RequiredMaterials: `["阿里云账号","实名认证"]`,
			ManualSteps: "1. 登录阿里云 → DashScope\n2. 开通服务（同意协议）\n3. API-KEY 管理 → 创建新 Key\n4. 回填",
		},
		{
			Key: "minimax", Name: "MiniMax", Provider: "minimax",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 35, ChannelBaseURL: "https://api.minimax.chat",
			DefaultModels: "abab6.5s-chat,abab6.5g-chat,abab6.5t-chat,minimax-m1",
			DocURL:        "https://www.minimaxi.com/user-center/basic-information/interface-key",
			OutputFormat:  "长 JWT token",
			Description:   "MiniMax abab/M1 系列。实名后送 50 万 tokens 体验。",
			RequiredMaterials: `["国内手机号","实名认证"]`,
			ManualSteps: "1. https://www.minimaxi.com/\n2. 注册 + 实名\n3. 接口密钥 → 新建\n4. 回填",
		},

		// ───────────────────────── OAuth（已有订阅则简单） ─────────────────────────
		{
			Key: "openai-codex", Name: "OpenAI Codex CLI (OAuth)", Provider: "openai",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyMedium,
			IPRequirement: "any", AutomationCapability: "oauth_only",
			ChannelType: 57, ChannelBaseURL: "",
			DefaultModels: "gpt-5,gpt-5-mini,gpt-5-codex,o3,o4-mini,gpt-4o,gpt-4o-mini",
			DocURL:       "https://github.com/openai/codex",
			OutputFormat: "OAuth refresh_token",
			Description:  "Codex CLI 走 ChatGPT-Plus 订阅授权，无需 API 余额。需有效 ChatGPT Plus / Team / Enterprise 账号。",
			RequiredMaterials: `["ChatGPT Plus / Team / Enterprise 订阅账号","本机已安装 Node.js"]`,
			ManualSteps: "1. 终端运行 `npm i -g @openai/codex`\n2. `codex auth login` 触发浏览器 OAuth\n3. 用 ChatGPT 账号登录授权\n4. 取本机 ~/.codex/auth.json 中的 refresh_token\n5. 「录入结果」粘贴 refresh_token；本系统会自动 refresh access_token",
		},
		{
			Key: "anthropic-claude-pro", Name: "Claude Pro / Max OAuth", Provider: "claude",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyMedium,
			IPRequirement: "any", AutomationCapability: "oauth_only",
			ChannelType: 14, ChannelBaseURL: "https://api.anthropic.com",
			DefaultModels: "claude-sonnet-4-5-20250929,claude-opus-4-5-20251101,claude-3-7-sonnet-latest,claude-3-5-haiku-latest",
			DocURL:       "https://docs.anthropic.com/en/docs/claude-code/oauth",
			OutputFormat: "sk-ant-oat01-... (OAuth Token)",
			Description:  "通过 claude.ai Pro/Max 订阅获取 OAuth 凭证（sk-ant-oat01）。免实名信用卡，但需有效订阅。",
			RequiredMaterials: `["有效 Claude Pro 或 Max 订阅","本机安装 claude-code CLI"]`,
			ManualSteps: "1. `npm i -g @anthropic-ai/claude-code`\n2. `claude /login` 浏览器授权\n3. 取 ~/.claude/credentials.json 中的 sk-ant-oat01\n4. 「录入结果」粘贴",
		},

		// ───────────────────────── 海外付费 / 强反爬（必须手动） ─────────────────────────
		{
			Key: "openai-platform", Name: "OpenAI Platform API", Provider: "openai",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyHard,
			IPRequirement: "residential", AutomationCapability: "manual",
			ChannelType: 1, ChannelBaseURL: "https://api.openai.com",
			DefaultModels: "gpt-5,gpt-5-mini,gpt-5-nano,gpt-4o,gpt-4o-mini,o4-mini,o3,o3-mini,gpt-4-turbo,gpt-3.5-turbo,text-embedding-3-large,text-embedding-3-small",
			DocURL:       "https://platform.openai.com/api-keys",
			OutputFormat: "sk-proj-... 或 sk-...",
			Description:  "⚠️ 实测：自动化注册被 Cloudflare Turnstile + 设备指纹拦截，本仓库不内置 Runner。请手动：住宅 IP + 海外手机号 + 信用卡，整体流程约 15-30 分钟。",
			RequiredMaterials: `["住宅 IP（必须，机房 IP 必失败）","海外手机号（接 OTP）","Visa/Mastercard 信用卡"]`,
			ManualSteps: "1. 住宅 IP 打开 https://platform.openai.com/signup\n2. 邮箱注册并验证\n3. 绑定海外手机号（短信 OTP）\n4. Settings → Billing → Add Payment Method\n5. 充值 $5 解锁 GPT-4\n6. API Keys → Create new secret key\n7. 复制 sk-... 回填",
		},
		{
			Key: "anthropic-console", Name: "Anthropic Console API", Provider: "claude",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyHard,
			IPRequirement: "residential", AutomationCapability: "manual",
			ChannelType: 14, ChannelBaseURL: "https://api.anthropic.com",
			DefaultModels: "claude-sonnet-4-5-20250929,claude-opus-4-5-20251101,claude-3-7-sonnet-latest,claude-3-5-haiku-latest,claude-3-5-sonnet-20241022,claude-3-haiku-20240307",
			DocURL:       "https://console.anthropic.com/settings/keys",
			OutputFormat: "sk-ant-api03-... (95 字符)",
			Description:  "⚠️ 实测：极强 Cloudflare 校验 + 信用卡验证，自动化必失败，必须手动。",
			RequiredMaterials: `["住宅 IP","Visa/Mastercard"]`,
			ManualSteps: "1. 住宅 IP 打开 https://console.anthropic.com/\n2. 邮箱注册 + 手机号 OTP\n3. Plans & Billing → 充值 ≥ $5\n4. Settings → API Keys → Create Key\n5. 复制 sk-ant-api03-... 回填",
		},
		{
			Key: "openrouter", Name: "OpenRouter", Provider: "openrouter",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 20, ChannelBaseURL: "https://openrouter.ai/api/v1",
			DefaultModels: "deepseek/deepseek-r1:free,deepseek/deepseek-chat:free,meta-llama/llama-3.3-70b-instruct:free,google/gemini-2.0-flash-exp:free,qwen/qwen-2.5-72b-instruct:free,anthropic/claude-3.5-sonnet,openai/gpt-4o,openai/gpt-4o-mini",
			DocURL:       "https://openrouter.ai/keys",
			OutputFormat: "sk-or-v1-... (73 字符)",
			Description:  "⚠️ 实测：注册和创建 Key 流程都接 Cloudflare Turnstile，自动化失败。手动注册倒是 2 分钟搞定。聚合 100+ 模型，含若干免费模型（DeepSeek-R1 free / Llama-3.3 free 等）。",
			RequiredMaterials: `["邮箱 或 GitHub/Google 账号"]`,
			ManualSteps: "1. https://openrouter.ai/sign-up\n2. 邮箱/GitHub/Google 任一登录方式\n3. 填昵称\n4. 右上角头像 → Keys → Create Key\n5. 复制 sk-or-v1-... 回填",
		},
		{
			Key: "together-ai", Name: "Together AI", Provider: "together",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 1, ChannelBaseURL: "https://api.together.xyz/v1",
			DefaultModels: "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo,meta-llama/Llama-3.3-70B-Instruct-Turbo,deepseek-ai/DeepSeek-V3,deepseek-ai/DeepSeek-R1,Qwen/Qwen2.5-72B-Instruct-Turbo,mistralai/Mixtral-8x7B-Instruct-v0.1",
			DocURL:       "https://api.together.xyz/settings/api-keys",
			OutputFormat: "64 位 hex",
			Description:  "⚠️ 实测：自动注册被 Cloudflare 拦截。手动 5 分钟即可。新用户赠 $1 + 部分模型免费层。",
			RequiredMaterials: `["邮箱"]`,
			ManualSteps: "1. https://api.together.xyz/signup\n2. 邮箱注册 → 验证邮件\n3. Settings → API Keys → Reveal\n4. 复制回填",
		},
		{
			Key: "mistral-platform", Name: "Mistral La Plateforme", Provider: "mistral",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyMedium,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 42, ChannelBaseURL: "https://api.mistral.ai",
			DefaultModels: "mistral-large-latest,mistral-small-latest,codestral-latest,pixtral-large-latest,ministral-8b-latest,ministral-3b-latest,mistral-embed",
			DocURL:       "https://console.mistral.ai/api-keys",
			OutputFormat: "32 字符随机串",
			Description:  "⚠️ 实测：注册流程有 Cloudflare 校验，自动化失败。手动 5-10 分钟。Open weights 模型有免费层。",
			RequiredMaterials: `["邮箱","海外手机号（部分场景需要）"]`,
			ManualSteps: "1. https://console.mistral.ai/\n2. 邮箱注册（必要时手机号验证）\n3. API Keys → Create new key\n4. 回填",
		},
		{
			Key: "cohere", Name: "Cohere (Trial)", Provider: "cohere",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyEasy,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 34, ChannelBaseURL: "https://api.cohere.ai",
			DefaultModels: "command-r-plus-08-2024,command-r-08-2024,command-r-plus,command-r,command-light,c4ai-aya-expanse-32b,embed-english-v3.0,rerank-english-v3.0",
			DocURL:       "https://dashboard.cohere.com/api-keys",
			OutputFormat: "40 字符",
			Description:  "⚠️ 实测：hCaptcha 拦截自动化。手动 3 分钟。Trial Key 免费但限速 20 RPM；Command-R / Embed / Rerank 都能用。",
			RequiredMaterials: `["邮箱"]`,
			ManualSteps: "1. https://dashboard.cohere.com/welcome/register\n2. 邮箱注册 + 验证\n3. API Keys → Trial Key（默认就有）\n4. 复制回填",
		},
		{
			Key: "xai-grok", Name: "xAI (Grok)", Provider: "xai",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyMedium,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 48, ChannelBaseURL: "https://api.x.ai/v1",
			DefaultModels: "grok-4,grok-4-fast,grok-3,grok-3-mini,grok-2-vision-1212",
			DocURL:       "https://console.x.ai/",
			OutputFormat: "xai-... (88 字符)",
			Description:  "⚠️ 需 X.com（Twitter）账号联通 + 海外信用卡。X.com 设备指纹检查严格，自动化不可行。新账号送 $25/月。",
			RequiredMaterials: `["有效 X.com 账号","海外信用卡"]`,
			ManualSteps: "1. https://console.x.ai/\n2. 用 X.com 账号登录\n3. Settings → Workspace → Billing 绑卡\n4. API Keys → Create\n5. 回填 xai-...",
		},
		{
			Key: "google-vertex-ai", Name: "Google Vertex AI (Paid)", Provider: "gemini",
			Version: "2.0.0", ManualMode: true, Difficulty: PoolRecipeDifficultyHard,
			IPRequirement: "any", AutomationCapability: "manual",
			ChannelType: 41, ChannelBaseURL: "",
			DefaultModels: "gemini-2.5-pro,gemini-2.5-flash,gemini-1.5-pro,gemini-1.5-flash,imagen-3.0-generate-002",
			DocURL:       "https://console.cloud.google.com/vertex-ai",
			OutputFormat: "Service Account JSON 整个粘贴",
			Description:  "GCP Vertex AI 付费。可用 Gemini Pro/Ultra/Imagen。需 GCP 计费账号 + Service Account。",
			RequiredMaterials: `["GCP 账号","已绑信用卡的计费项目","Service Account（角色：Vertex AI User）"]`,
			ManualSteps: "1. GCP 启用 Vertex AI API\n2. 创建 Service Account，授予 roles/aiplatform.user\n3. 下载 JSON Key\n4. 把整个 JSON 粘到「录入结果」key_raw 字段",
		},
	}
}

// EnsureDefaultOption 仅当 options 表中不存在该 key 时插入默认值；存在则保留用户已有配置。
// 直接查 options 表，避免依赖 OptionMap 在启动早期阶段尚未加载的状态。
func EnsureDefaultOption(key, value string) error {
	if key == "" || value == "" {
		return nil
	}
	var existing Option
	err := DB.Where(map[string]interface{}{"key": key}).First(&existing).Error
	if err == nil {
		return nil
	}
	if err := DB.Create(&Option{Key: key, Value: value}).Error; err != nil {
		return err
	}
	common.SysLog("[pool] seeded default option: " + key)
	return nil
}

// SeedDefaultTelegramConfig 启动时写入默认 Telegram Bot/ChatId（仅当未配置时）
func SeedDefaultTelegramConfig(defaultToken, defaultChatId string) error {
	if err := EnsureDefaultOption("PoolTelegramBotToken", defaultToken); err != nil {
		return err
	}
	return EnsureDefaultOption("PoolTelegramChatId", defaultChatId)
}

// SeedDefaultAutomationConfig 启动时写入号池自动化默认配置（仅当未配置时）
//   - PoolEmailProvider     邮箱 provider（mailtm/guerrilla/1secmail）
//   - PoolSmsProvider       SMS provider（5sim/smsactivate/onlinesim/mock）
//   - PoolFivesimApiKey     5sim Bearer JWT
//   - PoolSmsActivateApiKey sms-activate API Key
func SeedDefaultAutomationConfig(emailProvider, smsProvider, fivesimKey, smsActivateKey string) error {
	if err := EnsureDefaultOption("PoolEmailProvider", emailProvider); err != nil {
		return err
	}
	if err := EnsureDefaultOption("PoolSmsProvider", smsProvider); err != nil {
		return err
	}
	if err := EnsureDefaultOption("PoolFivesimApiKey", fivesimKey); err != nil {
		return err
	}
	return EnsureDefaultOption("PoolSmsActivateApiKey", smsActivateKey)
}
