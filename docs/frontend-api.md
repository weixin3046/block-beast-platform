# 前端接口接入

完整机器可读的接口定义在 [openapi.yaml](./openapi.yaml)。可直接导入 Swagger UI、Postman、Apifox，或用 OpenAPI Generator 生成 TypeScript 客户端。

本地 API 地址为 `http://localhost:8080`。所有接口使用 JSON，错误统一为：

```json
{ "error": "请求参数格式不正确" }
```

后端统一返回简体中文提示，前端可直接显示 `error`，不需要再次翻译。HTTP 状态码、字段名、`business_type`、`status`、币种代码和 Socket `type/subject` 均保持原值；不要通过比较中文提示判断业务分支。未知内部错误返回“操作失败，请稍后重试”，不会透传数据库或第三方错误详情。用户自己填写的昵称、聊天、公告和备注不自动翻译。

常见提示：账号或密码不正确、请先登录、权限不足、可用余额不足、二级密码不正确、修改二级密码失败、今日已领取该任务奖励。404/405 的默认接口错误也返回中文 JSON；Socket 业务错误、握手失败和服务端主动关闭的说明为中文。

## 账号与币种

平台初始化八种钱包币种；注册根据币种目录中的 `enabled && create_on_registration` 创建零余额钱包（默认八种）。真实与虚拟用户使用同一规则：

| 币种 | 用途 | 来源 |
|---|---|---|
| `USDT` | 投注、提现 | 链上充值回调自动入账，或管理员后台充值 |
| `POINTS`（宝石） | 投注、红包 | 管理员后台充值 |
| `JADE`（玉石） | 投注、红包 | 管理员后台充值 |
| `ORIGIN_STONE`（源石） | 投注、红包 | 管理员后台充值 |
| `STAMINA` | 宝石体力 | 宝石投注任务奖励，用于宝石转盘 |
| `USDT_STAMINA` | USDT 体力 | USDT 投注任务奖励，用于 USDT 转盘 |
| `JADE_STAMINA` | 玉石体力 | 玉石投注任务奖励，用于玉石转盘 |
| `ORIGIN_STONE_STAMINA` | 源石体力 | 源石投注任务奖励，用于源石转盘 |

## 调用顺序

1. 玩家端调用 `POST /v1/auth/register` 注册时必须填写 `invitation_code`。邀请码从 `10001` 起，且只有后台设置为 1–6 级代理的用户的邀请码可用；注册会原子建立直属推荐关系。登录后调用 `GET /v1/users/me` 获取玩家资料、邀请码和 `agent_level`（`0` 表示不可邀请）。已有账号调用 `POST /v1/auth/login` 登录，并使用 `POST /v1/auth/refresh` 续期；管理后台使用 `/v1/admin/auth/login` 和 `/v1/admin/auth/refresh`。
   `user_id` 是从 `100000` 起连续分配的公开数字 ID；UUID 只在服务端内部使用。玩家可用 `PUT /v1/users/me` 修改昵称和头像，用 `PUT /v1/users/me/password` 携带当前密码和新密码修改密码；改密后需重新登录。头像和二级密码的完整链路见下文。
2. 调用 `GET /v1/rounds?game_type={code}` 获取仍可下注的轮次。
   游戏页同时调用 `GET /v1/rounds/state?game_type={code}` 展示当前轮次封盘倒计时
   与最近一期已结算结果；倒计时始终以响应中的 `bet_closes_at` 为准。使用同一响应的
   `server_time` 与收到响应时的本地时间计算时差，避免设备时钟快慢造成 1–2 秒误差。
3. 调用 `POST /v1/bets` 创建投注。共享哈希轮次还必须提交 `game_room_id` 和 `play_mode`：竞猜/躲避使用 `selection={"pick":"0"}` 至 `{"pick":"9"}`，上下路使用 `big`、`small`、`odd`、`even`。`currency` 可传 `USDT`、`POINTS`（宝石）、`JADE`（玉石）或 `ORIGIN_STONE`（源石）。浏览器应为每次用户确认操作生成稳定的 `client_request_id`；网络重试必须复用该值。`account_id` 必须与令牌主体一致（本人），否则返回 403。
4. 使用 `GET /v1/bets/{betID}` 轮询投注状态；当前状态有 `accepted`、`won`、`lost` 与 `refunded`。
   玩家在 `bet_closes_at` 前可调用 `POST /v1/bets/{betID}/cancel` 取消自己的投注并原路退款；封盘后返回 409。
5. 使用 `GET /v1/wallets/{accountID}?currency=USDT` 查询单币种余额，或 `GET /v1/wallets/{accountID}/all` 一次拉取全部币种。
6. 体力只通过参与平台活动任务获得，不再提供每日签到领取体力接口。参加活动时调用 `POST /v1/stamina/consume` 扣体力，`activity_id` 由活动方提供；体力不足返回 409。
7. 大厅调用 `GET /v1/announcements` 获取当前时间窗口内启用的公告；该接口无需登录。

