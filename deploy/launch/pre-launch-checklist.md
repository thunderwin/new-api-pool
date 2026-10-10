# 上线前 Checklist（100+ 项）

> 全部勾完才能进 P1 内测。
> 缺 1 项不上。

## 一、部署与基础设施（25 项）

### 网关 / 数据库 / 缓存
- [ ] 1.1 new-api 已升级到稳定版本（不是 main 最新）
- [ ] 1.2 启用 PostgreSQL 16，**不再用 SQLite**
- [ ] 1.3 PG 主从（Patroni 3 节点）+ etcd 集群
- [ ] 1.4 已演练 PG failover ≥ 1 次（RTO < 30s）
- [ ] 1.5 PG 慢查询监控开启（log_min_duration_statement = 1000）
- [ ] 1.6 Redis Sentinel 3 节点
- [ ] 1.7 Redis 数据持久化（AOF appendfsync everysec）
- [ ] 1.8 LOG_SQL_DSN 分离（日志库与主库分开）
- [ ] 1.9 new-api 双实例运行（无状态校验过）
- [ ] 1.10 Caddy / Nginx 反代有 health check + 自动摘流
- [ ] 1.11 health 检查路径配置正确（/api/status）

### 配置与环境变量
- [ ] 1.12 SESSION_SECRET 已设（不再是默认）
- [ ] 1.13 CRYPTO_SECRET 已设（且备份在 Vault）
- [ ] 1.14 STREAMING_TIMEOUT ≥ 300s
- [ ] 1.15 MEMORY_CACHE_ENABLED = true
- [ ] 1.16 ERROR_LOG_ENABLED = true
- [ ] 1.17 TZ = Asia/Shanghai

### 备份
- [ ] 1.18 PG 每日全备 + 持续 WAL → S3
- [ ] 1.19 已**演练 1 次完整恢复**（不仅是备份能跑）
- [ ] 1.20 备份加密（密钥分离存放）
- [ ] 1.21 备份保留策略：日 7 / 周 4 / 月 12
- [ ] 1.22 异地副本（不同区域）

### 域名 / TLS
- [ ] 1.23 主域名 + 备用 TLD（≥ 3 个不同后缀）
- [ ] 1.24 证书续期自动化（cert-manager / acme.sh）
- [ ] 1.25 证书 7 天前过期告警

## 二、安全（20 项）

### 网络
- [ ] 2.1 Cloudflare WAF 启用（OWASP 规则集）
- [ ] 2.2 Cloudflare DDoS 防护启用
- [ ] 2.3 源站只允许 Cloudflare IP（authenticated origin pulls）
- [ ] 2.4 SSH 禁用密码登录
- [ ] 2.5 SSH 端口已改（非 22）
- [ ] 2.6 fail2ban 已启用
- [ ] 2.7 ufw 规则最小化（只开必要端口）
- [ ] 2.8 Redis / PG 不暴露公网

### 应用
- [ ] 2.9 管理员密码足够强（初始化向导自设，系统无默认账号）
- [ ] 2.10 删除默认令牌
- [ ] 2.11 admin.example.com 走 Cloudflare Access SSO + 2FA
- [ ] 2.12 admin 与 api 是不同子域名
- [ ] 2.13 强制 HTTPS + HSTS

### 密钥管理
- [ ] 2.14 上游 Key 用 Vault / SOPS 加密
- [ ] 2.15 没有任何密钥进 Git（gitleaks 扫过）
- [ ] 2.16 .env 文件 600 权限
- [ ] 2.17 密钥轮换计划已制定（季度）

### 审计
- [ ] 2.18 所有管理操作进审计日志
- [ ] 2.19 审计日志接入 Loki / 单独存档
- [ ] 2.20 注册时强制 ToS 勾选

## 三、风控与计费（15 项）

### 风控
- [ ] 3.1 公开注册关闭，改邀请码
- [ ] 3.2 默认令牌 RPM / TPM 已配置
- [ ] 3.3 用户日消耗上限已配置
- [ ] 3.4 IP 日志开启
- [ ] 3.5 异常用户实时告警跑通

### 计费
- [ ] 3.6 model_ratio 已为所有模型配置
- [ ] 3.7 completion_ratio 已配置（input/output 不同价的模型）
- [ ] 3.8 用户分组 group_ratio 已配置（vip/default/free）
- [ ] 3.9 倍率不倒挂（成本 ≤ 售价）已校验
- [ ] 3.10 有月度对账脚本
- [ ] 3.11 利润率监控（亏损告警）

### 充值
- [ ] 3.12 至少一种充值方式工作（兑换码 / epay / Stripe）
- [ ] 3.13 退款流程已演练
- [ ] 3.14 充值失败有补救路径
- [ ] 3.15 财务对账每日自动跑

## 四、可观测性（15 项）

### 监控
- [ ] 4.1 Prometheus 抓 new-api / node / pg / redis 指标
- [ ] 4.2 Grafana 5 个核心看板已建（总览/渠道/用户/号池/成本）
- [ ] 4.3 Loki + Promtail 日志聚合跑通
- [ ] 4.4 日志已脱敏（PII 正则在 Promtail 上做）
- [ ] 4.5 blackbox_exporter 探活上游

### 告警
- [ ] 4.6 Alertmanager → Telegram 通联
- [ ] 4.7 P0 告警有电话 / 短信通道
- [ ] 4.8 P0/P1/P2/P3 阈值已配置
- [ ] 4.9 静默规则配置（避免凌晨乱响）
- [ ] 4.10 告警去重 / 抑制规则

