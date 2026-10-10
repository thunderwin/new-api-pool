# `deploy/` — new-api 号池生产级蓝图

> 把 `start.command + SQLite + root/123456` 演化成可对外付费售卖的号池系统的全部蓝图。
> 这是**文档与脚手架**：你需要按 [`ROADMAP.md`](./ROADMAP.md) 的 12 周节奏逐步落地，不是一键起飞。

## 看板：从哪里开始

| 我现在想做… | 看这里 |
|---|---|
| **第一次开干**，把本地 SQLite → 生产 PG | [`ROADMAP.md`](./ROADMAP.md) Week 1-2 |
| **跑起来生产网关**（PG/Redis/双实例/Caddy）| [`docker-compose.prod.yml`](./docker-compose.prod.yml) + [`.env.example`](./.env.example) |
| **导渠道**（4 类号池模板）| [`channels/`](./channels/) |
| **接监控告警**（Prom/Grafana/Loki/Alertmanager）| [`monitoring/`](./monitoring/) |
| **故障来了** | [`runbooks/`](./runbooks/) |
| **决定要不要上线**（100+ 项 checklist + 4 阶段灰度） | [`launch/`](./launch/) |
| **我要造号池上游**（注册流水线）| [`upstream/`](./upstream/) |
| **算账 / 看 BI** | [`bi/`](./bi/) |

## 36 个模块全景

```
deploy/
├── README.md                       ← 你在这里
├── ROADMAP.md                      ← 12 周执行路线图
├── docker-compose.prod.yml         ← 生产编排
├── .env.example                    ← 环境变量模板
│
├── 一、基础部署
│   ├── caddy/                      反代 + 自动 HTTPS + 域名分离
│   ├── scripts/                    gen-secrets / backup / restore
│   └── runbooks/                   9 个核心灾备 runbook
│
├── 二、号池 / 渠道
│   ├── channels/                   4 类号池模板（official/oauth/free/3rd）
│   ├── sop/                        入池→灰度→上线→离池→对账 SOP
│   └── upstream/                   注册流水线（独立环境）
│       ├── orchestrator/           状态机编排器
│       ├── adapters/email,sms,captcha,browser/   适配器
│       ├── recipes/gemini,claude/  注册脚本
│       ├── payment/                虚拟卡指南
│       └── budget/                 预算控制器 + 日报
│
├── 三、计费 / 风控
│   ├── billing/                    倍率、对账、退款、发票
│   └── risk-control/               限速、邀请码、异常告警
│
├── 四、可观测性 / 高可用
│   ├── monitoring/                 Prom/Grafana/Loki/Alertmanager
│   ├── ha/                         Patroni PG 主从 + Redis Sentinel
│   ├── smart-routing/              自适应权重 sidecar + 阶梯回退
│   └── cost-cache/                 L0/L1/L2 缓存与 prompt cache
│
├── 五、安全 / 合规
│   ├── security/                   Vault / SSH / 密钥轮换 / 审计
│   ├── compliance/                 PII / 数据保留 / GDPR-DSR
│   └── legal/                      ToS / Privacy / AUP / DMCA / Refund
│
├── 六、客服 / 用户侧
│   ├── customer-ops/               L0~L3 客服 + Chatwoot + 状态页
│   └── docs/                       用户 API 文档 / SDK 兼容 / Postman
│
├── 七、工程化
│   ├── testing/                    smoke + regression + k6 + chaos
│   ├── cicd/                       Actions + Ansible + 灰度脚本
│   └── multi-region/               跨区域 + Cloudflare + 边缘加速
│
├── 八、运营 / 增长
│   ├── bi/                         10 个报表 SQL + Metabase + 推送
│   └── launch/                     上线 checklist + 4 阶段灰度 SOP
```

## 现状速读

✅ **已完成（蓝图层面）**：36 个模块全部产出，含可执行脚本、配置模板、SQL、文档。
🟡 **未完成（落地层面）**：脚本里的 IP / 域名 / 密钥 / 上游配置都是占位符；首次运行需要逐项填实。
❌ **未做**：没有自动化"一键起整个堆栈"——故意的，因为生产部署必须按节奏来。

按 [`ROADMAP.md`](./ROADMAP.md) 走 12 周，能到 P4 公开运营。

## 必读三件套（按顺序）