轮次响应同时包含 `bet_closes_at` 和 `result_at`。前者是停止接受投注的时刻，
后者是目标区块结果可用后的开奖时刻；倒计时必须以服务端字段为准。封盘后到开奖前不得
继续提交投注，也不得把本地时间或行情提前当作开奖结果。

前端校准示例：

```ts
const state = await api.get(`/v1/rounds/state?game_type=${code}`)
const serverOffsetMs = Date.parse(state.server_time) - Date.now()
const remainingMs = () => Math.max(
  0,
  Date.parse(state.current.bet_closes_at) - (Date.now() + serverOffsetMs),
)
```

管理后台可用 `GET/POST /v1/admin/announcements` 和 `PUT /v1/admin/announcements/{id}` 管理公告。`operator` 与 `admin` 均可管理公告；不可变审计日志 `GET /v1/admin/audit-logs?action={action}&actor_user_id={id}` 仅 `admin` 可查看。

## 修改头像闭环

`PUT /v1/users/me` 不接受任意外链或其他用户的文件。`avatar_url` 只能为空字符串，或当前用户本人已经确认的 JPEG、PNG、WebP 上传记录的 `storage_key`。完整顺序如下：

1. 调用 `GET /v1/users/me` 保存当前 `display_name`。资料更新是完整 `PUT`，即使只换头像也必须同时提交非空昵称。
2. 调用 `POST /v1/uploads/authorize`，请求体例如 `{"content_type":"image/png","size_bytes":12345}`。头像只选择 `image/jpeg`、`image/png` 或 `image/webp`；上传服务虽然也支持 PDF 文件，但 PDF 不能设为头像。
3. 根据响应上传文件：
   - `requires_auth=true` 的本地存储授权：对响应的 `url` 发 `PUT`，携带 Bearer Token、响应指定的 `Content-Type`，请求体为原始二进制。成功响应已经包含 `status=confirmed`，不要再重复确认。
   - S3 兼容授权：对响应的签名 `url` 发 `PUT`，严格携带响应的 `headers`，不携带平台 Bearer Token；上传成功后再调用 `POST /v1/uploads/{upload_id}/confirm`。只有确认返回 `status=confirmed` 才能继续。
4. 调用 `PUT /v1/users/me`，提交 `{"display_name":"玩家一号","avatar_url":"uploads/内部用户ID/上传ID"}`。这里必须使用授权/确认响应里的 `upload.storage_key`，不能提交签名 URL，也不能提交最终的 `/v1/avatars/...` 展示地址。
5. 保存成功响应中的 `avatar_url` 是公开展示地址，例如 `/v1/avatars/100009?v=上传ID`。前端拼接 API Origin 后可直接作为 `<img src>`；聊天、公开投注和排行榜也会返回同一类公开地址。空字符串表示未设置头像。

清除头像时不需要上传文件，读取当前昵称后提交 `{"display_name":"当前昵称","avatar_url":""}`。未确认、已过期、非本人或非图片上传会返回 400，原资料不会被部分更新。

## 二级密码设置、修改与验证

登录后先调用 `GET /v1/users/me`，通过 `secondary_password_set` 判断进入“首次设置”还是“修改”流程：

- 首次设置：`PUT /v1/users/me/secondary-password`，请求体 `{"secondary_password":"新二级密码"}`，成功返回 204。已经设置却不带旧密码时返回 409。
- 修改：对同一接口提交 `{"current_secondary_password":"旧二级密码","secondary_password":"新二级密码"}`。旧密码错误返回 401，成功返回 204。
- 单独验证：`POST /v1/users/me/secondary-password/verify`，请求体 `{"secondary_password":"待验证密码"}`。验证通过返回 204，错误返回 401，尚未设置返回 409。

服务端以数据库中的实际设置状态为准：尚未设置时，即使前端因缓存状态误带了非空的 `current_secondary_password`，也会按首次设置处理；已经设置后仍必须提供正确的旧二级密码。前端仍应优先使用 `secondary_password_set` 选择请求体，避免让用户误解操作类型。

二级密码必须是 JSON 字符串且去除首尾空白后不能全空；当前没有数字位数、字符种类或最短长度规则，也没有找回/重置接口。服务端保存 Argon2id 不可逆哈希，任何读取接口都不会返回密码。设置或修改二级密码不会撤销登录会话；它也不会自动保护提现或其他业务接口。目前“验证”接口只返回本次校验结果，不签发可供后续敏感操作消费的凭证，因此前端不能把它当作服务端强制的二次认证。如需强制保护某项资金操作，必须由该业务接口在服务端直接校验二级密码或消费一次性验证凭证。