### 状态页
- [ ] 4.11 Uptime Kuma 部署
- [ ] 4.12 监控目标完整（API/Admin/Docs/Status）
- [ ] 4.13 公开状态页 status.example.com 上线
- [ ] 4.14 内部状态页与公开版分开

### SLO
- [ ] 4.15 SLO 定义文档化（可用性 99.9%、p95 < X）

## 五、号池（10 项）

- [ ] 5.1 至少 4 个分组（official / claude-oauth / gemini-free / third-party）
- [ ] 5.2 每分组至少 1 个渠道
- [ ] 5.3 每渠道单测通过（通道测试按钮 + 真实调用）
- [ ] 5.4 自动禁用 / 自动启用规则配置
- [ ] 5.5 渠道健康巡检 cron 跑通
- [ ] 5.6 上游 Key 加密存（不是明文）
- [ ] 5.7 Claude OAuth refresh 脚本验证
- [ ] 5.8 Gemini Free 限速配置（RPM/RPD）
- [ ] 5.9 第三方 Key 质量巡检
- [ ] 5.10 号池容量 ≥ 预估首月 3 倍

## 六、合规与法务（8 项）

- [ ] 6.1 ToS 上线（注册必勾）
- [ ] 6.2 Privacy Policy 上线
- [ ] 6.3 AUP 上线
- [ ] 6.4 Refund Policy 上线
- [ ] 6.5 Cookie Policy 上线（如部署在 EU/UK）
- [ ] 6.6 DMCA 通知地址公开
- [ ] 6.7 法务模板已经过当地律师 review
- [ ] 6.8 数据保留窗口已实施（默认 conversation 24h）

## 七、客服与运营（10 项）

- [ ] 7.1 工单系统部署（Chatwoot / Zendesk）
- [ ] 7.2 邮箱 support@ / abuse@ / dmca@ 已建并测试转发
- [ ] 7.3 至少 5 条 canned response 编辑完成
- [ ] 7.4 客服值班表（即使是个人项目也要写"个人响应时段"）
- [ ] 7.5 知识库 / FAQ 上线
- [ ] 7.6 自助工具：用量查询 / Token 重置 / 充值记录
- [ ] 7.7 告警 Telegram bot 通了
- [ ] 7.8 状态页订阅功能可用
- [ ] 7.9 紧急联系人列表（CDN / 上游 / 律师）
- [ ] 7.10 客服 SLA 公示（付费用户 24h 首响）

## 八、灾备（7 项）

- [ ] 8.1 9 个核心 runbook 写完（[deploy/dr/](../dr/)）
- [ ] 8.2 至少 3 个 runbook 演练过
- [ ] 8.3 一键回滚脚本可用（[`cicd/scripts/rollback.sh`](../cicd/scripts/rollback.sh)）
- [ ] 8.4 备用域名 DNS 已配置好（仅未启用）
- [ ] 8.5 备用支付通道（≥ 2 个）
- [ ] 8.6 Cloudflare 故障的 fallback DNS（绕过 cf）
- [ ] 8.7 客服/法务的紧急联系人保存

## 九、文档（10 项）

- [ ] 9.1 用户文档站点上线（VitePress / Mintlify）
- [ ] 9.2 quickstart（5 分钟版）
- [ ] 9.3 模型清单 + 价格
- [ ] 9.4 错误码字典
- [ ] 9.5 SDK 兼容性矩阵
- [ ] 9.6 Postman 集合
- [ ] 9.7 多语言代码示例（Python / Node / Go）
- [ ] 9.8 内部 wiki：架构图 / 部署流程 / 运维手册
- [ ] 9.9 内部 onboarding 文档（让新来的工程师 1 天上手）
- [ ] 9.10 用户公告渠道（Telegram 频道 / 邮件列表）

## 十、测试（10 项）

- [ ] 10.1 烟雾测试自动跑（每次部署）
- [ ] 10.2 回归测试用例 ≥ 5 个
- [ ] 10.3 k6 压测过（5 分钟稳定 1k QPS）
- [ ] 10.4 至少 2 个混沌实验跑过
- [ ] 10.5 协议兼容性测试（OpenAI/Anthropic/Gemini 三向）
- [ ] 10.6 计费正确性测试
- [ ] 10.7 限速测试
- [ ] 10.8 缓存命中测试
- [ ] 10.9 性能基线已建立
- [ ] 10.10 CI/CD pipeline 全绿运行 ≥ 5 次

## 十一、CI/CD（5 项）

- [ ] 11.1 GitHub Actions 配置（lint/test/build/deploy）
- [ ] 11.2 staging 自动部署
- [ ] 11.3 prod 部署需人工审批
- [ ] 11.4 Ansible playbook 完整
- [ ] 11.5 灰度 + 自动回滚脚本可用

## 十二、商业（5 项）

- [ ] 12.1 收款主体已注册（公司 / 工作室）
- [ ] 12.2 银行账户 / 收款渠道已开
- [ ] 12.3 发票 / 报销流程清楚
- [ ] 12.4 第一波种子用户 ≥ 5 人已确认
- [ ] 12.5 至少 1 个月运营预算（含上游成本 + VPS + CDN + 客服 + 储备）

## 十三、Go/No-Go 会议（3 项）

- [ ] 13.1 上面 12 大类全部勾完
- [ ] 13.2 4 方签字（技术 / 运营 / 财务 / 法务）
- [ ] 13.3 P1 内测起始时间已定 + 通知到内测用户

---

> ⚠️ 任何一项未勾，**都不允许进 P1**。
> 这不是过度严格——号池业务是高风险业务，麻烦在合规、客诉、资金链。
> 上线前多花一周时间，比上线后救火一个月强。
