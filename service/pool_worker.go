// Pool Worker — 全自动 Job 队列消费
//
// 启动后周期扫描 pool_jobs 表中 status=pending 的 Job：
//   - 找对应 Runner，没有则置 failed
//   - 启 goroutine 跑 Runner.Run
//   - 成功：写 pool_accounts + 可选自动建 channel + Job → success + recipe.success_count++
//   - 失败：Job → failed + error_msg + recipe.failure_count++
//
// 并发：单进程，最大并发 = PoolWorkerMaxConcurrent (默认 2)
// 单 Job 超时：5 min（chromedp + 邮箱等待）

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const (
	poolWorkerScanInterval = 8 * time.Second
	poolWorkerJobTimeout   = 5 * time.Minute
)

var (
	workerStarted   int32
	workerInflight  int32 // 当前运行中的 Job 数（atomic）
	workerSemaphore chan struct{}
)

// StartPoolWorker 启动后台队列消费者（master 节点调用一次即可）
func StartPoolWorker(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&workerStarted, 0, 1) {
		return
	}
	maxConc := getIntOptionOrDefault("PoolWorkerMaxConcurrent", 2)
	if maxConc <= 0 {
		maxConc = 2
	}
	workerSemaphore = make(chan struct{}, maxConc)

	common.SysLog(fmt.Sprintf("[pool-worker] started, max_concurrent=%d, runners=%v",
		maxConc, ListRunnerKeys()))
	go logAutomationSelfCheck()

	go func() {
		ticker := time.NewTicker(poolWorkerScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				common.SysLog("[pool-worker] context done, exiting")
				return
			case <-ticker.C:
				dispatchOnce(ctx)
			}
		}
	}()
}

// PoolWorkerStatus 给前端用的运行时状态
type PoolWorkerStatus struct {
	Running       bool     `json:"running"`
	MaxConcurrent int      `json:"max_concurrent"`
	Inflight      int      `json:"inflight"`
	RunnerKeys    []string `json:"runner_keys"`
}

// GetPoolWorkerStatus 返回当前 worker 状态
func GetPoolWorkerStatus() PoolWorkerStatus {
	maxConc := 0
	if workerSemaphore != nil {
		maxConc = cap(workerSemaphore)
	}
	return PoolWorkerStatus{
		Running:       atomic.LoadInt32(&workerStarted) == 1,
		MaxConcurrent: maxConc,
		Inflight:      int(atomic.LoadInt32(&workerInflight)),
		RunnerKeys:    ListRunnerKeys(),
	}
}

// dispatchOnce 扫一次 pending Job
func dispatchOnce(ctx context.Context) {
	jobs, _, err := model.SearchPoolJobs("", model.PoolJobStatusPending, 0, 20)
	if err != nil {
		common.SysLog("[pool-worker] scan failed: " + err.Error())
		return
	}
	if len(jobs) == 0 {
		return
	}
	for _, job := range jobs {
		recipe, err := model.GetPoolRecipeByKey(job.RecipeKey)
		if err != nil {
			_ = job.UpdateStatus(model.PoolJobStatusFailed, "", "recipe 不存在: "+err.Error())
			continue
		}
		// 半自动 Recipe 不该出现在 pending 状态（应该是 manual_pending），跳过
		if recipe.ManualMode {
			continue
		}
		runner := GetRunner(recipe.Key)
		if runner == nil {
			_ = job.UpdateStatus(model.PoolJobStatusFailed, "",
				fmt.Sprintf("未找到 Runner: %s（可能尚未实现全自动）", recipe.Key))
			incrementRecipeCounter(recipe.Key, false)
			continue
		}
		// 抢一个 slot
		select {
		case workerSemaphore <- struct{}{}:
		default:
			// 满了，下轮再说
			continue
		}
		atomic.AddInt32(&workerInflight, 1)
		go func(j *model.PoolJob, r *model.PoolRecipe, run RecipeRunner) {
			defer func() {
				<-workerSemaphore
				atomic.AddInt32(&workerInflight, -1)
				if rec := recover(); rec != nil {
					common.SysLog(fmt.Sprintf("[pool-worker] panic in %s: %v", r.Key, rec))
					_ = j.UpdateStatus(model.PoolJobStatusFailed, "",
						fmt.Sprintf("worker panic: %v", rec))
					incrementRecipeCounter(r.Key, false)
				}
			}()
			runJob(ctx, j, r, run)
		}(job, recipe, runner)
	}
}

