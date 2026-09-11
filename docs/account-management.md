# 账号创建与后台权限速查

本文按现有接口整理。完整字段见 [OpenAPI](openapi.yaml)，更多接入流程见 [前端接入说明](frontend-api.md)。以下密码均为示例占位，实际使用时替换，不在仓库保存真实密码。

## 接口选择

| 目的 | 接口 | 调用者权限 |
| --- | --- | --- |
| 创建真实玩家 | `POST /v1/admin/users` | admin/operator，需后台二级密码 |
| 创建虚拟玩家 | `POST /v1/admin/virtual-accounts` | admin/operator |
| 将已有账号设为后台管理员或运营 | `PUT /v1/admin/users/{userID}/roles` | 仅 admin |
| 查询可分配角色 | `GET /v1/admin/roles` | 仅 admin |
| 登录后台 | `POST /v1/admin/auth/login` | admin/operator 账号 |
| 登录玩家端 | `POST /v1/auth/login` | player 账号；拒绝后台角色 |

管理接口携带 `Authorization: Bearer <后台访问令牌>`。`second_password` 是后台全局二级操作密码，不是新用户登录密码，也不是用户个人交易密码。

## 创建真实玩家

`POST /v1/admin/users`：

```json
{
  "login_name": "player001",
  "password": "replace-this-password",
  "display_name": "玩家昵称",
  "second_password": "后台二级密码"
}
```

账号名为 3–32 位英文、数字、下划线或连字符；登录密码为 12–128 字节。昵称可省略，默认使用“用户”加公开 ID。可选头像 `avatar_url` 必须是当前操作人已确认上传的图片 storage_key。

成功返回 201，包含 `user_id`、`is_virtual:false` 和 `roles:["player"]`。自动创建默认零余额钱包；不接受 roles、is_virtual、initial_balances 或批量 count。重复登录名返回 409，不覆盖旧账号。后台创建不要求邀请码；需要绑定上级时另用 `PUT /v1/admin/users/{userID}/agent-relation`。

## 创建虚拟账号

`POST /v1/admin/virtual-accounts`：

```json
{
  "login_name": "sadewf",
  "count": 99,
  "password": "replace-this-password"
}
```

批量 count 使用 2–100，以上生成 sadewf1 到 sadewf99，共用请求密码；昵称省略时，每个账号随机生成 2–5 个汉字，返回 `{"items":[...]}`。当前实现按账号逐个提交，中途失败可能已经创建部分账号；重试前先查询确认。

单个创建省略 count 或传 1，login_name 就是完整账号名，不追加编号，返回单个账号对象；空昵称默认“用户”加 ID。虚拟账号密码只要求非空且不能全是空白，不限制长度，仍受请求体大小限制。初始余额 `initial_balances` 可选，按实际币种金额填写。创建后不会自动启动模拟投注，需另外配置 `/v1/admin/robot-plans`。

虚拟账号支持充值，但禁止下分、清退和提现；经营看板排除虚拟账号。跟踪接口 `/v1/admin/monitor/bets` 默认查询全部，传 `player_type=real` 仅查询真实用户，`virtual` 仅查询虚拟用户；兼容聚合接口 `/v1/admin/monitor` 仍仅返回真实用户投注。创建账号接口没有请求幂等键，超时后先按登录名查询。

## 创建后台账号：先建账号，再分配角色

目前没有一步创建后台账号的专用接口，按以下顺序操作：

1. 调用 `POST /v1/admin/users` 创建一个新的真实账号，保存响应的 `user_id`。
2. 使用现有管理员令牌调用 `PUT /v1/admin/users/{userID}/roles`。例如返回 user_id 为 100014，就调用 `/v1/admin/users/100014/roles`。
3. 新账号通过 `POST /v1/admin/auth/login` 登录后台。

步骤 2 创建管理员时请求体：

```json
{"roles":["admin"]}
```

创建运营时请求体：

```json
{"roles":["operator"]}
```

roles 是全量替换，不是追加。此接口当前不接收 second_password，仅管理员令牌可调用；operator 不能分配角色。修改后撤销目标账号旧登录会话，应重新登录。不能移除自己的 admin 角色，也不能移除平台最后一个管理员角色。两步操作不是一个事务；若第二步失败，已创建账号仍保留为 player，可对同一 user_id 重试角色设置。

平台第一个管理员通过 `bootstrap-admin` 命令初始化，具体命令见 [README 初始化首个管理员](../README.md#初始化首个管理员)。平台已有管理员后，该命令拒绝再次创建。

## 查询与 ID

用户查询：`GET /v1/admin/users?user_type=real` 或 `user_type=virtual`，省略查全部；q 可按公开 ID、登录名、昵称搜索。

按币种查询并按可用余额从高到低排序：`GET /v1/admin/users?currency=USDT&limit=50&offset=0`。多币种如 `currency=USDT,JADE` 以第一个币种 USDT 排序，不跨币种相加；缺少排序币种钱包的用户排最后。同额按创建时间、公开 ID 降序，排序在分页前完成。未传币种时保留创建时间、公开 ID 降序。

投注查询：`GET /v1/admin/bets?player_type=real` 或 `player_type=virtual`，省略或 all 查全部。注意用户列表使用 user_type，投注列表使用 player_type。

创建响应的 user_id 和用户列表的 id 都是公开数字 ID，管理路径用该值，不使用内部 UUID。执行 0081 迁移后，历史用户按旧 ID 顺序重新编号为 10001 起的五位数，invitation_code 同步一致；新用户继续顺序分配，最大99999且不循环。user_public_id_history 保存旧新对照，审计历史原样保留。旧六位ID不可继续使用，前端应重新加载用户列表及本人资料，文中旧ID示例需替换为实际响应ID。部署脚本自动执行缺失迁移，但服务器必须先同步包含该迁移的代码；迁移编号不能重复。