1. **[`ROADMAP.md`](./ROADMAP.md)** — 12 周做什么、不做什么
2. **[`launch/pre-launch-checklist.md`](./launch/pre-launch-checklist.md)** — 100+ 项上线准入条件
3. **[`launch/gray-release-sop.md`](./launch/gray-release-sop.md)** — P1→P2→P3→P4 灰度规则

## 三个最容易踩的坑

1. **CRYPTO_SECRET 一旦改，所有上游 Key 全部失效**——务必先备份到 1Password / Vault
2. **多实例必须共用同一份 SESSION_SECRET / CRYPTO_SECRET**——否则会互相踢登录
3. **NODE_TYPE master 只能有一个**——多了定时任务会重复执行

## 快速开始（最小可跑）

```bash
cd deploy
cp .env.example .env
bash scripts/gen-secrets.sh          # 自动生成所有 secret
$EDITOR .env                         # 改 API_DOMAIN/ADMIN_DOMAIN/ACME_EMAIL/ADMIN_ALLOW_CIDR
docker compose -f docker-compose.prod.yml up -d
```

容器都 `healthy` 后 → `https://admin.your-domain.com` → 首次访问进初始化向导，自己设管理员账号和密码。

后续每一步都在 [`ROADMAP.md`](./ROADMAP.md) 里。

## 反向阅读：按角色

- **创始人 / 决策者**：[`ROADMAP.md`](./ROADMAP.md) → [`launch/go-no-go.md`](./launch/go-no-go.md) → [`bi/`](./bi/)
- **SRE / 运维**：[`runbooks/`](./runbooks/) → [`ha/`](./ha/) → [`cicd/`](./cicd/) → [`testing/chaos/`](./testing/chaos/)
- **开发**：[`docs/`](./docs/) → [`smart-routing/`](./smart-routing/) → [`cost-cache/`](./cost-cache/) → [`testing/regression/`](./testing/regression/)
- **运营**：[`sop/`](./sop/) → [`customer-ops/`](./customer-ops/) → [`risk-control/`](./risk-control/) → [`bi/`](./bi/)
- **法务 / 财务**：[`legal/`](./legal/) → [`compliance/`](./compliance/) → [`billing/`](./billing/)
- **上游号池工程师**：[`upstream/`](./upstream/) → [`upstream/payment/`](./upstream/payment/) → [`upstream/budget/`](./upstream/budget/)

## 安全 Checklist（精简版）

完整版在 [`launch/pre-launch-checklist.md`](./launch/pre-launch-checklist.md)。

- [ ] `.env` 文件 `chmod 600`，绝不进 Git
- [ ] `ADMIN_ALLOW_CIDR` 改成你自己的固定 IP
- [ ] SSH：禁密码、改非标端口、装 fail2ban
- [ ] 前置 Cloudflare：开 WAF + Bot Fight + Rate Limit
- [ ] 源站防火墙：只允许 Cloudflare IP 段访问 80/443
- [ ] 备份做过至少一次成功恢复演练
- [ ] `CRYPTO_SECRET` 备份到 1Password / Vault
- [ ] 管理员密码足够强（初始化向导自设，系统无默认账号），开启 2FA
- [ ] 监控告警跑通（Telegram 收到测试消息）

## 故障速查（精简版）

完整版在 [`runbooks/`](./runbooks/) 各 runbook。

| 现象 | 排查路径 |
|---|---|
| 容器循环重启 | `docker compose logs <service>` |
| 502 Bad Gateway | 两个 new-api 都没 healthy；查 `/api/status` |
| 证书签发失败 | DNS 没生效 / 80/443 没通 / Cloudflare 没关橙云 |
| 登录立刻被踢 | SESSION_SECRET 不一致或多机时间差 > 5s |
| 渠道全报 401 | CRYPTO_SECRET 被改过，需要重录所有 Key |
| 用户量大后 CPU 飙高 | 检查日志库膨胀，或 Redis 没生效 |

## 法律与风险提醒（务必看完）

详见原 plan 文件最后一节 + [`legal/`](./legal/)。简言之：

1. 自动化注册流水线 + 接码 + 虚拟卡的组合在多数司法辖区**可能违法**；至少分账户主体、分服务器、分支付主体
2. 上游 ToS 普遍禁止批量注册；号池只是**成本优化**，不能做唯一来源
3. 充值通道（epay/灰色）有跑路风险，重要场景接 Stripe
4. 用户 prompt 全部经过你这台机器——日志脱敏与数据保留窗口必须做
5. 上游突然涨价/限额是常态，永远留 ≥ 30% 利润缓冲