func runJob(ctx context.Context, job *model.PoolJob, recipe *model.PoolRecipe, runner RecipeRunner) {
	common.SysLog(fmt.Sprintf("[pool-worker] running job_id=%d recipe=%s", job.Id, recipe.Key))
	_ = job.UpdateStatus(model.PoolJobStatusRunning, "", "")

	jobCtx, cancel := context.WithTimeout(ctx, poolWorkerJobTimeout)
	defer cancel()

	result, err := runner.Run(jobCtx, recipe)
	if err != nil {
		common.SysLog(fmt.Sprintf("[pool-worker] FAIL job_id=%d recipe=%s err=%s",
			job.Id, recipe.Key, err.Error()))
		_ = job.UpdateStatus(model.PoolJobStatusFailed, "", err.Error())
		incrementRecipeCounter(recipe.Key, false)
		return
	}
	if result == nil || result.KeyRaw == "" {
		_ = job.UpdateStatus(model.PoolJobStatusFailed, "", "runner 返回空 Key")
		incrementRecipeCounter(recipe.Key, false)
		return
	}

	// 1) 写 pool_accounts
	groupName := "default"
	if recipe.Provider != "" {
		groupName = recipe.Provider
	}
	acc := &model.PoolAccount{
		Name:        result.AccountName,
		Provider:    recipe.Provider,
		AccountType: model.PoolAccountTypeOfficial,
		Status:      model.PoolAccountStatusActive,
		KeyMasked:   model.MaskPoolAccountKey(result.KeyRaw),
		BalanceUSD:  result.BalanceUSD,
		ExpireAt:    result.ExpireAt,
		GroupName:   groupName,
		Notes:       fmt.Sprintf("[auto] Recipe=%s Job=%d %s", recipe.Key, job.Id, result.Notes),
	}
	if err := acc.Insert(); err != nil {
		_ = job.UpdateStatus(model.PoolJobStatusFailed, "",
			"runner 成功但 pool_accounts 写入失败: "+err.Error())
		incrementRecipeCounter(recipe.Key, false)
		return
	}

	// 2) 自动建渠道（如果 Recipe.ChannelType 已设置）
	channelId := 0
	if recipe.ChannelType > 0 {
		poolTag := "pool-auto"
		chGroup := "default"
		if groupName != "" && groupName != "default" {
			chGroup = "default," + groupName
		}
		ch := &model.Channel{
			Type:        recipe.ChannelType,
			Key:         result.KeyRaw,
			Status:      common.ChannelStatusEnabled,
			Name:        result.AccountName,
			CreatedTime: common.GetTimestamp(),
			Group:       chGroup,
			Models:      recipe.DefaultModels,
			Tag:         &poolTag,
		}
		if recipe.ChannelBaseURL != "" {
			base := recipe.ChannelBaseURL
			ch.BaseURL = &base
		}
		// channel.Insert() 自动写 abilities，路由立即可用
		if err := ch.Insert(); err != nil {
			common.SysLog("[pool-worker] auto create channel failed: " + err.Error())
		} else {
			channelId = ch.Id
			acc.ChannelId = channelId
			_ = acc.Update()
		}
	}

	// 3) Job 关闭
	resultJSON, _ := json.Marshal(map[string]any{
		"pool_account_id": acc.Id,
		"channel_id":      channelId,
		"by":              "worker",
	})
	_ = job.UpdateStatus(model.PoolJobStatusSuccess, string(resultJSON), "")
	incrementRecipeCounter(recipe.Key, true)
	common.SysLog(fmt.Sprintf("[pool-worker] OK job_id=%d recipe=%s account_id=%d channel_id=%d",
		job.Id, recipe.Key, acc.Id, channelId))
}

func incrementRecipeCounter(key string, success bool) {
	col := "failure_count"
	if success {
		col = "success_count"
	}
	// 不要拼 `key` = ? ：PostgreSQL 把反引号当运算符，直接语法错误。
	// 交给 GORM 按方言引用标识符。（col 取值只有上面两个常量，无注入面）
	model.DB.Model(&model.PoolRecipe{}).
		Where(map[string]interface{}{"key": key}).
		UpdateColumn(col, gorm.Expr(col+" + 1"))
}

// 帮助函数：从 OptionMap 拿 int，缺省 fallback
func getIntOptionOrDefault(key string, def int) int {
	common.OptionMapRWMutex.RLock()
	v := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	if v == "" {
		return def
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// logAutomationSelfCheck 启动时尝试拉一次 5sim / sms-activate 余额并写入日志，
// 让用户在控制台一眼看到接码 provider 是否可用。
// 失败时只打 warning，不影响 worker 主流程。
func logAutomationSelfCheck() {
	// 给 OptionMap 一个加载窗口
	time.Sleep(2 * time.Second)
	smsP := GetSmsProvider()
	if smsP == nil {
		common.SysLog("[pool-worker] selfcheck: PoolSmsProvider 未配置（可在「自动化设置」中切换 5sim / sms-activate / mock）")
	} else {
		balance, err := smsP.Balance()
		if err != nil {
			common.SysLog(fmt.Sprintf("[pool-worker] selfcheck: SMS provider %s 余额查询失败: %s",
				smsP.Name(), err.Error()))
		} else {
			unit := "USD"
			if smsP.Name() == "5sim" {
				unit = "RUB"
			}
			common.SysLog(fmt.Sprintf("[pool-worker] selfcheck: SMS provider %s 余额 = %.3f %s",
				smsP.Name(), balance, unit))
		}
	}

	common.OptionMapRWMutex.RLock()
	preferEmail := common.OptionMap["PoolEmailProvider"]
	common.OptionMapRWMutex.RUnlock()
	if preferEmail == "" {
		preferEmail = "mailtm (默认)"
	}
	common.SysLog(fmt.Sprintf("[pool-worker] selfcheck: Email provider = %s（fallback 顺序: mailtm > guerrilla > 1secmail）",
		preferEmail))
}

var _ = sync.Mutex{} // keep import
