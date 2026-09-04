# 前端接口接入

## 后台话术（客服快捷回复）

参照 `block-beast-servers` 的 `GMPhraseService`：全局共享话术库，按配置权限管理；当前项目允许 `admin/operator` 读取和维护，`player` 无权访问。不额外验证一级/二级操作密码。不是公告、自动回复或机器人发言，不关联其他项目。

| 操作 | 接口 | 参数/返回 |
| --- | --- | --- |
| 查询 | GET /v1/admin/phrases | category、enabled、page、limit；返回 `{count,items}` |
| 新增 | POST /v1/admin/phrases | `{request_id,title,content,category,sort,enabled}`；返回话术对象 |
| 编辑 | PUT /v1/admin/phrases/{phraseID} | `{title,content,category,sort,enabled}`；返回话术对象 |
| 删除 | DELETE /v1/admin/phrases/{phraseID} | 无请求体；成功204，不存在或重复删除404 |
| 启停 | PUT /v1/admin/phrases/{phraseID}/enabled | `{enabled:true}` 或 `{enabled:false}`；返回话术对象 |
| 排序 | PUT /v1/admin/phrases/order | `{ids:[3,1]}`；返回排序后的全部 `{items}` |

所有请求携带后台 Bearer Token。话术对象字段：`id`（数字int64，自增，非用户ID）、`title/content/category`（字符串）、`sort`（非负整数int64）、`enabled`（布尔值）、`created_at/updated_at`（RFC3339时间字符串）。不使用参考项目的 `phraseID/createTimestamp` 字段或 `errCode` 返回格式，错误沿用本项目 `{error:"中文提示"}`。

标题/内容去首尾空白后必填，最长分别100/2000；分类可空、最长50。长度与参考项目JS一致，按UTF-16单位计数（普通中文1，emoji通常2）。文本按纯文本显示，不作为HTML插入。`sort` 默认0，`enabled` 默认false。编辑是完整替换，省略分类、排序或启用状态会重置为默认值。

查询 `page` 从0开始，默认0；`limit` 默认20，0按默认，其他整数裁剪到1–100。`category` 精确匹配，空值不筛选；`enabled` 只能为true/false，省略返回两种状态。`count` 是筛选后总数，`items` 是当前页，默认按sort升序、同sort按id降序。

排序是全局排序，包含所有分类和停用项：请求指定项置前，未指定项按原顺序追加，统一从0连续编号。`ids` 必填且不重复、不含不存在或已删除ID；`[]` 表示保留现有相对顺序并重编号。保存后重新查询当前页。

调用闭环：

1. `POST /v1/admin/auth/login` 登录，打开管理页时 `GET /v1/admin/phrases?page=0&limit=20`。
2. 新增时生成UUID `request_id`，例如提交 `{"request_id":"48b3f585-33ba-4381-a186-93fe5bcac592","title":"欢迎语","content":"您好，请问有什么可以帮您？","category":"客服","sort":0,"enabled":true}`。成功200；同一管理员同键同规范化参数重放返回当前话术，参数不同或原话术已删除返回409。请求重试复用键，不重复创建或写审计。
3. 编辑/启停/删除调用上表接口，成功后刷新列表。数据库保留内部删除标记以避免旧创建请求复活话术，不提供恢复接口。初始化没有默认话术。
4. 客服选用：`GET /v1/admin/phrases?enabled=true&category=客服`，点击条目将 `content` 填入聊天输入框，允许编辑。确认后通过已鉴权 Socket 发送 `{"v":1,"type":"chat.send","room_id":"当前会话UUID","body":"选中的话术正文","request_id":"本条消息的唯一编号"}`；消息重试复用该编号。发送者由连接身份确定，不传话术ID代替正文。完整握手、订阅及确认流程见 [Socket手册](./realtime-api.md)。历史消息通过 `GET /v1/chat/rooms/{roomID}/messages` 查询；当前没有对应的POST发送接口。停用/删除话术不修改已发送消息，也不会触发自动消息。

新增、编辑、启停、删除、排序分别记录 `phrase.create/update/enabled/delete/reorder` 审计，排序和业务变更与审计同事务。参数无效400、无权限403、不存在404、幂等冲突409、未登录401、服务不可用503。部署先执行 `0051_customer_phrases.sql`，再更新API。

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

### 请求参数类型约定（以 Go 实际解码类型为准）