角色管理仅对 `admin` 开放：`GET /v1/admin/roles` 查询 `player`、`operator`、
`admin` 标准角色，`PUT /v1/admin/users/{userID}/roles` 使用
`{"roles":["operator"]}` 替换目标账号的完整角色集合。成功后目标账号所有刷新
会话立即撤销。接口禁止管理员移除自己的 `admin`，也禁止移除系统最后一个
`admin`；用户禁用接口也禁止管理员禁用自己或最后一个有效 `admin`。这些情况返回 409。

平台动态配置使用 `GET /v1/configs/{key}` 读取，只有 `visibility=public` 的配置
可匿名访问。管理后台使用 `GET /v1/admin/configs?visibility=public` 查询配置，
并使用 `PUT /v1/admin/configs/{key}` 创建或更新：

```json
{
  "value": {"enabled": true, "text": "活动进行中"},
  "visibility": "public",
  "expected_version": 0
}
```

创建时 `expected_version=0`；更新时必须传当前版本。其他管理员已经更新时返回
409，后台应重新读取后让操作者确认，不得静默覆盖。配置只保存非敏感业务 JSON，
API 密钥、密码和令牌必须继续使用环境变量或密钥管理系统。

玩家通过 `GET /v1/hash/menus` 一次读取固定六个赔率房间、每个房间相同的
9/13/17/19/23/29 区块菜单，以及当前币种的竞猜、躲避、上下路倍率和累计上限。
运营后台使用 `GET /v1/admin/hash/config` 读取完整矩阵，并通过
`PUT /v1/admin/hash/config` 携带 `expected_version` 原子保存。六个房间及六个共享
区块关系固定，只允许修改名称、排序、启停状态和各币种参数。
倍率使用 `multiplier/divisor` 定点整数；投注上下限字段统一使用钱包最小单位。
例如 USDT 按 6 位精度时，`100000` 表示 0.1 USDT，`50000000` 表示 50 USDT。
后台不再提供创建房间、创建玩法或人工创建哈希轮次的接口。

走势图调用 `GET /v1/hash/trends?game_type=hash_9&limit=100`。返回结果按目标区块
高度倒序排列，每条包含 `digit`、`size`、`parity` 和 `settled_at`；`summary`
同时提供数字 0–9 的当前遗漏期数，以及最新大小、单双的连续出现次数。六个赔率
房间共用走势图，切换赔率房间时不需要重新请求不同数据。

TRON 平均每 3 秒一个区块，Worker 根据当前区块高度 H 计算
`(floor(H/N)+1)×N` 作为下一目标块并把高度保存为轮次号。每期提前 5 秒封盘；
目标区块实际取得后立即结算，不设置 1 秒或其他人为结算延迟。六个房间使用同一
目标区块和开奖结果，赔率在投注成交时快照，因此后台修改参数不会影响历史投注。

## TypeScript 示例

```ts
const api = "http://localhost:8080";

// 1. 注册（已有账号则改为 /v1/auth/login）
const auth = await fetch(`${api}/v1/auth/register`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ login_name: "player-001", password: "开发环境密码", invitation_code: "10001" }),
}).then((r) => (r.ok ? r.json() : r.json().then(({ error }) => Promise.reject(new Error(error)))));

let { access_token, refresh_token, user_id } = auth;

// access_token 过期后轮换；旧 refresh_token 随即失效
const refreshed = await fetch(`${api}/v1/auth/refresh`, { // 后台改用 /v1/admin/auth/refresh
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ refresh_token }),
}).then((r) => r.json());
({ access_token, refresh_token } = refreshed);

// 2. 携带令牌投注（currency 可为 USDT 或 POINTS）
const response = await fetch(`${api}/v1/bets`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${access_token}`,
  },
  body: JSON.stringify({
    client_request_id: crypto.randomUUID(),
    round_id: roundId,
    account_id: user_id,
    currency: "POINTS",
    selection: { pick: "7" },
    stake_minor: 2500,
  }),
});

if (!response.ok) {
  const { error } = await response.json();
  throw new Error(error);
}
const bet = await response.json();

// 3. 查询全部余额
const balances = await fetch(`${api}/v1/wallets/${user_id}/all`, {
  headers: { Authorization: `Bearer ${access_token}` },
}).then((r) => r.json());

