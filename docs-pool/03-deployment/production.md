# 生产部署

> 本文是**索引页**——具体的生产蓝图、Docker Compose、监控告警、合规文档全部都已经写在 [`<REPO>/deploy/`](../../deploy/) 里。这里只补充与本 fork 强相关的细节。

---

## 0. 先看这个：真实上线的那一套

[`tokwork-nbg1.md`](./tokwork-nbg1.md) —— **2026-10-09 实际上线的部署记录**：
Hetzner 纽伦堡 CPX12 + Docker Compose 四容器 + Cloudflare Pages 前端，
含域名规划、构建链路、资源预算、备份现状与排错速查。

本文余下部分和 [`deploy/`](../../deploy/) 是**通用蓝图**（怎么搭都行的那套），
和上面的实际部署不是同一件事。别把蓝图的资源规划当成线上现状。

## 1. 推到生产前的准备

按 [deploy/ROADMAP.md](../../deploy/ROADMAP.md) Week 0 完成清单：
1. 法律：读 [deploy/legal/](../../deploy/legal/) 的 ToS / Privacy / AUP / Refund，能接受风险。
2. 基建：海外 VPS 4C/8G+、Cloudflare 接管 DNS、≥3 个域名候补。
3. 资金：上游充值 ≥ $200、Stripe 商家或加密钱包、备用 3 个月运营预算。
4. **现成代码**：当前本 fork 的 binary 已能直接跑。

## 2. 标准生产 Stack

```
                    Internet
                       │
                       ▼
              ┌────────────────┐
              │  Cloudflare WAF + DDoS  │   不暴露源站
              └────────┬───────┘
                       │
        ┌──────────────┴───────────────┐
        ▼                              ▼
   api.example.com                admin.example.com
   (开放)                          (Cloudflare Access SSO + 2FA)
        │                              │
        └──────────────┬───────────────┘
                       ▼
                ┌──────────────┐
                │    Caddy     │   反代 + 自动证书
                └──────┬───────┘
                       │
          ┌────────────┴────────────┐
          ▼                         ▼
     new-api A (3000)         new-api B (3001)   两份对等实例
          │                         │
          └────────────┬────────────┘
                       │
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
   Postgres 16     Redis 7         共享 NFS / S3
                                  （logs / data）

  另外旁挂：
   - Prometheus + Grafana + Loki + Alertmanager   ← deploy/monitoring/
   - Uptime Kuma （状态页）                       ← deploy/customer-ops/
   - Chatwoot （工单）                            ← deploy/customer-ops/
```

完整 compose 文件：[`deploy/docker-compose.prod.yml`](../../deploy/docker-compose.prod.yml)。

## 3. 跟本 fork 直接相关的环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `SQL_DSN` | sqlite | 生产换成 `pg://user:pass@host:5432/newapi` |
| `LOG_SQL_DSN` | 同业务库 | 大流量时拆出来 |
| `REDIS_CONN_STRING` | empty | 生产必填 `redis://:pass@host:6379/0` |
| `SESSION_SECRET` | 随机 | **必须**生成强随机串：`openssl rand -base64 32` |
| `CRYPTO_SECRET` | 随机 | 同上 |
| `CHANNEL_UPDATE_FREQUENCY` | 0 | 600 = 每 10 分钟更新一次渠道余额 |
| `BATCH_UPDATE_ENABLED` | false | 多节点时设 true，扣费走批量 |
| `IS_MASTER_NODE` | true | 多节点只有一个 master 跑 cron / pool worker |
| `PoolTelegramBotToken` | 默认已 seed | 生产建议改自己的，避免共用 |
| `PoolTelegramChatId` | 默认已 seed | 同上 |

`.env` 模板：[`deploy/.env.example`](../../deploy/.env.example)。

## 4. 数据库切换：SQLite → Postgres

### 4.1 一次性导出 SQLite
```bash
# 备份
cp <REPO>/one-api.db backup.db
# 导出 SQL（去掉 SQLite 私有语法）
sqlite3 backup.db .dump > dump.sql
```

### 4.2 在 Postgres 起 schema

new-api 启动时会 AutoMigrate 全部表（包括 pool_*）。先空库启动一次让它建表：
```bash
SQL_DSN="postgres://newapi:pass@pg-host:5432/newapi?sslmode=disable" \
  ./new-api-macos --port 3000
# 看见 "Database migrated" 后 Ctrl+C
```