用户 ID 在不同接口中的 JSON 类型并不完全相同，不要全局统一转为数字或字符串：

| 接口/位置 | 字段 | 必须使用的类型 |
| --- | --- | --- |
| `POST /v1/bets` 请求体 | `account_id` | number 整数，例如 `100009` |
| 管理员上分、统一上下分、`POST /v1/stamina/consume` 请求体 | `user_id` | string，例如 `"100009"` |
| `POST /v1/agents/bind` 请求体 | `parent_user_id` | string，例如 `"100009"`，不是 UUID |
| 积分清退/链上提现请求体（仅本地关闭鉴权时回退） | `account_id` | string；正常登录省略，由 Token 确定本人 |
| 用户/代理管理路径 | `{userID}`、`{agentID}` | 公开数字 ID，例如 `/100009/roles`，不是内部 UUID |
| 轮次、投注、提现单、上传文件路径 | 对应资源 ID | UUID 字符串，不要套用用户 ID 规则 |
| 金额 | `amount` | 展示金额 string，例如 `"1.5"`，目前用于管理员资金操作 |
| 金额 | `amount_minor`、`stake_minor`、`reward_minor` 等 | 最小单位整数，不能传小数字符串；不能将所有资金接口都改传 amount |

排行榜奖励配置使用 `version`（整数）与 `rules`（数组），每档 `rank_from/rank_to` 为整数、`reward_minor` 为最小单位整数、`enabled` 为 boolean。任务/转盘配置的金额也仍是最小单位整数。

注意两个保留现状的例外：审计日志查询 `actor_user_id` 目前按内部 UUID 筛选；平台配置响应 `updated_by` 目前仍为内部 UUID 字符串。其他经公开 ID 转换的流水响应 `operator_id` 是公开数字 ID。这里仅对齐文档，没有改变这些接口的实际行为。

### 后台直接上分、下分、赠分、人工扣分

统一调用 `POST /v1/admin/wallet-adjustments`。`admin` 和 `operator` 均可调用；使用后台全局一级密码，不是个人或目标玩家的二级密码。支持所有已登记启用币种。平台扣分不会触发链上付款。

调用顺序：

1. `POST /v1/admin/auth/login` 登录，后续携带后台 Token。
2. 查询 `GET /v1/admin/security-passwords` 的 `first_set`；未设置时由 `admin` 按下文设置全局一级密码。
3. `GET /v1/admin/users?q=用户名` 选择目标用户。列表的 `id` 是公开数字 ID，请转换为字符串作为写接口的 `user_id`。
4. `GET /v1/currencies` 选择币种；`GET /v1/wallets/{目标公开ID}/all` 展示可用、冻结余额。
5. 管理员确认用户、币种、操作和金额，输入后台全局一级密码；生成 `request_id`，提交下面的请求。不需要先调用密码验证接口，资金接口会自行验证。
6. 200 后刷新余额、`GET /v1/admin/ledger?user=100009&currency=POINTS`、看板；人工清退也可在 `GET /v1/admin/refunds-clearances` 查询。网络超时重试必须保持同一管理员、请求编号和业务参数，不能自动换编号。

| 字段 | JSON 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| user_id | string | 是 | 公开用户 ID，如 `"100009"`，不接受数字或内部 UUID |
| currency | string | 是 | 币种代码，后端去空白并转大写 |
| action | string | 是 | `credit` 上分、`debit` 下分/清退、`reward` 赠分、`penalty` 人工扣分 |
| amount | string | 是 | 正数展示金额；如 `"100.5"`，POINTS 内部为 100500；后端转换，不能传负数、科学计数法或超精度金额 |
| request_id | string | 是 | 1–128 字符，建议 UUID，同一管理员所有人工资金操作共用幂等空间 |
| first_password | string | 是 | 后台全局一级密码；每次提交，不存入本地持久化存储或日志 |
| remark | string | 否 | 备注，UTF-8 编码不超过 2000 字节 |

```json
{
  "user_id": "100009",
  "currency": "POINTS",
  "action": "debit",
  "amount": "100.5",
  "request_id": "admin-debit-001",
  "first_password": "后台全局一级密码",
  "remark": "人工清退"
}
```

返回：`operation_id`（UUID）、`user_id`（公开数字 ID）、`currency`、`action`、`amount_minor`（正数）、`delta_minor`（增减带符号）、`balance_before_minor`、`balance_after_minor`（操作前后可用余额）、`frozen_minor`（冻结余额）、`duplicate`、`occurred_at`。金额字段均为最小单位整数，时间为 RFC3339。重复请求 `duplicate=true`，返回首次操作快照，不代表当前余额。