```

## 流水查询

- 本人所有币种统一流水：`GET /v1/users/me/ledger?currency=POINTS&limit=50`；不传 `currency` 则查询本人全部币种。返回 `{items,next_cursor}`，下一页原样传 `cursor=next_cursor` 并保持币种筛选不变，没有 `next_cursor` 表示结束。不能用此接口读取其他玩家的资金流水。
- 每条统一流水返回 `currency`、`decimals`、`amount`（展示金额字符串）、`available_after`（变更后可用余额字符串）、`frozen_after`（冻结余额字符串或 null）。无需前端做精度转换。
- `amount_minor` 是业务金额；实际余额变动以 `available_delta_minor` 和 `frozen_delta_minor` 为准。申请提现是可用减少、冻结增加；确认提现是可用不变、冻结减少；驳回是可用增加、冻结减少。不要把冻结和确认两条流水重复算为两次下分。
- 新记录包含冻结余额快照；无法可靠还原的历史记录返回 `frozen_after_minor=null`、`frozen_after=null`，不能当成 0。
- 旧积分和体力接口继续可用，底层已统一查询 `ledger_entries`；体力旧接口仅查询 `STAMINA`，其他体力币种请使用统一流水接口。

- 积分流水：`GET /v1/points/{accountID}/ledger?limit=50&offset=0`
- 体力流水：`GET /v1/stamina/{accountID}/ledger?limit=50&offset=0`
- USDT 充值记录：`GET /v1/deposits`
- USDT 提现记录：`GET /v1/withdrawals`
- 本人投注与结算记录：`GET /v1/bets?status=won&limit=50&offset=0`。下单响应、投注详情和本人列表统一返回 `game_type`、`game_name`、`round_sequence`、`game_room_id/code/name`、`play_mode`、投注选项及赔率快照。`balance_after_bet_minor` 是该笔投注扣款后的余额快照；`balance_after_settlement_minor` 是赢、输、结算退款或主动取消完成瞬间的余额快照，进行中的投注为空；取消投注响应额外返回 `balance_after_refund_minor`。
- 所有玩家公开投注：`GET /v1/bets/public-feed?player_type=all&game_type=hash_9&currency=USDT&status=accepted&limit=50&offset=0`。`player_type` 可传 `all`、`real`、`virtual`，默认同时返回真实和虚拟玩家；通过 `player.is_virtual` 标识玩家类型。玩家展示信息读取 `player.user_id`、`player.display_name` 和 `player.avatar_url`。公开列表包含相同的期号、房间和赔率字段，但绝不返回任何余额、登录账号、内部 UUID 或 IP。

赔率展示使用 `payout_rate`；精确计算或核对派奖时使用整数 `payout_multiplier / payout_divisor`，不要使用浮点数自行推导。房间配置之后发生变化，也不影响投注记录中的赔率快照。

流水按时间倒序返回，`amount_minor` 正数为入账、负数为出账；`business_type` 区分来源：`admin_credit`（管理员充值）、`bet_task_reward`（参与投注活动任务奖励）、`activity_consume`（活动消耗）。

## 活动任务与多转盘

活动任务按中国时区（UTC+8）的自然日累计。只有最终结算为 `won` 或 `lost` 的有效投注才增加进度；`cancelled` 和 `refunded` 投注完全不计入。玩家达到门槛后必须在当天主动领取，跨到第二天后进度从 0 重新计算，前一天未领取的奖励失效。

后台配置闭环：

1. `admin` 调用 `GET /v1/admin/tasks/bet-configs` 读取完整配置列表。
2. 编辑每个档位的以下字段：

- `accumulation_currency`：已登记的平台币种代码；只有实际使用该币种产生的投注才会累计。
- `threshold_minor`：该币种的累计投注门槛。
- `reward_currency`：已登记的平台币种代码，与累计投注币种独立配置。
- `reward_minor`：奖励数量。
- `enabled`：是否展示、累计并发放该档奖励。

3. 调用 `PUT /v1/admin/tasks/bet-configs` 提交 `{"items":[...]}`。这是完整列表替换：已有项必须沿用 GET 返回的 `id`；新增项的 `id` 传空字符串；提交不存在的非空 `id` 返回 400；未包含的旧项会被自动禁用。数组至少保留一项，如需全部停用，应把保留项的 `enabled` 全部设为 `false`。
4. 保存成功后接口返回数据库中的完整列表，后台应以该响应覆盖页面状态。相同 `accumulation_currency + threshold_minor` 不能重复，两个金额都必须是大于 0 的最小单位整数。币种统一转大写且必须先在币种目录登记，不做资产/体力白名单或累计/奖励绑定；错误搭配由运营负责。

```json
{
  "items": [
    {
      "id": "",
      "accumulation_currency": "USDT",
      "threshold_minor": 1000000,
      "reward_currency": "USDT_STAMINA",
      "reward_minor": 10,
      "enabled": true
    }
  ]
}
```

前台参与与到账闭环：

1. 进入活动页调用 `GET /v1/tasks/bet-progress`，展示每档的 `progress_minor / threshold_minor`、`completed` 和 `rewarded`。
2. 玩家按正常流程调用 `POST /v1/bets`。下单成功时暂不累计任务；网络重试必须复用同一 `client_request_id`，避免创建重复投注。
3. 等投注结算为 `won` 或 `lost` 后，服务端才按结算时所在的中国时区自然日，把 `stake_minor` 累加到相同 `accumulation_currency` 的进度。前端收到结算事件或查询到最终状态后，重新调用 `GET /v1/tasks/bet-progress`；当某档 `completed=true` 且 `rewarded=false` 时显示“领取”按钮。
4. 点击领取调用 `POST /v1/tasks/{taskID}/claim`，`taskID` 使用进度项的 `id`，不需要请求体。成功返回本次奖励币种、奖励金额、领取后的余额和领取时间。
5. 领取成功后重新调用 `GET /v1/tasks/bet-progress` 和 `GET /v1/wallets/{accountID}/all`，此档应变为 `rewarded=true`。领取记录、钱包入账和 `business_type=bet_task_reward` 流水在同一事务内完成，不会出现只标记领取但余额未到账的情况。

领取接口状态：未达到门槛或当天已经领取返回 409；任务不存在或已被后台禁用返回 404；成功返回 200。领取按钮提交期间必须禁用，收到 409 后应重新拉取进度，不要反复请求。

每种累计币种拥有独立的当日进度，同一币种的多个门槛共享进度。主动取消的 `cancelled` 投注和轮次取消、异常退款产生的 `refunded` 投注从未写入进度，因此不会出现先领取奖励再扣回的问题。中国时区零点后，查询和领取都只读取新一天的进度，旧日记录仅保留作审计，不能补领。运营在当天中途新增一个低于玩家现有进度的档位时，该档会直接显示 `completed=true, rewarded=false`，玩家可以在当天立即手动领取。

后台通过 `GET/PUT /v1/admin/spins` 管理多个转盘。每个转盘配置独立的 `code`、`cost_currency`、`cost_minor` 和 `prizes`；消耗币种和奖项奖励币种均接受已登记的平台币种代码，不做绑定。玩家先调用 `GET /v1/activities/spins` 获取启用转盘，再调用 `POST /v1/activities/spins/{spinID}/play`。扣除参与消耗、发放奖品、写入两侧流水和抽奖记录在同一事务内完成。新领取/抽奖记录保存当时的配置快照，不随运营后续修改而变化。

## 金额精度与前端表示

### 币种配置接口

1. 玩家调用 `GET /v1/currencies` 获取已启用币种；管理员调用 `GET /v1/admin/currencies` 获取全部币种。
2. 只有 `admin` 可用 `POST /v1/admin/currencies` 新增，例如 `{"code":"EVENT_TOKEN","name":"活动券","decimals":2,"category":"custom","enabled":true,"create_on_registration":false,"sort_order":90}`。类别可为 `token/points/stamina/custom`，不决定任务或转盘如何搭配。
3. `PUT /v1/admin/currencies/{code}` 必须提交 `name/enabled/create_on_registration/sort_order/version`。`version` 使用上次查询值；409 时重新查询，不能覆盖别人的修改。代码与精度创建后不可修改，也不提供删除接口。
4. 未登记币种不能用于钱包或配置。`create_on_registration` 只影响以后注册，不会批量改老用户余额；老用户首次收到该币种入账时按业务创建钱包。
5. `enabled=false` 会隐藏玩家币种目录、关闭注册初始化、后台新上分和新链上提现；不会取消已有投注、活动配置或到账义务。要停止已有活动，请另外关闭活动/转盘，不能把币种目录开关视为全平台紧急停机开关。
6. `GET /v1/wallets/{accountID}/all` 新增 `decimals`、`available`、`frozen`，可直接显示后端金额字符串；包括用户持有的禁用币种，不要因目录隐藏而丢弃已有资产。

代码中有明确的余额精度边界：内部钱包余额、投注、奖励、充值、提现和流水使用 PostgreSQL `BIGINT` / Go `int64` 的最小单位整数，不使用浮点余额。平台精度来自 `GET /v1/currencies`，不再使用网络资产精度解释钱包余额。`GET /v1/assets` 的 `decimals` 只描述对应链上单位；后端按平台精度转换充值，并对提现保存网络精度与平台精度两个快照。无法精确表示的金额被拒绝，不四舍五入；充值回调原始金额单独保存。禁用币种的已确认充值仍入账，避免已转入资产丢失。

当前币种显示精度如下：

| 币种 | 小数位 | 1 个显示单位对应的 `_minor` | 前端规则 |
|---|---:|---:|---|
| `USDT` | 6（平台目录） | `1000000` | 不随用户选择的链改变。 |
| `POINTS`（宝石） | 3 | `1000` | 固定显示最多3位小数；例如 `1500 minor = 1.5`。 |
| `JADE`（玉石） | 3 | `1000` | 固定显示最多3位小数；例如 `1 minor = 0.001`。 |
| `ORIGIN_STONE`（源石） | 3 | `1000` | 固定显示最多3位小数；例如 `1 minor = 0.001`。 |
| `STAMINA`（宝石体力） | 0 | `1` | 只显示整数。 |
| `USDT_STAMINA` | 0 | `1` | 只显示整数；它是体力，不沿用 USDT 的链上精度。 |
| `JADE_STAMINA` | 0 | `1` | 只显示整数。 |
| `ORIGIN_STONE_STAMINA` | 0 | `1` | 只显示整数。 |
| PQPA 返回的其他链上资产 | 读取对应资产的 `decimals` | `10^decimals` | 必须使用所选资产记录的精度。 |
| 自定义币种 | 创建时指定 0–18 位 | `10^decimals` | 必须先登记；没有未知币种默认 0 位的回退。 |

三个积分资产统一采用3位小数。后台上分接口由后端换算精度：给玩家上 `100` 宝石提交 `amount="100"`，后端入账 `100000 minor`；上 `1.5` 宝石提交 `amount="1.5"`，后端入账 `1500 minor`。四种体力为0位，提交 `amount="8"` 就入账 `8 minor`；`USDT_STAMINA` 虽然名称带有 USDT 前缀，仍是独立的整数型活动次数，不沿用 USDT 的链上精度。其他仍以 `_minor` 命名的写接口继续提交最小单位整数，接口文档会逐项明确，前端不要自行猜测。

这样设计是为了让钱包余额、不可变流水、投注扣款和 outbox 能在同一事务中精确相等，避免二进制浮点误差，并让幂等重试与并发校验得到完全相同的结果。数据库余额不得为负，单笔金额通常必须大于 0。赔率同样使用 `payout_multiplier / payout_divisor` 的定点整数；无法整除的派奖最小单位余数会被整数除法截去，不产生小数最小单位。

服务端理论范围是有符号 64 位整数（最大 `9223372036854775807`），但 JavaScript `number` 的安全整数上限只有 `9007199254740991`。当前 JSON 金额仍以数字返回，服务端也没有额外限制到 JavaScript 安全整数；前端不得用浮点数换算金额，业务配置应控制在安全整数范围内。若未来必须支持超过该范围的余额，需要把所有 `_minor` 字段统一升级为十进制字符串契约，不能只在前端收到响应后再转 `BigInt`，因为标准 `JSON.parse` 在转换前已经可能丢失精度。

## 链上充值

充值网络与币种由 PQPA 应用配置决定，前端不得硬编码 TRON 或其他网络：

1. 调用 `GET /v1/assets` 获取当前启用的 `chain_code`、`token_code`、精度和 `support_withdraw`。
2. 玩家选择资产后，调用 `GET /v1/deposit-addresses?chain_code=POLYGON&token_code=USDT` 查询既有地址。
3. 地址不存在时，调用 `POST /v1/deposit-addresses` 并传入相同的 `chain_code` 和 `token_code` 创建地址。
4. 切换网络或币种时必须清空之前展示的地址，避免跨链误充值。
5. 创建地址响应可能包含 `memo`；存在时必须与地址一起展示和复制。

## USDT 提现

提现网络同样来自 `GET /v1/assets`，只允许选择 `support_withdraw=true` 的资产。调用 `POST /v1/withdrawals` 时必须提交 `chain_code`、`currency`、目标地址、可选的 `destination_memo`、最小单位整数金额和客户端幂等键。服务端执行地址格式、单笔最小/最大金额和 UTC 每日累计限额检查；超出每日限额返回 409。后台审批通过后由 Worker 调用 PQPA 出金；最终状态由 PQPA Webhook 更新，回调丢失时由 Worker 主动对账补偿。

## 管理后台接口

后台监控和统计仅提供后端 JSON 契约，不依赖任何管理端前端项目：

- `GET /v1/admin/monitor/bets?user=&game_type=&limit=100` 返回当前接受中的投注及完整期号、房间、模式和赔率快照；`GET /v1/admin/monitor/rounds` 单独返回每个启用玩法当前轮次的 `bet_closes_at` / `result_at` 和 `server_time`。兼容接口 `GET /v1/admin/monitor` 仍返回两者。
- `GET /v1/admin/bets` 跨玩家查询投注并返回相同的期号、房间和赔率字段；`GET /v1/admin/ledger` 查询统一流水；`GET /v1/admin/refunds-clearances` 查询结算退款、主动取消退款和下分/清退明细，投注退款记录包含关联 `bet_id`、期号和房间。三个接口均支持时间、用户和分页筛选。
- `GET /v1/admin/dashboard?user=&from=&to=` 返回玩家统计及按币种全局统计；全局数据自动排除虚拟账户。
- `GET /v1/admin/users/{userID}/login-ips` 返回玩家用过的 IP，并在每个 IP 下嵌套该地址登录过的其他用户；也可用 `GET /v1/admin/login-ips/{ip}/users` 直接反查。
- `POST /v1/admin/virtual-accounts` 使用登录名和密码创建可登录的虚拟账户；`PUT /v1/admin/virtual-accounts/{userID}/automation` 保存挂机玩法、币种、单注和间隔配置。虚拟账户可投注并进入排行榜，但不计入看板全局充值、流水和余额统计，也禁止下分。

管理员（operator/admin 角色）可调用 `POST /v1/admin/credits` 为用户充值任意币种：

```json
{
  "user_id": 100009,
  "currency": "POINTS",
  "amount": "100",
  "remark": "活动补偿",
  "request_id": "admin-20260723-0001"
}
```

`request_id` 是幂等键，重复请求返回首次结果（`credited=false`），不会重复入账。

用户管理：

- `GET /v1/admin/users?status=active&q=用户名`：查询和筛选用户。
- `PUT /v1/admin/users/{userID}/status`：设置 `active`、`disabled` 或 `bet_banned`。

`disabled` 用户不能登录或继续下注；`bet_banned` 用户可以登录和查看资产，但不能创建新投注。

## 代理返佣

- `GET /v1/agents/me/commissions`：查询本人佣金明细。
- `GET /v1/agents/me/team-summary`：按币种查询直属团队有效投注和已付佣金。
- `GET /v1/admin/commissions?status=paid`：后台查询返佣订单。
- `POST /v1/admin/commissions/{commissionID}/reverse`：撤销返佣并冲正代理余额。
- `POST /v1/admin/agents/{agentID}/commissions`：人工补发积分或 USDT 佣金。

自动返佣仅计算一级直属代理，按已结算且未退款的有效投注额计算。积分投注返到积分钱包，USDT 投注返到 USDT 钱包。
人工补发请求必须提供唯一 `request_id`，重复请求不会重复入账。

## 当前限制

API 通过 `API_ALLOWED_ORIGINS` 配置玩家端和管理后台的跨域白名单。访问令牌默认有效期 15 分钟；客户端应使用可轮换刷新令牌续期，退出时撤销当前刷新令牌。

## 实时连接

完整的连接认证、消息结构、事件 payload、错误处理、断线恢复和 TypeScript 客户端见
[WebSocket v1 前端接口手册](realtime-api.md)。本节仅保留快速接入摘要。

浏览器通过子协议连接：`new WebSocket("ws://58.87.64.208/v1/ws", ["bearer." + accessToken])`。如果之后配置 HTTPS，请改为 `wss://`；并通过 `REALTIME_ALLOWED_ORIGINS` 限制前端来源。连接建立后服务端发送版本化握手：

