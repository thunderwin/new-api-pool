# 安全与合规

> 这一章是给"管理员"+"老板"+"安全审计"看的。
> 总原则：**密钥不外露、默认不暴露、上游 ToS 不冒犯**。

---

## 1. 默认账号 / 默认凭证清单

fork 启动后会有一些**默认值**，**全部要在上线前改掉**：

| 项 | 默认值 | 危害 | 怎么改 |
|---|---|---|---|
| 管理员密码 | **无默认值**（首次访问的初始化向导里自设） | 向导只要求 ≥8 位，弱密码等于把后台敞开 | 首次设置时就直接用强密码（≥ 12 位） |
| Default Token | 安装时生成的 access_token | 拿到的人能调所有上游 | 后台「令牌」→ 删除/吊销原始 token，重建 |
| Telegram Bot Token | 启动时空（开源版默认） | 必须填上自己的 Bot Token 才能推送告警 | 「号池管理 → 巡检告警 → ⚙️ Telegram 设置」<br>或环境变量 `POOL_TELEGRAM_BOT_TOKEN` |
| Telegram Chat Id | 启动时空（开源版默认） | 同上 | 同上 / `POOL_TELEGRAM_CHAT_ID` |
| 5sim / mail.tm Key | 启动时空 | 不填没影响；填了泄露会被刷余额 | 「自动注册 → ⚙️ 自动化设置」录入；定期轮换 |

**自检 SQL**：
```sql
-- 管理员是 role=100 的那个用户（名字由初始化向导决定，不一定是 root）
SELECT id, username, role FROM users WHERE role = 100;
SELECT \`key\`, value FROM options WHERE \`key\` LIKE 'PoolTelegram%';
SELECT \`key\`, value FROM options WHERE \`key\` LIKE 'Pool%ApiKey';
```

---

## 2. 密钥分级与处置

| 类别 | 例子 | 存储 | 谁能看明文 | 备份策略 |
|---|---|---|---|---|
| **上游 LLM API Key** | OpenAI sk-... / Gemini AIza... | `channels.key`（明文） + `pool_accounts.key_masked`（脱敏） | 后台管理员 | 备份 db，**离线加密保存** |
| **用户访问令牌** | tokens.key（sk-xxx 给业务方） | `tokens.key`（明文，业务方拿走） | 业务方 + 管理员 | 业务方自己保管 |
| **管理员 access_token** | "复制系统访问令牌" | `tokens.key`（明文，user.role=10） | 管理员本人 | 只在自己电脑 / 1Password |
| **Telegram Bot Token** | `123456789:AAxxxx...` | `options.value`（明文） | 后台 | 备份 db，离线 |
| **5sim API Key** | jwt token | `options.value`（明文） | 后台 | 备份 db，离线 |
| **数据库密码（生产）** | Postgres pw | 环境变量 / Vault | 运维 | 不入 git，不入备份 |

**核心规则**：
- **明文 Key 只在数据库里**，前端列表全部脱敏（`****abcd`）
- 真要复制时点「显示完整 Key」按钮 → 调 `GET /api/pool/accounts/:id/full_key`（一次性返回）
- 不要把数据库备份明文丢公开 OSS / 共享网盘
- 备份命名不要含 `.db.unencrypted` / 含 `key` 字样以避免被脚本扫到

---

## 3. 网络暴露面

### 必须暴露
| 端口 | 谁访问 | 协议 |
|---|---|---|
| `:80/443` | 终端用户调 `/v1/...` | HTTP/HTTPS（用 Caddy/Nginx 终结 TLS） |
| `:80/443` | 管理员开后台 | HTTPS only，**强制 HSTS** |

### 不应暴露
| 端口/路径 | 风险 | 防护 |
|---|---|---|
| `:3000` 直接对公网 | 没 TLS、没 WAF、没限速 | 用反向代理收口 |
| `/api/pool/*` 给非管理员 | 后台数据 / Key 全暴露 | 接口已有 `AdminAuth` 中间件，**不要绕过** |
| `/api/pool/jobs/:id/callback?token=...` 公网 | 第三方瞎传 callback | option `PoolWorkerSharedToken` 不为空时强制校验；**没用就把 token 留空 = 任何 callback 拒绝** |
| `pprof` / `/debug/*` | 内存 / goroutine dump | 生产 build 加 `-tags=prod` 关掉 |
| 备份目录列目录 | 泄露 .db | nginx/Caddy 显式 `respond / 404` |