- 下分/人工扣分仅扣可用余额，不动冻结，不取消投注。余额不足 409；虚拟账户禁止两种扣款，返回 403，允许上分/赠分。
- 同一请求编号修改用户、币种、操作、金额或备注返回 409；等值金额（如 "1" 与 "1.0"）视为相同。
- 未设置全局一级密码 409；密码错误 401；无管理员权限 403；参数/精度错误 400。
- 新流水类型：`admin_credit` 上分、`admin_debit` 清退、`admin_reward` 赠分、`admin_penalty` 人工扣分。业务编号 `business_id` 对应 `operation_id`。
- 看板 `global[].credit_minor` 为人工上分；总充值由链上 `deposit_minor` 加人工 `credit_minor` 展示。新增 `clearance_minor` 为人工下分+积分审核成功扣款+链上最终成功扣款（不含冻结、驳回和投注退款），`gift_minor` 为人工赠分，`penalty_minor` 为人工扣分，全部按币种，排除虚拟账户。人工扣分不计游戏输赢。
- `players[].funds[]` 按币种返回 `currency/credit_minor/clearance_minor/gift_minor/penalty_minor`；既有玩家汇总字段保留，不要用跨币种总数做资金核算。
- 金额、流水、成功审计、幂等结果和 outbox 同一事务，失败全部回滚；不在任何持久化记录中保存操作密码。

### 旧上分入口与提现的边界

`POST /v1/admin/credits` 保留，但现在允许 admin/operator 且必须提交 `first_password`；其他字段为上表去掉 `action`。等价于统一接口的 `action=credit`，两者共用幂等空间。返回原 `CreditResult` 字段，`credited=false` 表示重放。

发布时先执行所有待应用迁移（包含 `0049` 和 `0050_admin_security_passwords.sql`），再更新 API。旧上分入口同样需要改传 `first_password`；不再接受 `secondary_password`。

玩家积分清退申请、链上提现仍保留原申请→冻结→审核流程，与后台直接上下分分开。不要用后台 Token 代玩家调用申请接口，也不要对同一笔提款既做人工下分又批准提现，避免重复扣款。链上提现仅在服务商最终确认后计入清退，审核成功不代表到账。

### 积分下分/清退调用顺序

1. **玩家端，玩家 Token**：查询 `GET /v1/wallets/{本人公开ID}/all`，确认 `POINTS` 可用余额。
2. **玩家端**：`POST /v1/point-withdrawals` 提交申请。例如下分 `1.5` 宝石：

   ```json
   { "request_id": "point-withdraw-001", "amount_minor": 1500, "remark": "申请下分" }
   ```

   | 字段 | JSON 类型 | 必填 | 说明 |
   | --- | --- | --- | --- |
   | `request_id` | string | 是 | 幂等标识，网络重试复用。 |
   | `amount_minor` | number（整数，对应 Go int64） | 是 | 大于 0 的最小单位整数。POINTS 精度 3，下分 100 传 `100000`，下分 1.5 传 `1500`；这里尚未改为展示金额字符串。 |
   | `remark` | string | 否 | 申请备注；当前申请列表响应不返回此字段。 |
   | `account_id` | string | 否 | 正常登录时不要传，由 Token 确定本人；只供鉴权关闭的本地开发回退使用。 |

   不传 `currency`，这个接口固定操作 `POINTS`；不传 `amount` 或二级密码字段。当前没有在下分接口中自动验证二级密码的链路。
3. **服务端**：同一事务将金额从可用余额转入冻结余额，记录申请和流水。返回 201，申请字段为 `id`（UUID string）、`user_id`（公开数字 ID）、`client_request_id`（string，对应请求中的 `request_id`）、`amount_minor`（整数）、`status="requested"`、`created_at`（时间字符串）。重复申请返回原申请，不再冻结。
4. **后台端，后台 Token**：`GET /v1/admin/point-withdrawals?status=requested` 查询待审核申请。`status` 为可选 string，可取 `requested/approved/rejected`，不传返回全部状态；当前固定最多 50 条，未实现用户筛选和翻页，不能依赖 `user`、`limit`、`offset` 参数生效。
5. **后台端**：使用申请的 `id` 调用 `POST /v1/admin/point-withdrawals/{withdrawalID}/review`。路径参数为申请 UUID string，不是用户 ID。请求体二选一：

   ```json
   { "approved": true }
   ```

   ```json
   { "approved": false }
   ```

   `approved` 必须明确传 JSON boolean，不是 `"true"` 或 `1`。通过：扣除已冻结积分，状态变为 `approved`，不再次扣可用余额；驳回：解冻回到可用余额，状态变为 `rejected`。不需要重新传用户、金额、币种或审核人，审核接口不接收备注。200 响应为 `{"status":"processed"}`，这个值只是“审核已处理”，不是申请最终状态。