```json
{"v":1,"type":"hello","topics":["game"],"occurred_at":"2026-07-26T12:00:00Z"}
```

连接默认订阅全部 `game.*` 事件。客户端可以取消全局游戏事件并仅订阅指定轮次：

```json
{"v":1,"type":"unsubscribe","topics":["game"],"request_id":"u-1"}
{"v":1,"type":"subscribe","topics":["round:f39ac19d-20a0-42d7-a876-87aa3618635e"],"request_id":"s-1"}
```

服务端以 `type=subscribed` 返回当前完整订阅列表。客户端也可以发送
`{"v":1,"type":"ping","request_id":"p-1"}`，服务端返回对应 `pong`。版本、消息类型或 topic 无效时返回 `type=error`。

客服和全局聊天室消息均通过 WebSocket 发送，不再提供 HTTP 发送接口。发送命令如下，`request_id` 是幂等键；网络重连后重发必须使用同一个值：

```json
{"v":1,"type":"chat.send","room_id":"客服房间 UUID","body":"我要上分","request_id":"chat-001"}
```

成功后当前连接会先收到确认：

```json
{"v":1,"type":"chat_message_sent","request_id":"chat-001","payload":{"message":{"id":"消息 UUID","room_id":"客服房间 UUID","sender":{"user_id":100009,"display_name":"玩家一号","avatar_url":"/v1/avatars/100009?v=上传ID"},"body":"我要上分","status":"visible","created_at":"..."},"created":true}}
```