### 推荐网关配置（Caddy）
```caddyfile
api.example.com {
    encode gzip

    # 后台限制 IP
    @admin path /api/pool/* /panel/*
    @admin remote_ip 1.2.3.4/32 5.6.7.0/24
    handle @admin {
        reverse_proxy localhost:3000
    }

    # 公开 API
    reverse_proxy localhost:3000

    # 安全 headers
    header {
        Strict-Transport-Security "max-age=63072000; includeSubDomains; preload"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        Referrer-Policy "no-referrer"
        Content-Security-Policy "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'"
    }

    # 限速（Caddy v2 第三方插件 mholt/caddy-ratelimit）
    rate_limit {
        zone perip {
            key {client.ip}
            events 60
            window 1m
        }
    }
}
```

---

## 4. RBAC（权限体系）

new-api 自带 3 级角色：

| `users.role` | 名称 | 能做什么 | 能进号池管理？ |
|---|---|---|---|
| 1 | 普通用户 | 调 `/v1/...` + 个人设置 + 创建自己的 token | ❌ |
| 10 | 管理员（admin） | 上面 + 用户管理 + 渠道管理 + **号池管理** | ✅ |
| 100 | 超级管理员（root） | 全部 + 系统设置 + 看所有 token | ✅ |

**不要**给业务方 admin 角色，只给普通用户角色 + 单独一份 token。

**审计**：
```sql
SELECT id, username, display_name, role, status, created_time
FROM users
WHERE role >= 10
ORDER BY id;
```

定期复核，离职/调岗的人 `UPDATE users SET status=2` 软封即可。

---

## 5. 审计日志

| 行为 | 落表 | 字段 |
|---|---|---|
| 用户调 `/v1/*` | `logs` | user_id / channel_id / model / quota / created_at |
| 用户充值 | `topups` | amount / status / created_time |
| 管理员手动操作 Pool | **暂未独立审计** ⚠️ | 仅 `pool_jobs.operator_id` 留下"是谁触发的注册" |
| Telegram 推送 | 应用日志 `data/logs/oneapi-*.log` | |
| 管理员登录 / 改密 | 应用日志 + `users.last_login_time` | |

**已知缺口**：管理员**对 PoolAccount / Channel 的 CRUD 没有独立审计表**。如果你的合规要求"谁在什么时候改了哪个上游 Key"，要自己加：

```go
type PoolAuditLog struct {
    Id        int    `gorm:"primaryKey"`
    UserId    int    `gorm:"index"`
    Action    string `gorm:"size:64"`        // pool_account.update / pool_account.full_key.read
    Target    string `gorm:"size:128"`        // pool_account:42
    Detail    string `gorm:"type:text"`
    CreatedAt int64
}
```

在 `controller/pool.go` 关键路径（`UpdatePoolAccount` / `GetPoolAccountFullKey` / `BindPoolAccountChannel`）`PoolAuditLog{...}.Insert()` 一行记录即可。

---

## 6. 上游 ToS 风险

每个上游服务条款不一样，常见红线：

| 行为 | 风险 |
|---|---|
| 用脚本批量注册免费帐号 | OpenAI / Anthropic / Together / OpenRouter 都 ban，并可能挂钩信用卡黑名单 |
| 一个人多张信用卡注册付费帐号 | 同上 + 退款被拒 |
| 卖给第三方 / 商业转售 | OpenAI / Anthropic API ToS 明确禁止"中转转售给非企业实体"，需要走 Azure OpenAI / Anthropic Bedrock 等合规通道 |
| 把官方 API key 给学生 / 公开论坛 | 一旦被滥用 + 投诉，账号 ban，余额清零 |

**建议防御**：
- 所有付费上游账号**用同一个公司主体**信用卡 + 公司邮箱注册
- 转售前把业务模式写清楚 + 看 OpenAI/Anthropic 商业条款是否允许
- 给终端用户的 token 必须能限速 / 限模型 / 限分组（new-api 原生有这些）
- 用户协议里写明"我们是中转 / proxy 服务，不为终端内容负责"——**不能免责但是减少法律风险**

详细参考 [`deploy/legal/`](../deploy/legal/) 与 [`deploy/compliance/`](../deploy/compliance/) 下的合规文档模板。

---

## 7. 常见漏洞自检清单