6. **审核后**：后台重新查申请列表、`GET /v1/wallets/{用户公开ID}/all` 和 `GET /v1/admin/ledger?user=100009&currency=POINTS`；玩家用 `GET /v1/point-withdrawals` 查看自己的申请。审核仅允许 `requested` 状态，重复审核返回 409，不会重复扣款；网络超时先查状态再决定是否重试。

积分下分流水的 `business_type` 分别为 `point_withdrawal`（冻结）、`point_withdrawal_debit`（通过并扣除冻结额）、`point_withdrawal_unfreeze`（驳回解冻）。不要把冻结与通过重复统计为两次下分。

### 链上资产下分/提现调用顺序

1. **玩家端，玩家 Token**：`GET /v1/assets` 查询链和资产，该接口已过滤停用资产，响应没有 `enabled` 字段；从列表选择 `support_withdraw=true` 的资产。`GET /v1/currencies` 读取启用的平台币种及精度，确认包含该资产的 `token_code`，再查询本人钱包余额。
2. **玩家端**：`POST /v1/withdrawals` 提交申请，请求参数如下：

   | 字段 | JSON 类型 | 必填 | 说明 |
   | --- | --- | --- | --- |
   | `client_request_id` | string | 是 | 幂等标识；注意与积分申请的 `request_id` 字段名不同。 |
   | `chain_code` | string | 是 | 所选资产返回的链代码，不自行硬编码。 |
   | `currency` | string | 是 | 使用所选资产的 `token_code`，例如 `USDT`。 |
   | `destination_address` | string | 是 | 收款地址，必须符合所选链的校验规则。 |
   | `destination_memo` | string | 按链要求 | 不需要时省略；需要备注/标签的网络按要求提供。 |
   | `amount_minor` | number（整数，对应 Go int64） | 是 | 平台最小单位，不是网络最小单位。USDT 平台精度 6，提现 100 USDT 传 `100000000`。不能传展示字符串 `amount`。 |
   | `account_id` | string | 否 | 正常鉴权时省略，仅本地关闭鉴权时回退使用。 |

   正确请求体形状（链和地址必须替换为实际选择值）：

   ```json
   {
     "client_request_id": "chain-withdraw-001",
     "chain_code": "所选链代码",
     "currency": "USDT",
     "destination_address": "所选链的有效收款地址",
     "amount_minor": 100000000
   }
   ```

3. **服务端**：检查余额、虚拟账号限制、链/资产支持、地址、最低/单笔/每日限额及精度；冻结金额后返回 201。响应主键为 `withdrawal_id`（UUID string），不是积分申请的 `id`；并包含 `client_request_id`、收款地址、链、币种、整数 `amount_minor`、string `status` 和时间 `created_at`。
4. **后台端，后台 Token**：`GET /v1/admin/withdrawals?status=requested` 查询待审核列表；`GET /v1/withdrawals/{withdrawalID}` 查详情。后台列表只实现可选 string `status` 过滤，固定最多 50 条，不支持用户筛选或翻页。当前链上提现 DTO 不返回所属用户 ID，不能假设列表有 `user_id`；需要按用户辅助核对时，可用 `GET /v1/admin/refunds-clearances?user=100009&status=requested&limit=50&offset=0`，筛出 `record_type="withdrawal"`，其 `id` 对应提现 UUID。
5. **后台通过**：`POST /v1/admin/withdrawals/{withdrawalID}/approve`，不需要请求体，200 返回提现对象，`status="approved"`。金额此时仍冻结，后续由 Worker 出金。
6. **后台驳回**：`POST /v1/admin/withdrawals/{withdrawalID}/reject`，建议传 `{"reason":"收款信息需要核实"}`；`reason` 为可选 string。200 返回提现对象，`status="cancelled"`，金额解冻退回原钱包。两种审批均只允许 `requested` 状态；重复审批返回 409。
7. **跟踪结果**：查询提现详情，正常路径为 `requested → approved → broadcasted → confirmed`；`confirmed` 才代表服务商确认成功并扣除冻结额。出金失败为 `failed` 并解冻；后台驳回为 `cancelled`。发生审批超时先查当前状态，不要再发一笔新申请，也不要人工另扣一次余额。