重复发送同一个 `request_id` 时 `created` 为 `false`，且返回第一次创建的消息。校验失败、没有房间权限或房间不存在时，服务端返回同一 `request_id` 的 `type=error`。客服的新消息仍会以 `type=event`、`subject=chat.message.created` 定向推送；前端应按消息 `id` 去重，避免收到自己的确认和事件后重复显示。

事件统一为 `{"v":1,"type":"event","subject":"game.round.settled","payload":{...},"occurred_at":"..."}`。`game.*` 按 `game` 或 `round:{round_id}` 订阅投递；订阅 `chat` 可接收公共房间消息。客服消息、`wallet.*` 和 `chain.*` 无需客户端订阅，只发送给对应玩家。客户端消费过慢导致 128 条发送队列写满时，网关会主动断开连接，客户端应退避重连并重新建立订阅。

## 聊天与客服

- `POST /v1/chat/customer-service`：幂等获取或创建玩家自己的两间独立客服房间。响应中的 `deposit` 是上分（充值）客服，`withdrawal` 是下分（提现）客服；前端首次进入客服页调用一次并分别保存两个 `id`。
- `GET /v1/chat/rooms`：玩家查询全局聊天室和自己的两间客服房间；后台角色可查询全部客服房间。客服房间会返回 `service_type`：`deposit` 为上分客服，`withdrawal` 为下分客服。
- `GET /v1/chat/rooms/{roomID}/messages`：查询可访问房间的最近消息。
- 发送消息：使用 WebSocket `chat.send` 命令，不能再调用 HTTP `POST /v1/chat/rooms/{roomID}/messages`。