### 7.1 凭证泄露面
- [ ] `git log --all --full-history -- '**/.env*'` —— 历史 commit 没夹过 `.env`
- [ ] `git log -S 'sk-' --all --oneline` —— 没把上游 Key 写到代码里 commit
- [ ] 备份目录权限 `chmod 700`，所有者 root
- [ ] 备份不在 GitHub / 公开 OSS bucket
- [ ] CI 日志没打印 access_token / api_key

### 7.2 后台暴露面
- [ ] `https://your-domain/api/status` 返回 200 但不暴露版本号 + ip 列表（如确需，用网关层处理）
- [ ] `https://your-domain/api/user/login` 没开放未授权批量爆破（开 ratelimit / fail2ban）
- [ ] 后台路径 `/panel/*` 加 IP 白名单（运营公司公网 IP）
- [ ] HTTPS 强制 + HSTS 6 个月以上

### 7.3 Token / Key 处理
- [ ] `tokens.key` 在前端不显示历史明文（只在创建时给一次）
- [ ] `channels.key` 列表展示一律脱敏（`maskAPIKey` 在 controller 里调）
- [ ] 「号池管理 → 上游账号 → 复制完整 Key」按钮在审计日志里有记录（如已加 PoolAuditLog）
- [ ] 「自动化设置」里的 5sim key / fivesim key 列表展示掩码

### 7.4 接口
- [ ] `/api/pool/jobs/:id/callback` 必有 token 校验（`PoolWorkerSharedToken` 不为空）
- [ ] 删除 PoolAccount 后，关联 channels 是否一并禁用？（当前不会，需手动清理 → 写入运营 SOP）
- [ ] 改 PoolAccount.key_raw 后是否同步 channel.key？（已有同步逻辑）

### 7.5 上线前
- [ ] root 密码已改
- [ ] Telegram bot token 已改成自己的
- [ ] 默认创建的 access_token 已删
- [ ] 防火墙只开 80/443（不暴露 3000）
- [ ] 备份脚本跑过一次 + 验证可启动
- [ ] 准备了"上游突然 ban"的应急预案：换 Recipe → 该 channel 禁用 → 余额转移给同 group 其他 channel

---

## 8. 应急响应（Key 泄露怎么办）

### 8.1 上游 Key 泄露（如：终端用户截图把 channel key 发出来了）
1. 进后台「渠道管理」找到该 channel → **立即禁用**（status=2）
2. 去对应上游官方控制台 **revoke** 这个 key
3. 重新申请新 key → 在「号池管理 → 上游账号 → 编辑」录入（系统会同步 channel.key）
4. 启用 channel
5. 看 `logs` 表，受影响时段被滥用了多少 quota，必要时给已付费用户补偿
6. 写复盘：泄露途径（截图？日志？前端？）→ 加防御

### 8.2 admin access_token 泄露
1. 后台「令牌」→ **吊销**（不只是禁用，要删）
2. 查 `logs` 看是否被用过
3. 改 root 密码
4. 重新生成 access_token（这个**只复制给 1Password**，不发 IM）

### 8.3 数据库泄露（如：服务器被入侵）
- **最坏情况** = 所有上游 key + 所有用户 token 全公开
- 立即执行：
  1. 停服务 + 断网
  2. 上游官方控制台**全部 revoke**所有 key
  3. 让用户重新生成 token（在新机器恢复备份 → 但所有 token.key 已不可信 → 强制改 token 字段）
  4. 重新申请 / 部署 / 录入
- 这是为什么备份要离线 + 加密

---

## 9. 隐私（GDPR / 个保法）

- **采集的数据**：用户邮箱 + 充值记录 + 调用日志（含 prompt / completion 内容）
- **数据所在地**：你的服务器（境内 / 境外，按部署位置）
- **保留期**：建议 logs 30 天滚动清理 + topups 永久保留（财务凭据）
- **用户权利**：能不能要求删除？— 法律要求能。后台「用户管理」删除时**真删 logs/tokens/topups 关联**，不只是软删 user 表
- **合规建议**：在 ToS / Privacy Policy 里写清楚"我们会把 prompt 转发给上游 LLM 提供商，他们的隐私政策同样适用"

---

## 10. 拿不准就按这个原则

> 默认不暴露，默认最小权限，默认有备份，默认有审计。
> 但凡你犹豫"要不要把这个开到公网" / "要不要给这个人 admin"——答案是**不**。