链上提现详情可能额外返回 `provider_order_id`、`tx_hash`、`failure_reason`（均为可选 string）。审核完刷新用户钱包、统一流水和退款/清退明细；后台不能用 `GET /v1/users/me/ledger` 查看玩家，该路径始终是当前 Token 本人的流水。

### 上下分共同约定与错误处理

- 参数类型以本节实际 Go 接口为准：上分金额是展示字符串；两种下分申请金额仍是整数 `amount_minor`，不能混用。前端构造下分整数时使用十进制定点处理，不用浮点乘法再取整；超过 JavaScript 安全整数范围时不能直接经 `Number` 序列化，也不能擅自把接口整数改成字符串。
- 通常 400 表示参数、金额/精度或资产不支持；401 表示登录无效；403 表示权限不足或虚拟账号禁止下分；404 表示用户/钱包/申请不存在；409 表示余额不足、每日限额或状态冲突；500/503 表示服务异常/暂不可用。以实际接口响应为准，可直接展示中文 `error`。
- 写请求超时不是失败的证明。上分/申请复用原幂等标识并核对结果；审核先重新查询状态，已经通过或驳回就停止重试。前端提交期间禁用按钮，防止连续点击生成多笔不同标识的操作。
- 冻结、扣款、解冻、写流水由后端事务完成，前端不再调用其他接口手动“补扣/补回”。钱包通知用于触发查询刷新，不用推送金额自行累加余额。

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


## 后台全局操作密码（与个人二级密码独立）

后台角色保持 `admin`（全部后台权限）和 `operator`（含上下分权限）；玩家角色 `player` 不受影响。不新增超级管理员角色或硬编码特权账号。

1. 登录后调用 `GET /v1/admin/security-passwords`，返回 `{"first_set":false,"second_set":false}`。
2. 未设置或需要重置时，仅 `admin` 调用 `PUT /v1/admin/security-passwords/first` 或 `/second`，提交 `{"login_password":"当前管理员登录密码","password":"新全局操作密码"}`，成功 204。不要求旧操作密码，没有默认密码。
3. 可选预校验：`POST /v1/admin/security-passwords/first/verify` 或 `/second/verify`，提交 `{"password":"待验证密码"}`，成功 204。不会签发凭证，也不能代替业务接口校验。
4. 资金操作每次提交 `first_password`；下表配置更新在原 JSON 请求体最外层追加 `second_password`，不得放进 items/value。一级和二级密码分别配置、独立验证。

| 接口 | 密码字段 | 允许角色 |
| --- | --- | --- |
| POST /v1/admin/wallet-adjustments、POST /v1/admin/credits | first_password | admin/operator |
| PUT /v1/admin/hash/config | second_password | admin/operator |
| PUT /v1/admin/leaderboard-reward-rules | second_password | admin/operator |
| PUT /v1/admin/configs/{key} | second_password | admin |
| PUT /v1/admin/tasks/bet-configs | second_password | admin |
| PUT /v1/admin/spins | second_password | admin |

任务配置示例：`{"second_password":"后台全局二级密码","items":[原有任务配置对象]}`。其他参数类型和业务限制不变；查询配置不返回密码。

操作密码必须为非空字符串，不能全空白，UTF-8 最多 128 字节；不限制必须数字。存储使用 Argon2id 哈希，审计仅记录修改人、级别和版本。密码不进入普通配置、响应、资金幂等记录或日志，前端不要持久化保存。

状态码：参数错误 400、密码错误 401、权限不足 403、未设置 409、验证锁定 429（Retry-After: 900）、服务不可用 503。按账号和用途 first/second/manage 分开计算，15 分钟内失败 5 次锁定 15 分钟，成功验证清零；一个账号不会锁死其他账号。重置密码不会提前解除已触发的验证锁定。

个人 `/v1/users/me/secondary-password` 的设置和验证流程完全不变，不用于上述后台操作。只修改本后端接口与文档，不关联或修改其他前台、后台项目。