HTTP 历史消息、Socket 发送确认和 `chat.message.created` 使用相同的消息结构。前端通过 `message.sender.display_name` 显示名称，通过 `message.sender.avatar_url` 显示头像；头像为空时使用本地默认头像。`sender.user_id` 是公开数字 ID，前端不会收到内部 UUID。站内 `/v1/avatars/...` 头像地址可直接交给图片组件显示。系统消息可能没有 `sender`。

消息与 `chat.message.created` outbox 事件在同一个数据库事务中提交。客服房间只有所属玩家和后台角色可读写，公共消息通过 WebSocket 的 `chat` topic 广播。当前后台角色不会自动成为客服房间 Socket 事件的定向接收者，后台客服页面仍需通过 HTTP 对账；具体边界见 WebSocket v1 手册。

## 文件上传

1. 调用 `POST /v1/uploads/authorize`，提交 `content_type` 和 `size_bytes`。
2. 使用响应中的 `method=PUT`、`url` 和完整 `headers` 上传。当前本地存储返回
   `requires_auth=true` 和站内相对 URL，必须额外携带当前 Bearer Token；
   不得修改授权要求的 `Content-Type`。
3. 本地存储在 `PUT` 成功后自动确认；仍可调用
   `POST /v1/uploads/{uploadID}/confirm`，该操作幂等。未来切换 S3 时由此接口
   通过 HEAD 核对对象大小和类型。
