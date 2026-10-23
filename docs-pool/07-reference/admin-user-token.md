# 管理员代用户创建 Token API

> 2026-10-20 新增。给 Rakazo 这类外部系统做用户映射用；Rakazo 不接触 New API 用户令牌。
>
> **2026-10-20 已部署到生产并验证**：`idempotency_name` 列与唯一索引已由 AutoMigrate
> 自动创建；管理员代建、幂等、权限拒绝和真实调用均通过；测试 token id `16`
> （name `rakazo-deploy-test-20261020-121545`）保留在 demo 用户下，可在门户删除。

## 路由

```
POST /api/user/:id/token
```

鉴权：`middleware.AdminAuth()`。

- `Authorization: Bearer <管理员 access token>`（也接受不带 Bearer 前缀，沿用 `ValidateAccessToken`）
- `New-Api-User: <管理员自己的 user id>`，必须与 access token 对应的用户一致

## 请求

```json
{
  "name": "rakazo-<stable-user-key>",
  "unlimited_quota": true,
  "remain_quota": 0,
  "expired_time": -1,
  "model_limits_enabled": false,
  "model_limits": "",
  "allow_ips": "",
  "group": "",
  "cross_group_retry": false
}
```

`unlimited_quota` 缺省时按 `true`；`expired_time` 缺省时按 `-1`。`name` 必填，最长 50。

## 成功响应

```json
{
  "success": true,
  "message": "",
  "data": {
    "id": 123,
    "key": "sk-..."
  }
}
```

`data.key` 是**完整 key**。只有创建和幂等读取（同名重复请求）会返回它；
Token 列表、详情、更新接口仍然只返回 masked key。

## 失败

沿用 New API 管理接口风格：HTTP 200 + `success:false`（鉴权中间件可能返回 401），
`message` 为稳定的 i18n 文案。不会返回 access token、完整 API key、数据库错误或内部路径。

## 权限与目标用户

1. 调用者必须是 admin（`AdminAuth`）。
2. 目标用户必须存在且 `status == common.UserStatusEnabled`。
3. 调用者角色必须**严格高于**目标角色：
   - admin 不能替同级 admin 建；
   - admin 不能替 root 建；
   - root 也不能替另一个 root 建；
   - root 可以替 admin / 普通用户建。

## 校验复用

新接口和原 `/api/token/` 共用 `controller/token.go` 里的：

- `validateTokenForCreate`：名称长度、额度范围、每用户 token 数量上限
- `createTokenForUser`：生成 key、写入 UserId/Key/Status/Name/ExpiredTime/Group 等字段

普通 `/api/token/` 的行为不变：仍只给调用者自己建 token，且不写入幂等键。

## 幂等与并发

- `tokens` 新增可空列 `idempotency_name`（`varchar(50)`）。
- 唯一索引：`idx_tokens_user_idempotency (user_id, idempotency_name)`。
- 管理员代建时写入 `idempotency_name = name`；普通 token 保持 `NULL`，因此 MySQL 允许多个 NULL，不影响原行为。
- 同一目标用户 + 同一 name：
  - 已存在未删除 token → 直接返回它的完整 key；
  - 并发首次创建 → 唯一索引保证只成功一条，输的一方 insert 失败后回查并返回同一条的 key；
  - 软删除时清空 `idempotency_name`，因此删除后可以用同名重新创建。
- 首次查询用 `Find + RowsAffected`，不把正常的 “not found” 打成 SQL 错误日志。

## 密钥安全

- Token 插入和按 key 查询这两处使用 `gorm/logger.Discard`，避免 GORM 在唯一索引冲突/慢查询日志里打印完整 key。
- Controller 的错误日志只包含 caller/target user id，不包含 key 或数据库错误原文。
- 创建响应加 `Cache-Control: no-store`。

## 测试

`controller/admin_user_token_test.go` 覆盖：

- 管理员替普通用户创建
- 非管理员被拒绝
- `New-Api-User` 不匹配被拒绝
- 目标不存在 / 被禁用被拒绝
- admin 不能操作同级 admin、不能操作 root；root 不能操作另一个 root
- 重复调用幂等
- 并发首次调用只创建一条
- 删除后同名可重建
- 共享名称/额度/数量上限校验
- 错误响应和 sys log 不含完整 key
- 唯一幂等索引存在