### 4.3 数据搬运

逐表 `pg_restore` 不现实（SQLite dump 含很多 SQLite 方言）。最简方法：
- 用 Python 脚本逐表 `SELECT *` from sqlite → `INSERT INTO` postgres，包括所有 `pool_*` 表 + `channels` + `abilities` + `users` + `tokens` + `options`。
- 字段 1:1 对应（pool 表的 schema 都用通用类型：int / text / bigint / bool）。

模板：

```python
import sqlite3, psycopg2, json
src = sqlite3.connect("backup.db")
src.row_factory = sqlite3.Row
dst = psycopg2.connect("postgres://newapi:pass@pg-host/newapi")
dcur = dst.cursor()
for table in ["users","tokens","channels","abilities","options",
              "pool_accounts","pool_recipes","pool_jobs","pool_alert_history"]:
    rows = src.execute(f"SELECT * FROM {table}").fetchall()
    if not rows: continue
    cols = rows[0].keys()
    placeholders = ",".join(["%s"]*len(cols))
    sql = f'INSERT INTO {table} ({",".join(cols)}) VALUES ({placeholders}) ON CONFLICT DO NOTHING'
    dcur.executemany(sql, [tuple(r) for r in rows])
dst.commit()
```

### 4.4 切流量
- 先停 SQLite 实例。
- 改 `.env` 里的 `SQL_DSN` 到 Postgres。
- 起新实例，访问 `/api/pool/overview` 检查数据齐。

## 5. Caddy 反代示例

`Caddyfile`：

```caddyfile
api.example.com {
  encode zstd gzip
  reverse_proxy /v1/* /api/relay/* /api/* {
    to localhost:3000 localhost:3001
    lb_policy least_conn
    health_uri /api/status
    health_interval 10s
  }
}

admin.example.com {
  # Cloudflare Access JWT 校验在 cf 那侧做
  encode zstd gzip
  reverse_proxy localhost:3000
}
```

## 6. 多区域 / HA

进 P3 阶段才上：
- Postgres Patroni 主从：[`deploy/ha/`](../../deploy/ha/)
- Redis Sentinel：同上
- 跨区域 PG 副本：[`deploy/multi-region/pg-cross-region.md`](../../deploy/multi-region/)
- DNS 智能解析：[`deploy/multi-region/dns-config.md`](../../deploy/multi-region/dns-config.md)

## 7. 多节点时的特殊事项

`main.go` 里有：
```go
if common.IsMasterNode {
    service.StartPoolHealthCron(...)
    service.StartPoolWorker(...)
}
```

**这两个在多节点里只能跑一份**。否则会出现：
- 健康巡检并发跑 → Telegram 重复推送
- pool worker 并发抢 job → 同一个 job 跑两次 → 两个上游账号

部署多节点时：
- 只在一个节点设 `IS_MASTER_NODE=true`（或不设，看 main 默认值）。
- 其他节点设 `IS_MASTER_NODE=false`。
- 用 K8s 的话，加一个 leader-elect sidecar。

## 8. 上线 Checklist

走 [`deploy/launch/pre-launch-checklist.md`](../../deploy/launch/pre-launch-checklist.md)，凡是号池相关补这几项：
- [ ] 默认 Telegram Bot 和 ChatId 已改成自己生产专用
- [ ] 5sim API Key 已改成自己充值的（如要用自动 Runner）
- [ ] 默认 root 密码已改 / root 默认令牌已删除
- [ ] 所有内置 Recipe 检查一遍 BaseURL（厂商可能改）
- [ ] 巡检 cron 在生产真的跑起来了：`grep "[pool-health]" logs/`
- [ ] Telegram 测试推送收到了：后台「巡检告警」→「测试 Telegram」

## 9. 监控告警

号池侧告警走 Telegram（不走 Alertmanager），因为：
- 告警目标受众单一（管理员）
- 数量级很小（每天 < 50 条）
- Telegram 推送自带去重 / 历史

如果想接 Alertmanager / PagerDuty，改 `service/pool_health.go::SendTelegramMessage`，加 webhook 适配。

监控大盘：[`deploy/monitoring/grafana/dashboards/`](../../deploy/monitoring/grafana/dashboards/) 已有 5 个看板。号池专用看板未单独做（用 Overview 页就够），如要单做，写一个 PG 查询 `pool_accounts` / `pool_alert_history` 即可。