4. `GET /v1/uploads/{uploadID}` 仅允许所有者查询状态；
   `GET /v1/uploads/{uploadID}/content` 仅允许所有者下载已确认的本地文件。

允许 JPEG、PNG、WebP 和 PDF，默认最大 10 MiB、签名有效期 10 分钟。前端不能把 `pending` 对象当成可用文件；授权过期或对象元数据不匹配时确认接口返回 409。
Worker 会把超时未确认记录批量标记为 `expired`。本地文件位于 Docker
`uploads-data` 持久卷，必须随数据库一起备份；切换对象存储后还应配置生命周期规则。

## 日榜与周榜

`GET /v1/leaderboards?period=today&currency=USDT&limit=50`。`period` 只能是 `today`、`yesterday`、`this_week`、`last_week`，周期均按中国时区（UTC+8）计算；同一排行币种内按已结算有效投注额降序，同额按最早有效投注时间和公开用户 ID 排序。响应含周期边界、进行中/冻结状态、玩家公开资料、有效流水和已实际发放的奖励。

Worker 默认每分钟刷新今天和本周；结束周期内没有 `accepted` 投注后冻结排名并自动发奖。虚拟账户同样参与排行和发奖，但不计入全局环境统计。后台用 `GET/PUT /v1/admin/leaderboard-reward-rules` 按 `daily|weekly + currency` 配置名次区间、奖励币种、奖励金额及开关；`PUT` 需提交当前 `version`，首次保存传 `0`。`GET /v1/admin/leaderboard-rewards` 查询实际发奖记录。

## 聊天室红包

- `POST /v1/chat/rooms/{roomID}/red-packets`：使用 `USDT` 或 `POINTS` 创建红包。请求包含 `client_request_id`、`currency`、`total_minor`、`packet_count` 和可选 `greeting`。
- `GET /v1/red-packets/{packetID}`：房间成员查询红包剩余份数和状态。
- `POST /v1/red-packets/{packetID}/claim`：领取红包；同一用户重复请求返回第一次领取记录，不会重复入账。

创建时总金额立即从发送者可用余额扣除并进入红包托管。金额必须至少等于份数，最多 100 份；发送者不能领取自己的红包。每次领取至少一个最小货币单位，最后一份获得全部剩余金额。默认 24 小时过期，Worker 将未领取余额退回发送者原币种钱包。创建、领取、退款均在同一事务中更新钱包、写不可变账本和 outbox 事件。

游戏开奖结果的数据源由后端玩法规则决定：K 线玩法使用 OKX `candle1m` WebSocket 实时数据并由 REST 补偿，TRON 哈希玩法使用官方 TronGrid FullNode HTTP API。前端不得自行计算或替代开奖结果。
