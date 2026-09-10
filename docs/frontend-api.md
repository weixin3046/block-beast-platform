# 前端接口接入

## 活动任务与转盘对齐（0056–0059）

所有管理写操作仍仅 admin，并验证后台全局二级密码 second_password；不放宽 operator 权限。金额传真实数量，响应为金额字符串，配置 ID 均由服务端生成。

### 多转盘、启停与删除

可以同时启用多个转盘，包括多个消耗同一币种的转盘。新增 POST /v1/admin/spins，单条修改 PUT /v1/admin/spins/{spinID}，不会关闭其他转盘。旧 PUT /v1/admin/spins 整批入口已停用，验证权限及二级密码后返回 410。

独立开关：PUT /v1/admin/spins/{spinID}/enabled，传 `{"second_password":"后台二级密码","enabled":false}`。独立删除：DELETE /v1/admin/spins/{spinID}，请求体传 `{"second_password":"后台二级密码"}`。成功返回 `{"success":true}`；删除为软删除，列表隐藏、禁止新抽奖，历史记录保留；重复删除成功，不存在返回404，已删除不能重新启用。

奖项新增 disabled 布尔值，默认 false。weight 允许为0，无需同时设置 disabled；零权重奖项可以显示但不会被抽中。禁用奖项不参与抽奖，也不计入有效总权重。奖励 amount 仍必须大于0，不用零金额模拟“谢谢参与”。启用转盘至少有一个未禁用且权重大于0的奖项；可以保存整体停用、权重全零或所有奖项禁用的配置。消费与奖励币种独立，多个奖项可奖励不同币种；不要求体力与币种强绑定。不限制每日抽奖次数。零权重可保存与参考对齐，但不复制参考随机边界误选零权重奖项的行为。

抽奖记录：

- 后台 GET /v1/admin/spin-records（admin）。
- 前台 GET /v1/activities/spin-records（已登录用户）。
- 查询参数：spin_id 可选UUID、currency可选奖励币种、user_id可选公开数字ID、limit 1–100默认50、offset默认0。
- 返回 `{items,total}`；每项含 id、spin_id、user_id、display_name、is_virtual、prize_id、prize_label、currency、amount、created_at。不返回其他用户余额或请求幂等键；只查询真实抽奖执行记录，不混入合成中奖记录，虚拟账户记录必须明确标识。

### 独立任务、多奖励和完成次数

新增或编辑任务示例（编辑路径带 taskID，请求不带 id/code）：

```json
{
  "second_password":"后台二级密码",
  "title":"每日宝石投注任务",
  "period_type":"daily",
  "sort_order":10,
  "max_complete_count":2,
  "accumulation_currency":"POINTS",
  "threshold":100,
  "enabled":true,
  "rewards":[
    {"currency":"STAMINA","amount":1.5},
    {"currency":"USDT","amount":0.1}
  ]
}
```

period_type 为 daily/global，默认 daily；max_complete_count 默认1，0表示不限制，正数为该周期最多领取次数，上限100000。global为长期任务，无每日清零；daily仍遵守中国时区当天手动领取、过期失效。sort_order 为整数，升序展示。奖励列表1–32项，各币种不能重复；每项 amount 独立按币种精度换算。新请求使用 rewards，不与旧 reward_currency/reward 混传。响应暂保留旧首项摘要以支持现有调用，新页面只以 rewards 为准。

每个任务独立累计。达到门槛即封顶，等待手动领取期间不再累计，多余投注不带入下一次；领取成功后次数加1，若未达上限则进度归零开始下一轮，达到上限则停止。当日新建任务不追溯继承同币种旧任务进度。已有进度的任务不能更换累计币种或周期，应停用旧任务再新建，避免历史进度被重新解释。

只有非模拟、非躲避、最终 won/lost 的投注累计。取消与退款不累计。日期按投注时间 placed_at 对应的中国时区日期，不按结算时间；23:59投注次日才结算时只写前一天进度，前一天奖励已过期，不能在次日补领。global任务不受这个日期过期限制。

GET /v1/tasks/bet-progress 返回 title、period_type、sort_order、max_complete_count、complete_count、rewards 及原进度字段。completed表示达到门槛，rewarded表示本周期已达到领取次数上限；completed=true且rewarded=false可领取。POST /v1/tasks/{taskID}/claim（沿用既有路径）领取后刷新进度和钱包，读取 rewards[].currency/amount/balance_after；多项奖励、领取次数及快照同事务提交。

独立启停：PUT /v1/admin/tasks/bet-configs/{taskID}/enabled，传 enabled 和 second_password；删除：DELETE /v1/admin/tasks/bet-configs/{taskID}，传 second_password。删除隐藏配置且不能再累计/领取，保留进度与历史奖励审计。旧整批 PUT /v1/admin/tasks/bet-configs 返回410。

后台 GET /v1/admin/tasks/progress 支持 task_id、user_id、date（YYYY-MM-DD）、limit、offset，返回 items/total；列表包含任务、用户、周期、累计币种、实际进度/门槛、已领取次数、上限及更新时间。长期任务 period_date 为内部固定值1970-01-01，展示为“长期”即可。该接口只读，不提供任意篡改进度或已领取次数的接口。

升级必须在停止旧 API/Worker 写入后执行0056、0057、0058、0059，启动同版本进程。0057保留旧领取记录并迁移已有每日进度，不重算历史资金；旧共享进度表只作历史留存，不再写入。

## 后台代理关系查询

`GET /v1/admin/users/{userID}/agent-relation`：admin/operator 查询指定用户的直属上级。路径传查询用户接口返回的公开数字 ID，不是邀请码，也不是前端生成的 ID。

```json
{"user_id":100009,"parent_user_id":100006}
```

未绑定上级返回 `parent_user_id: null`；用户不存在返回 404，ID 格式错误返回 400；未登录 401、普通玩家 403。此接口不返回整棵代理树或下级列表。玩家端 `GET /v1/agents/me` 仍只查询本人。
`PUT /v1/admin/users/{userID}/agent-level` 必须显式传入 `agent_level`：`0` 恢复普通用户，`1–6` 设置代理等级；缺失、null 或超出范围返回 400。恢复普通用户后邀请码不可用于新注册，新投注返水按非代理处理；保留上下级关系、已有佣金比例和历史投注快照，历史订单沿用原结算规则。

设置代理等级与绑定上级是两件事：设置 `agent-level` 不会自动创建上下级关系。

## 级差返水配置（0060–0061，本地新版结算已接入）

后台 admin/operator 调用 `GET /v1/admin/rebate-configs?game_type=hash_9&game_room_id=94000000-0000-4000-8000-000000000001&currency=POINTS`，三个筛选参数均可省略。响应为配置数组，含 id、game_type、game_room_id、game_room_name、currency、enabled、version、levels、updated_at。初始化144组，即六房间×六区块×四投注币种。

编辑使用 `PUT /v1/admin/rebate-configs/{configID}`，传 `version`（GET返回整数）、`enabled`（布尔）、`second_password`（后台二级密码）和完整 `levels` 数组。每项为 `{"level":1,"rate_per_mille":14}`，必须包含不重复的1–6级，比例随等级非递减且均为0–1000整数。14表示14‰即1.4%，前端不要转换金额精度。修改成功返回新版配置；版本冲突409需重新获取，不得盲目覆盖。

新版API接受哈希投注时保存返水配置和代理链快照，之后修改配置、代理等级不影响已接受投注。上下路/竞猜以本金为基数，躲避以正净赢为基数；每位上级的级差金额按币种最小单位向下取整。模拟、取消、退款单不发放。配置停用或缺失不回退旧佣金；历史版本1订单仍走旧机制，新版本2订单不会叠加旧佣金。

本地代码已接入实际结算，但不代表线上已生效：必须停旧API/Worker，依次执行0060、0061并更新同版本进程。已入账返水沿用 `business_type=commission`，前端显示“佣金/返水”，不要再重复计入派奖或赠分。旧 `/v1/admin/agents/{agentID}/commission-rate` 万分比配置只影响版本1历史单；新单应使用上述级差配置。

### 返水查询闭环

后台 `GET /v1/admin/rebate-records`；本人 `GET /v1/agents/me/rebates`。支持 `source_user_id`、`beneficiary_user_id`（公开数字ID）、`currency`、`status=paid/reversed`、`game_type`、`game_room_id`、`from/to`（RFC3339，起始包含、结束不包含）、`limit`（1–100，默认50）、`offset`。本人接口强制收款人为登录用户，额外传别人ID只会得到空列表，不能代查。

响应 `{items,total,summary}`：items含来源玩家、收款代理、投注ID、期号、玩法、房间、币种、返水基数base、快照等级agent_level、千分比级差differential_per_mille、实际金额amount、状态和时间；base/amount为实际数量字符串。summary按币种返回全筛选范围（不受分页影响）的source_bets去重投注数、paid_count/reversed_count明细数和paid_amount/reversed_amount金额。排除模拟投注、虚拟来源和虚拟收款用户。只展示新版级差返水，历史直属佣金仍查原佣金接口；不得把两份数据重复累计。

### 排行榜本人排名

`GET /v1/leaderboards?period=today&currency=POINTS&limit=50`新增 `self`，字段结构与items单条一致。即使本人不在前50名，也返回本人完整排名；未上榜返回null。today/yesterday/this_week/last_week均使用中国时区和对应币种；items和self使用同一数据库快照。身份取登录令牌，不支持传入self_user_id冒充他人。普通玩家看不到其他玩家余额，self可展示本人的余额；金额仅返回实际数量字符串。

### 个人投注结算推送

监听 Socket `type=event`、`subject=game.bet.settled`，每位玩家每个 round_id 只推送一条，按 account_id+round_id 去重。bets 包含本期正常结算的全部主单（含 won/lost），用 bet_id 更新订单；totals 按币种分别汇总 stake、payout、net_win、bet_count，不跨币种累加。金额为真实数量字符串，net_win=派奖−投入，不含返水。不同选项、房间和币种均包含在同一期这一条内；不同 round_id 分别推送。不要据此再次累加钱包余额；断线重连查询本人投注列表。完整字段、示例和必要部署顺序见 realtime-api.md。

### 单账号单登录会话（0067）

所有账号（含管理员、operator、真实和虚拟玩家）只保留最新一次成功登录的会话。再次登录会撤销旧访问令牌和刷新令牌；登录失败不踢出已有会话。同一令牌的多标签页可共用会话，不采集设备指纹，也不限制同一设备切换账号。

前端请求字段不变，不需要生成设备 ID。旧访问令牌请求返回 401；旧刷新令牌不能续期。Socket 握手和每条命令都会校验会话，空闲连接每秒检查一次，发现撤销后以 1008 和“登录已失效，请重新登录”关闭（数据库响应时间另计）。收到此关闭后不要无限使用旧令牌重连，应提示重新登录；正常 Token 到期可尝试刷新一次，失败则退出登录。刷新保留会话 ID，但 Socket 使用的访问令牌到期后仍须用新令牌重连。

部署须先执行 0067，再同时更新 API 与 Realtime；迁移会撤销全部旧刷新会话，无 sid 的旧访问令牌也不再接受，所有用户需要重新登录。Worker 无需修改；不要在旧版和新版 API 间长期混跑。已通过鉴权、正在执行的请求不会被追溯取消。

### 登录白名单（0063，风险检测未启用）

### 后台创建普通玩家与绑定上级

1. 登录后台取得令牌，准备后台全局二级密码。
2. 需要头像时按上传授权→上传文件→确认上传取得storage_key；不要直接传任意外部图片地址。
3. `POST /v1/admin/users`：`{"login_name":"player008","password":"至少12字符的登录密码","display_name":"昵称","avatar_url":"已确认的storage_key","second_password":"后台二级密码"}`。头像、昵称可省略；只创建真实player和默认零钱包，不接受roles、is_virtual、初始余额。重复登录名409，不会覆盖旧账号。返回后端数字user_id，玩家之后用普通登录接口登录。
4. `PUT /v1/admin/users/{userID}/agent-relation`：`{"parent_user_id":100006,"second_password":"后台二级密码"}`。ID为数字，不是UUID；仅无上级真实用户可绑定真实上级，已有上级409，自身/成环/虚拟关系400。绑定同时更新已有后代路径、记录审计。查询沿用 `GET /v1/admin/users/{userID}/agent-relation`；等级和推荐关系是两项设置，绑定不会自动升级代理。
5. 上分仍独立调用wallet-adjustments并使用一级密码，不在创建接口内赠送真钱。

### 后台单笔作废（0062）

`POST /v1/admin/bets/{betID}/void`，JSON：`{"request_id":"本次操作唯一编号","reason":"操作原因","second_password":"后台二级密码"}`。仅admin/operator；只作废accepted订单，封盘后可操作，结算已完成则409，不能更改历史输赢。请求编号由调用方为一次操作生成，重试必须原样复用，同一操作人重用编号但改变投注/原因返回409。

返回operation_id、bet_id、user_id、operator_user_id、game_type、round_sequence、currency、stake、refund、is_simulated、reason、status=voided、duplicate、created_at。stake/refund为实际数量字符串，真实单退本金一次，模拟单refund为零且不动钱包。投注查询新增voided状态筛选；前端显示“后台作废”。`GET /v1/admin/bet-voids?limit=50&offset=0`返回items/total。统一退款明细也包含record_type=bet_void，资金账本business_type=bet_void、entry_type=bet_void_refund显示“后台作废退款”，不是充值/赠分/清退。作废不累计任务、排行榜或返水；实时余额更新仍通过wallet.ledger.committed。

### 白名单管理说明

目前按确认不接入第三方IP/VPN/设备风险检测。`GET /v1/admin/login-whitelist`明确返回 `risk_check_enabled:false`、`whitelist_effective:false`，管理端应显示“未启用”，不能显示“已通过VPN检测”。白名单配置只保存预备规则，不改变现有登录判定；密码、失败锁定、账号禁用和前后台角色隔离始终生效。

admin/operator可查询；保存调用 `POST /v1/admin/login-whitelist`，传 `{"ip":"203.0.113.77","remark":"办公网络","second_password":"后台二级密码"}`，或使用数字user_id替代ip，两者只能选一个。IP接受单个IPv4/IPv6，不接受CIDR，IPv4映射地址自动归一化；备注最多200字，ID由后端生成，同目标保存更新备注。删除 `DELETE /v1/admin/login-whitelist/{entryID}`，JSON请求体传second_password；重复删除返回204。所有写入和审计同事务，密码不写审计。

## 后台创建虚拟账号与挂机接入流程

以下管理接口使用 `POST /v1/admin/auth/login` 获取的 admin/operator 令牌，不使用虚拟玩家的令牌。

1. `GET /v1/currencies` 获取可用币种；`GET /v1/admin/users?user_type=virtual&q=账号名` 查询已有虚拟账号。
2. `POST /v1/admin/virtual-accounts` 创建账号，示例：

```json
{
  "login_name":"virtual-demo-01",
  "display_name":"演示玩家",
  "avatar_url":"",
  "password":"请替换为至少12字符的密码",
  "initial_balances":{"POINTS":1000,"USDT":100}
}
```

账号 ID 由后端生成，保存响应的 `user_id`。初始余额传真实金额，不乘精度；不需要的币种省略，不传零。创建默认不开启挂机。账号有 player 角色，可使用 `POST /v1/auth/login` 正常登录；不需要先登录玩家账号才能保存挂机配置。创建接口没有请求幂等键，网络超时应先按登录名查询确认，避免盲目重试。

`display_name` 可省略或传空白，后端生成“用户+公开用户ID”；自定义昵称去除首尾空白后最多100字节。`avatar_url` 可省略或为空；指定头像时，使用同一个后台操作人的令牌调用 `POST /v1/uploads/authorize` → 按返回的上传地址、方法和请求头上传图片 → `POST /v1/uploads/{upload_id}/confirm`，把已确认记录的 `storage_key` 传给创建接口。仅支持 JPEG/PNG/WebP，不接受外链、未确认图片或其他操作人的上传。不需要转交上传记录所有权，也不需要登录机器人上传。

创建响应只有账号信息，例如：

```json
{"user_id":100009,"login_name":"virtual-demo-01","display_name":"演示玩家","avatar_url":"","status":"active","is_virtual":true}
```

有头像时响应 `avatar_url` 是可直接展示的 `/v1/avatars/...` 地址；创建不再返回 `automation_enabled/interval_seconds/stake/currency/run_status` 等旧配置字段。是否挂机和运行结果以以下 robot-plans 接口为准。

3. 创建前查询房间和币种配置。`POST /v1/admin/robot-plans` 创建一条计划，一个账号可以创建多条：

```json
{
  "request_id":"robot-plan-create-001",
  "user_id":100009,
  "enabled":true,
  "game_type":"hash_9",
  "game_room_id":"94000000-0000-4000-8000-000000000001",
  "currency":"POINTS",
  "min_stake":1.5,
  "max_stake":100,
  "selections":[{"play_mode":"road","pick":"odd"},{"play_mode":"road","pick":"small"}],
  "skip_min":1,
  "skip_max":3
}
```

计划 `id` 由后端生成；`request_id` 是前端为一次创建生成并在重试时保持不变的幂等键，不是业务 ID。相同键不同内容返回 409。`user_id` 是公开数字用户 ID。`enabled` 为布尔值。`min_stake/max_stake` 为正的实际金额，数字或十进制字符串，响应仅返回金额字符串；必须符合该房间、币种和所有候选玩法的限额。`game_type` 为 hash_9/13/17/19/23/29 之一。`selections` 为不重复的候选组合：road 支持 odd/even/big/small；guess/dodge 支持字符串 0–9。`skip_min/skip_max` 为 1–10000 的整数，表示间隔多少期，不是秒数，最小值不能大于最大值。

4. `GET /v1/admin/robot-plans?q=100009&limit=50&offset=0` 查询计划和运行状态；q 支持公开用户 ID、账号名、昵称。返回 items/total，limit 为 1–100。`GET /v1/admin/robot-plans/{planID}` 查询单条。完整编辑调用 `PUT /v1/admin/robot-plans/{planID}`，提交上面除 request_id 外的全部字段，不允许改所属用户。开关调用 `PUT /v1/admin/robot-plans/{planID}/enabled`，只传 `{"enabled":false}` 或 true；删除调用 DELETE 同一路径（不带 /enabled）。admin/operator 均可管理，普通玩家返回 403，未登录 401。
5. Worker 按轮次序号调度：首次启用先随机计算未来期号，不立即下单；到期随机选择一组玩法和区间内金额，每计划每期最多一单。错过期号重新安排未来期号，不补历史单。关闭、删除或账号非 active 时不执行。编辑配置会重置排期。查看 `next_round_sequence`、`last_seen_sequence`、`last_checked_at`、`last_status`、`last_error`、`last_bet_id`；状态包括 ready/stopped/scheduled/waiting_round/placed/failed。保存成功不是投注成功，以 last_bet_id 及后台投注记录为准。同一用户同一期只能使用一个赔率房间，多计划选择冲突房间会失败；多计划合计仍受单期投注限额约束。
6. 新建虚拟账户无需初始余额（可省略 initial_balances）；Worker 按需创建零余额钱包。新虚拟投注为模拟订单：不扣款，中奖、取消、退款也不增加钱包余额；仍保存输赢和模拟派奖金额，不产生投注资金流水、佣金或活动累计。既有真实扣款订单继续按原规则结算，不根据账号当前类型篡改历史资金。
7. 虚拟玩家仍参与混合排行榜并占名次及奖励位置；排行榜奖励与模拟投注派奖是不同业务。前台必须通过 is_virtual 明确标识虚拟玩家、说明混合排名及奖励规则，不得将模拟投注展示为真实用户资金流入。真实经营看板、代理团队人数和流水排除虚拟用户，虚拟账户禁止下分，也禁止发送或领取红包（403），避免资金流向真实用户。

旧 `PUT /v1/admin/virtual-accounts/{userID}/automation` 已停用，返回 410；不会自动把旧的固定秒数配置转换为新计划。升级需要执行 0054、0055 迁移，并更新 API 和 Worker 后按上述步骤创建计划。

## 配置 ID 的当前规则

任务、转盘及奖项的 ID 均由后端生成。单条新增请求体不传 `id/code/items`；单条编辑把 GET 返回的 ID 放到路径中，不在请求体重复传。转盘 `code` 仅为后端维护的只读内部编码，前端不填写。

| 操作 | 任务 | 转盘 |
| --- | --- | --- |
| 查询列表 | GET /v1/admin/tasks/bet-configs | GET /v1/admin/spins |
| 新增单条 | POST /v1/admin/tasks/bet-configs | POST /v1/admin/spins |
| 编辑单条 | PUT /v1/admin/tasks/bet-configs/{taskID} | PUT /v1/admin/spins/{spinID} |

写接口仅 admin，均传 `second_password`（后台全局二级密码）。新增返回 201 和单条配置，编辑返回 200 和单条配置，编辑目标不存在返回 404。停用通过编辑设置 `enabled:false`，其他业务参数传齐。保存单条不会禁用其他任务/转盘。

新增任务请求示例：

```json
{"second_password":"后台二级密码","accumulation_currency":"POINTS","threshold":100,"reward_currency":"USDT","reward":1.5,"enabled":true}
```

新增转盘请求示例：

```json
{"second_password":"后台二级密码","title":"活动转盘","cost_currency":"POINTS","cost":1.5,"enabled":true,"sort_order":0,"prizes":[{"label":"奖励","currency":"USDT","amount":2,"weight":1}]}
```

编辑转盘时，`prizes` 仍是**该转盘的完整奖项列表**：保留/修改的奖项带 GET 返回的 `prizes[].id`，新增奖项不带 ID，漏传的旧奖项会移除；不能引用其他转盘的奖项。现在 `prizes[].id` 是真实 UUID，不再是旧的奖项编码，新抽奖结果的 `prize_id` 与它一致。历史抽奖记录保留原标识与奖励快照，历史展示使用 `prize_label` 等快照字段，不依赖当前奖项列表。

旧集合 PUT 整批接口已停用，验证权限和二级密码后返回410；请使用单条接口。新增 POST 没有新增请求幂等键，网络超时须先重新查询确认，不能盲目自动重试。已有投注/抽奖等接口的 `request_id`/`client_request_id` 仍由调用端生成，同一次操作重试复用，不是资源主键。

## 金额统一协议

所有前端金额接口遵循[实际金额接口协议](./amount-contract.md)：请求传100或1.5，响应只返回实际金额字符串，不再返回最小单位字段。前后端需要同步升级。

## 用户管理、统计及金额口径补充（0052）

- 重置他人登录密码：`PUT /v1/admin/users/{userID}/password`，`{"new_password":"新密码","second_password":"后台全局二级操作密码"}`。仅admin；取消12字符最低长度和128字节最高长度限制，密码不能为空或全空白，仍受HTTP请求体大小保护。前端不要额外设置长度限制。
- 重置个人交易密码：`PUT /v1/admin/users/{userID}/secondary-password`，同上字段，不限制密码长度但不可为空或全空白；交易密码即用户个人二级密码，不是后台全局操作密码。仅admin。本次仅放开后台重置两个接口，不改变注册、自助改密和后台全局操作密码的规则；短密码安全性较低，建议仍使用长且不重复的密码。
- 两种重置均撤销目标会话，不保存明文、不返回密码；0067 起已绑定会话的访问令牌也失效，Socket 会检测并关闭。后台自己改登录密码仍可走原个人改密接口。
- 禁言：`PUT /v1/admin/users/{userID}/mute`，`{"muted":true}`；解除传false。admin/operator可操作，operator不能禁言后台账号。全局聊天禁言独立于账号status，Socket发消息时检查，返回“账号已被禁言”；仍可登录、投注、读历史消息。
- 用户搜索：`GET /v1/admin/users?q=100006&currency=USDT,JADE&user_type=real&available_min=1.5&available_max=10000&limit=50&offset=0`。`user_type`可选real/virtual，省略全部；q支持公开用户ID、登录名、昵称。币种可逗号分隔或重复传currency，任一所选钱包满足余额范围即匹配用户，余额筛选为展示单位、不得跨币种相加。返回数组，每个用户新增`is_virtual/chat_muted/balances`，balances含currency、decimals、available、frozen、available、frozen。未传币种返回全部钱包。
- 代理等级：读取用户列表的`agent_level`，不要根据`roles`是否存在agent角色判断。1–6表示代理，0表示非代理；设置等级不等于创建上下级关系。修复列表此前漏查代理等级的问题，调整等级不再清空已有佣金比例。

### 看板 /v1/admin/dashboard 每个字段的含义

`server_time`为生成响应时服务端时间。`players`为筛选后的真实用户，含`user_id`公开ID、`login_name`账号、`display_name`昵称、`bet_count`统计期间全部投注单数、`funds`按币种统计数组。`global`为全部真实用户按币种汇总，不受user筛选或玩家limit影响。

`global[]`和`players[].funds[]`结构一致：

| 字段 | 含义 |
| --- | --- |
| currency / decimals | 平台币种及小数精度 |
| bet_count | 时间范围内创建的投注单数，含取消、退款及待结算；不是有效投注单数 |
| stake | 上述投注本金总额（含取消和退款），实际金额字符串 |
| payout | 上述投注当前已记录的游戏派奖总额，含本金，不是净盈利 |
| deposit | 时间范围内链上充值入账，不含后台上分 |
| credit | 时间范围内后台人工上分 |
| clearance | 人工下分、积分提现审核扣款、链上提现最终成功扣款；不含冻结、拒绝和投注退款 |
| gift | 后台人工赠分，不代表全部任务/转盘/排行榜奖励 |
| penalty | 后台人工扣分，返回正数；不是投注输款 |
| balance | 当前可用余额加冻结余额，不受from/to影响，不是历史期末余额 |

from/to使用RFC3339，范围左闭右开，默认最近24小时。投注按下单时间归属，资金按流水发生时间归属。所有统计排除虚拟账户。各币种金额独立；不要把不同币种金额直接相加，也不要把payout当net_win。此次删除`players`顶层的stake/payout/deposit/credit/balance（此前错误混合币种）；改读funds同名字段。没有资金操作但存在钱包时也返回该币种零统计。

### 排行榜新增字段及时间

`GET /v1/leaderboards?period=today&currency=USDT`的周期字段原本在根对象：`period`今天/昨天/本周/上周，`period_type`日/周，`starts_at/ends_at`中国时区周期对应的时间边界，`refreshed_at`快照刷新时间。新增根`decimals`；items新增`total_bet`（有效投注展示金额）、`total_payout`（有效投注含本金派奖）、`net_win`（派奖减有效投注）、`first_bet_at`（周期内首笔有效投注时间）、`available`（刷新时可用余额快照）。余额仅向本人或后台返回，不公开其他玩家余额。历史冻结榜单没有保存的派奖/余额字段省略，不使用0或当前余额冒充历史数据。周期排名仍按effective_stake降序，不改成净赢排序。

### 投注与转盘的金额单位

投注1000 USDT提交 `stake:1000`，赔率1.985中奖返回 `payout:"1985.000000"`。派奖包含本金；后端精确计算和转换，前端不再乘除精度。

转盘 `cost` 与奖项 `amount` 都是实际币种数量。消耗和奖励按各自币种独立转换。`cost_balance` 为扣费后中间快照，最终消耗币种余额读取 `cost_balance_after_spin`，奖励余额读取 `reward_balance`。

### 转盘权重和排行榜密码报错

每个转盘奖项 weight 为非负整数，0代表不中奖但可以保留展示；相对概率=该奖项weight/全部未禁用奖项weight总和，不要求总和100。请求应明确提交 weight，负数、小数或概率百分数字符串不正确。启用转盘时有效总权重必须大于0；disabled=true 的奖项不参与计算。

排行榜保存使用`PUT /v1/admin/leaderboard-reward-rules`，示例：`{"second_password":"后台全局二级操作密码","period_type":"daily","currency":"USDT","version":0,"rules":[{"rank_from":1,"rank_to":1,"reward_currency":"USDT","reward":1,"enabled":true}]}`。version取GET最新版本，不总是0。second_password放最外层，不是个人交易密码、first_password或password；先由admin在全局密码管理中设置second。缺字段/空值/类型错误400，未设置409，错误密码401，锁定429。此轮把缺字段及空值改成明确中文提示。

发布必须先执行0052迁移；不修改历史钱包余额，不自动重新派奖。

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

## 注册即创建客服房间（0066）

真实玩家注册或后台创建真实玩家时，在同一事务内创建上分deposit、下分withdrawal两个客服房间并添加玩家owner成员。失败时整个账号创建事务回滚。虚拟账号不自动建房；历史真实player用户由0066补建缺失房间，保留原房间ID、成员及消息。后台空房间不代表存在待处理请求，应根据消息/未读状态区分。

前台仍调用 `POST /v1/chat/customer-service` 获取两个房间ID；接口保留获取或创建的幂等兜底能力，不需要传创建标记。注册响应结构不变。部署先执行0066迁移，再启动新API；未执行迁移时新版创建账号会失败。

## 默认哈希上限修复（0065）

0065修复0041整数积分初始化值在0044三位精度下被解释为千分之一的问题。仅六个固定房间的POINTS、JADE、ORIGIN_STONE三个上限完整匹配旧默认值时，将其最小单位值乘1000。1.94房间宝石/源石实际上限恢复为竞猜500、躲避1000、上下路1000；玉石恢复为50、100、50。USDT不变。最低投注、赔率、钱包余额、账本及历史投注均不变。

任一上限已修改的配置整行跳过，避免将运营定制值误放大；已修正记录重复执行不再放大。若运营恰好手动设成完整旧默认三元组，数据库无法区分其来源，部署前应核对并记录这类有意设置。配置发生修复时版本递增，后台须重新读取。需要部署并执行0065后数据库配置才会生效，前端不可自行补乘1000。

## 金额响应格式与登录 IP 查询修复

HTTP和Socket金额响应统一去掉小数末尾多余的0，仍返回字符串：`available:"2223.000000"` 改为 `available:"2223"`，`"1.500"` 改为 `"1.5"`，`"0.000000"` 改为 `"0"`。不会丢弃有效小数，数据库精度与请求精度限制不变，赔率、昵称、ID及投注selection不受此格式化影响。本文旧示例中的补零金额按本规则展示。

`GET /v1/admin/users/{userID}/login-ips` 返回纯IPv4/IPv6地址，不带 `/32` 或 `/128`。每个IP下的users列出同地址登录过的账号；查询同IP用户时复用纯地址。未登录过返回空数组。本次修复不需要数据库迁移。

## 哈希投注合单（0064）

同一玩家、同一期、同币种、同赔率房间、同玩法和同选项的 `accepted` 投注，在下单事务中合为一张订单。例如连续三次提交 `stake:10`、不同 `client_request_id`，返回同一个 `bet_id`，`stake` 依次为 `"10.000"`、`"20.000"`、`"30.000"`（宝石），`placement_count` 依次为 1、2、3。不同选项（例如单和小）或不同币种不合并，仍受现有同一期不能跨赔率房间规则限制。

- 每次请求的 `stake` 都是本次追加的实际金额，不是希望订单达到的总额；前端不转换精度。累计限额按玩家、期号、币种、房间、玩法、选项校验。
- 本人列表、公开列表、后台投注列表及监控直接返回合单结果，无需前端再次合计。本人/公开/后台投注列表的 `placement_count` 是成功下单次数，`last_placed_at` 是最后追加时间。`placed_at`（后台为 `created_at`）保留首单时间，日任务和榜单日期仍按首单时间归属。
- 与参考项目删除旧单并生成新 ID 的存储方式不同，本项目保留稳定 `bet_id`、首单赔率及返水快照，追加不追溯改价或改上级。当前配置仍决定是否允许新追加及累计限额。每次追加有独立请求记录和扣款流水，流水 `business_type=bet`、`business_id=bet_id` 不变。
- `POST /v1/bets` 返回本次 `client_request_id` 对应订单的**当前合计状态**；详情/列表的 `client_request_id` 是首单请求编号。重试任何一次已成功请求不会再次追加，即使订单已取消或结算；同编号改变原请求参数返回 409。
- `balance_after_bet` 是最后一次追加扣款后的余额快照，不是第一次的余额，也不是当前实时余额；逐次扣款余额查看资金流水。虚拟投注不扣余额、不产生投注资金流水。
- 玩家取消、后台作废、整期退款均处理整张合单，退还全部已扣本金，不能只取消其中一次追加。取消后新的请求创建新单；旧请求重试仍返回旧单。
- 结算对合计本金计算一次派奖并按最小单位向下取整，返水也按整单计算一次；任务、榜单与看板不得再次按 `placement_count` 乘本金。虚拟合单参与混合榜单但不进入真实资金统计及活动累计。
- Socket `game.bet.placed` 的 `bet.stake` 是最新合计额；按 `bet_id` 更新已有行，按 `placement_count` 丢弃乱序旧版本。新增 `placement_id` 标识本次追加、`added_stake` 表示本次金额，可用于展示“刚投注10分”，不得用合计额重复累加。钱包仍以钱包事件或接口为准。

仅0064上线后新建的哈希订单启用合单；历史订单及账本不改写，旧待结算单仍独立处理。部署需停止旧写入，迁移后启动同版本 API/Worker/Realtime，不能与旧写入程序混用。非哈希通用玩法不合单。

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
配置接口倍率直接使用 `guess_rate`、`dodge_rate`、`road_rate`，响应例如 `"9.35"`、`"1.08"`、`"1.94"`，不再返回配置倍率的multiplier/divisor。保存同样传实际倍率，支持数字或十进制字符串，大于0、最多18位小数且约分后分子分母不得超出int64；旧配置倍率字段返回400。后端保留整数分数计算，前端无需相除或乘1000。
投注上下限字段同样使用实际币种金额：0.1 USDT传 `min_stake:0.1`，50 USDT传 `guess_max_stake:50`。配置示例：`{"currency":"POINTS","guess_rate":"9.35","dodge_rate":"1.08","road_rate":"1.94","guess_max_stake":"500","dodge_max_stake":"1000","road_max_stake":"1000","min_stake":"0.001"}`。此例仅说明保存格式，实际GET值来自数据库，本次格式调整不自动重置已有金额上限或最低投注。
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
    stake: 2500,
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
- `amount` 是业务金额；实际余额变动以 `available_delta` 和 `frozen_delta` 为准。申请提现是可用减少、冻结增加；确认提现是可用不变、冻结减少；驳回是可用增加、冻结减少。不要把冻结和确认两条流水重复算为两次下分。
- 新记录包含冻结余额快照；无法可靠还原的历史记录返回 `frozen_after=null`、`frozen_after=null`，不能当成 0。
- 旧积分和体力接口继续可用，底层已统一查询 `ledger_entries`；体力旧接口仅查询 `STAMINA`，其他体力币种请使用统一流水接口。

- 积分流水：`GET /v1/points/{accountID}/ledger?limit=50&offset=0`
- 体力流水：`GET /v1/stamina/{accountID}/ledger?limit=50&offset=0`
- USDT 充值记录：`GET /v1/deposits`
- USDT 提现记录：`GET /v1/withdrawals`
- 本人投注与结算记录：`GET /v1/bets?status=won&limit=50&offset=0`。下单响应、投注详情和本人列表统一返回 `game_type`、`game_name`、`round_sequence`、`game_room_id/code/name`、`play_mode`、投注选项及赔率快照。`balance_after_bet` 是该笔投注扣款后的余额快照；`balance_after_settlement` 是赢、输、结算退款或主动取消完成瞬间的余额快照，进行中的投注为空；取消投注响应额外返回 `balance_after_refund`。
- 所有玩家公开投注：`GET /v1/bets/public-feed?player_type=all&game_type=hash_9&currency=USDT&status=accepted&limit=50&offset=0`。`player_type` 可传 `all`、`real`、`virtual`，默认同时返回真实和虚拟玩家；通过 `player.is_virtual` 标识玩家类型。玩家展示信息读取 `player.user_id`、`player.display_name` 和 `player.avatar_url`。公开列表包含相同的期号、房间和赔率字段，但绝不返回任何余额、登录账号、内部 UUID 或 IP。

赔率展示使用 `payout_rate`；精确计算或核对派奖时使用整数 `payout_multiplier / payout_divisor`，不要使用浮点数自行推导。房间配置之后发生变化，也不影响投注记录中的赔率快照。

流水按时间倒序返回，`amount` 正数为入账、负数为出账；`business_type` 区分来源：`admin_credit`（管理员充值）、`bet_task_reward`（参与投注活动任务奖励）、`activity_consume`（活动消耗）。

## 活动任务与多转盘

每日任务按投注时间所在的中国时区（UTC+8）自然日累计；长期任务不清零。只有最终结算为 won/lost 且非躲避、非模拟的投注增加进度，cancelled/refunded完全不计入。每日任务必须当天主动领取，未领取次日失效。新增字段及多奖励流程以本文开头0056–0059章节为准。

后台配置闭环：

1. `admin` 调用 `GET /v1/admin/tasks/bet-configs` 读取完整配置列表。
2. 编辑每个档位的以下字段：

- `accumulation_currency`：已登记的平台币种代码；只有实际使用该币种产生的投注才会累计。
- `threshold`：该币种的累计投注门槛。
- `reward_currency`：已登记的平台币种代码，与累计投注币种独立配置。
- `reward`：奖励数量。
- `enabled`：是否展示、累计并发放该档奖励。

3. 新增调用 `POST /v1/admin/tasks/bet-configs`，编辑调用 `PUT /v1/admin/tasks/bet-configs/{taskID}`，请求体直接传单条业务字段及 `second_password`，不要包裹 items，不传 id/code。
4. 保存成功后返回单条配置，后台更新对应行或重新 GET 列表。允许相同累计币种与门槛的不同独立任务，金额必须大于0。币种统一转大写且必须先在币种目录登记，不做资产/体力白名单或累计/奖励绑定；错误搭配由运营负责。

```json
{
      "second_password": "后台二级密码",
      "accumulation_currency": "USDT",
      "threshold": 1,
      "reward_currency": "USDT_STAMINA",
      "reward": 10,
      "enabled": true
}
```

前台参与与到账闭环：

1. 进入活动页调用 `GET /v1/tasks/bet-progress`，展示每档的 `progress / threshold`、`completed` 和 `rewarded`。
2. 玩家按正常流程调用 `POST /v1/bets`。下单成功时暂不累计任务；网络重试必须复用同一 `client_request_id`，避免创建重复投注。
3. 等非躲避、非模拟投注结算为 won/lost 后，服务端按投注时间所在中国时区日期累加对应任务独立进度。收到结算事件后重新调用 GET /v1/tasks/bet-progress；completed=true且rewarded=false显示领取按钮。
4. 点击领取调用 `POST /v1/tasks/{taskID}/claim`，`taskID` 使用进度项的 `id`，不需要请求体。成功返回本次奖励币种、奖励金额、领取后的余额和领取时间。
5. 领取成功后刷新任务进度和钱包；次数达到上限时rewarded=true，否则进度归零开始下一次累计。领取记录、所有奖励钱包入账和business_type=bet_task_reward流水在同一事务内完成。

领取接口状态：未达到本轮门槛或本周期已达到领取上限返回409；任务不存在、删除或禁用返回404；成功返回200。领取按钮提交期间必须禁用，收到409后刷新进度，不要反复请求。

每个任务独立进度，同一币种的多个任务不共享进度记录，新任务不继承旧流水。cancelled/refunded不累计。每日任务零点后只查询、领取新一天进度，旧日只保留审计，不能补领；长期任务跨日保留进度。

后台通过 `GET /v1/admin/spins` 查询、`POST /v1/admin/spins` 新增、`PUT /v1/admin/spins/{spinID}` 编辑单个转盘。每个转盘配置独立的 `title`、`cost_currency`、`cost` 和 `prizes`，不需要填写 ID 或 code；消耗币种和奖项奖励币种均接受已登记的平台币种代码，不做绑定。玩家先调用 `GET /v1/activities/spins` 获取启用转盘，再调用 `POST /v1/activities/spins/{spinID}/play`。扣除参与消耗、发放奖品、写入两侧流水和抽奖记录在同一事务内完成。新领取/抽奖记录保存当时的配置快照，不随运营后续修改而变化。

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

| 币种 | 平台小数位 |
| --- | --- |
| USDT | 6 |
| POINTS、JADE、ORIGIN_STONE | 3 |
| STAMINA、USDT_STAMINA、JADE_STAMINA、ORIGIN_STONE_STAMINA | 0 |
| 自定义币种 | 登记时指定0–18 |

前端直接提交实际金额，后端读取币种目录转换；链上资产精度不参与前端钱包换算。

这样设计是为了让钱包余额、不可变流水、投注扣款和 outbox 能在同一事务中精确相等，避免二进制浮点误差，并让幂等重试与并发校验得到完全相同的结果。数据库余额不得为负，单笔金额通常必须大于 0。赔率同样使用 `payout_multiplier / payout_divisor` 的定点整数；无法整除的派奖最小单位余数会被整数除法截去，不产生小数最小单位。

内部使用int64最小单位整数；前端响应只接收十进制字符串，因此大余额不会在JSON解析时损失精度。请求大金额也应使用字符串，避免浏览器发送前丢精度。

## 链上充值

充值网络与币种由 PQPA 应用配置决定，前端不得硬编码 TRON 或其他网络：

1. 调用 `GET /v1/assets` 获取当前启用的 `chain_code`、`token_code`、精度和 `support_withdraw`。
2. 玩家选择资产后，调用 `GET /v1/deposit-addresses?chain_code=POLYGON&token_code=USDT` 查询既有地址。
3. 地址不存在时，调用 `POST /v1/deposit-addresses` 并传入相同的 `chain_code` 和 `token_code` 创建地址。
4. 切换网络或币种时必须清空之前展示的地址，避免跨链误充值。
5. 创建地址响应可能包含 `memo`；存在时必须与地址一起展示和复制。

## USDT 提现

提现网络同样来自 `GET /v1/assets`，只允许选择 `support_withdraw=true` 的资产。调用 `POST /v1/withdrawals` 时必须提交 `chain_code`、`currency`、目标地址、可选的 `destination_memo`、实际币种金额和客户端幂等键。服务端执行地址格式、单笔最小/最大金额和 UTC 每日累计限额检查；超出每日限额返回 409。后台审批通过后由 Worker 调用 PQPA 出金；最终状态由 PQPA Webhook 更新，回调丢失时由 Worker 主动对账补偿。

## 管理后台接口

后台监控和统计仅提供后端 JSON 契约，不依赖任何管理端前端项目：

- `GET /v1/admin/monitor/bets?user=&game_type=&limit=100` 返回当前接受中的投注及完整期号、房间、模式和赔率快照；`GET /v1/admin/monitor/rounds` 单独返回每个启用玩法当前轮次的 `bet_closes_at` / `result_at` 和 `server_time`。兼容接口 `GET /v1/admin/monitor` 仍返回两者。
- `GET /v1/admin/bets` 跨玩家查询投注并返回相同的期号、房间和赔率字段；`GET /v1/admin/ledger` 查询统一流水；`GET /v1/admin/refunds-clearances` 查询结算退款、主动取消退款和下分/清退明细，投注退款记录包含关联 `bet_id`、期号和房间。三个接口均支持时间、用户和分页筛选。
- `GET /v1/admin/dashboard?user=&from=&to=` 返回玩家统计及按币种全局统计；全局数据自动排除虚拟账户。
- `GET /v1/admin/users/{userID}/login-ips` 返回玩家用过的 IP，并在每个 IP 下嵌套该地址登录过的其他用户；也可用 `GET /v1/admin/login-ips/{ip}/users` 直接反查。
- `POST /v1/admin/virtual-accounts` 创建可登录虚拟账户；`/v1/admin/robot-plans` 管理多条按期号运行的模拟投注计划，详见本文开头流程。虚拟账户不计入看板全局充值、流水和余额统计，禁止下分。

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
| 金额 | `amount`、`stake`、`reward` 等 | 请求为实际金额数字或字符串；响应为实际金额字符串，不含最小单位字段 |

排行榜配置使用整数 `version` 和 `rules` 数组；每档 `rank_from/rank_to` 为整数，`reward` 为实际奖励金额，`enabled` 为布尔值。任务、转盘同样提交实际金额。

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

返回：`operation_id`（UUID）、`user_id`（公开数字 ID）、`currency`、`action`、`amount`（正数）、`delta`（增减带符号）、`balance_before`、`balance_after`（操作前后可用余额）、`frozen`（冻结余额）、`duplicate`、`occurred_at`。金额字段均为实际金额十进制字符串，时间为 RFC3339。重复请求 `duplicate=true`，返回首次操作快照，不代表当前余额。

- 下分/人工扣分仅扣可用余额，不动冻结，不取消投注。余额不足 409；虚拟账户禁止两种扣款，返回 403，允许上分/赠分。
- 同一请求编号修改用户、币种、操作、金额或备注返回 409；等值金额（如 "1" 与 "1.0"）视为相同。
- 未设置全局一级密码 409；密码错误 401；无管理员权限 403；参数/精度错误 400。
- 新流水类型：`admin_credit` 上分、`admin_debit` 清退、`admin_reward` 赠分、`admin_penalty` 人工扣分。业务编号 `business_id` 对应 `operation_id`。
- 看板 `global[].credit` 为人工上分；总充值由链上 `deposit` 加人工 `credit` 展示。新增 `clearance` 为人工下分+积分审核成功扣款+链上最终成功扣款（不含冻结、驳回和投注退款），`gift` 为人工赠分，`penalty` 为人工扣分，全部按币种，排除虚拟账户。人工扣分不计游戏输赢。
- `players[].funds[]` 按币种返回完整投注、资金和余额统计及展示字符串；0052起移除玩家顶层跨币种金额汇总，详见本文开头字段说明。
- 金额、流水、成功审计、幂等结果和 outbox 同一事务，失败全部回滚；不在任何持久化记录中保存操作密码。

### 旧上分入口与提现的边界

`POST /v1/admin/credits` 保留，但现在允许 admin/operator 且必须提交 `first_password`；其他字段为上表去掉 `action`。等价于统一接口的 `action=credit`，两者共用幂等空间。返回原 `CreditResult` 字段，`credited=false` 表示重放。

发布时先执行所有待应用迁移（包含 `0049` 和 `0050_admin_security_passwords.sql`），再更新 API。旧上分入口同样需要改传 `first_password`；不再接受 `secondary_password`。

玩家积分清退申请、链上提现仍保留原申请→冻结→审核流程，与后台直接上下分分开。不要用后台 Token 代玩家调用申请接口，也不要对同一笔提款既做人工下分又批准提现，避免重复扣款。链上提现仅在服务商最终确认后计入清退，审核成功不代表到账。

### 积分下分/清退调用顺序

1. **玩家端，玩家 Token**：查询 `GET /v1/wallets/{本人公开ID}/all`，确认 `POINTS` 可用余额。
2. **玩家端**：`POST /v1/point-withdrawals` 提交申请。例如下分 `1.5` 宝石：

   ```json
   { "request_id": "point-withdraw-001", "amount": 1.5, "remark": "申请下分" }
   ```

   | 字段 | JSON 类型 | 必填 | 说明 |
   | --- | --- | --- | --- |
   | `request_id` | string | 是 | 幂等标识，网络重试复用。 |
   | `amount` | number或string | 是 | 实际币种金额，下分100传100，下分1.5传1.5；后端转换精度。 |
   | `remark` | string | 否 | 申请备注；当前申请列表响应不返回此字段。 |
   | `account_id` | string | 否 | 正常登录时不要传，由 Token 确定本人；只供鉴权关闭的本地开发回退使用。 |

   不传 `currency`，这个接口固定操作 `POINTS`；必须传 `amount`；不传二级密码字段。当前没有在下分接口中自动验证二级密码的链路。
3. **服务端**：同一事务将金额从可用余额转入冻结余额，记录申请和流水。返回 201，申请字段为 `id`（UUID string）、`user_id`（公开数字 ID）、`client_request_id`（string，对应请求中的 `request_id`）、`amount`（十进制字符串）、`status="requested"`、`created_at`（时间字符串）。重复申请返回原申请，不再冻结。
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
   | `amount` | number或string | 是 | 实际币种金额，下分100传100，下分1.5传1.5；后端转换精度。 |
   | `account_id` | string | 否 | 正常鉴权时省略，仅本地关闭鉴权时回退使用。 |

   正确请求体形状（链和地址必须替换为实际选择值）：

   ```json
   {
     "client_request_id": "chain-withdraw-001",
     "chain_code": "所选链代码",
     "currency": "USDT",
     "destination_address": "所选链的有效收款地址",
     "amount": 100
   }
   ```

3. **服务端**：检查余额、虚拟账号限制、链/资产支持、地址、最低/单笔/每日限额及精度；冻结金额后返回 201。响应主键为 `withdrawal_id`（UUID string），不是积分申请的 `id`；并包含 `client_request_id`、收款地址、链、币种、实际金额 `amount`、string `status` 和时间 `created_at`。
4. **后台端，后台 Token**：`GET /v1/admin/withdrawals?status=requested` 查询待审核列表；`GET /v1/withdrawals/{withdrawalID}` 查详情。后台列表只实现可选 string `status` 过滤，固定最多 50 条，不支持用户筛选或翻页。当前链上提现 DTO 不返回所属用户 ID，不能假设列表有 `user_id`；需要按用户辅助核对时，可用 `GET /v1/admin/refunds-clearances?user=100009&status=requested&limit=50&offset=0`，筛出 `record_type="withdrawal"`，其 `id` 对应提现 UUID。
5. **后台通过**：`POST /v1/admin/withdrawals/{withdrawalID}/approve`，不需要请求体，200 返回提现对象，`status="approved"`。金额此时仍冻结，后续由 Worker 出金。
6. **后台驳回**：`POST /v1/admin/withdrawals/{withdrawalID}/reject`，建议传 `{"reason":"收款信息需要核实"}`；`reason` 为可选 string。200 返回提现对象，`status="cancelled"`，金额解冻退回原钱包。两种审批均只允许 `requested` 状态；重复审批返回 409。
7. **跟踪结果**：查询提现详情，正常路径为 `requested → approved → broadcasted → confirmed`；`confirmed` 才代表服务商确认成功并扣除冻结额。出金失败为 `failed` 并解冻；后台驳回为 `cancelled`。发生审批超时先查当前状态，不要再发一笔新申请，也不要人工另扣一次余额。

链上提现详情可能额外返回 `provider_order_id`、`tx_hash`、`failure_reason`（均为可选 string）。审核完刷新用户钱包、统一流水和退款/清退明细；后台不能用 `GET /v1/users/me/ledger` 查看玩家，该路径始终是当前 Token 本人的流水。

### 上下分共同约定与错误处理

- 金额类型统一遵循[实际金额接口协议](./amount-contract.md)，上分、下分、投注、奖励均提交实际金额，不在前端转换精度。
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

HTTP 历史消息、HTTP 发送响应、Socket 发送确认和 `chat.message.created` 使用相同的消息结构。前端通过 `message.sender.display_name` 显示名称，通过 `message.sender.avatar_url` 显示头像；头像为空时使用本地默认头像。`sender.user_id` 是公开数字 ID，前端不会收到内部 UUID。站内 `/v1/avatars/...` 头像地址可直接交给图片组件显示。系统消息可能没有 `sender`。

0068 起 `sender.is_staff` 为布尔值，true 时显示“系统管理员”标识，false 不显示。后端按发送时数据库中的 admin/operator 角色保存快照，普通玩家为 false；不可通过请求参数设置，不要按昵称或固定用户 ID 判断。角色后续变化和重复发送不会改变原消息标识。迁移前历史消息因无法重建发送时角色，统一默认 false；无 sender 的系统消息不通过此字段判断。部署先执行 0068，再更新 API 和 Realtime。

消息与 `chat.message.created` outbox 事件在同一个数据库事务中提交。客服消息定向推送给房间成员及 `admin`、`operator` 的在线连接，无需加入房间或订阅 `chat`；不会发送给其他玩家。发送者可能同时收到发送确认和事件，应按消息 `id` 去重。公共消息通过 WebSocket 的 `chat` topic 广播。断线期间不保证事件补发，重连后通过 HTTP 查询历史消息对账；推送依赖 worker、NATS 和 realtime 正常运行。

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

- `POST /v1/chat/rooms/{roomID}/red-packets`：使用 `USDT` 或 `POINTS` 创建红包。请求包含 `client_request_id`、`currency`、`total`、`packet_count` 和可选 `greeting`。
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

## LULU 彩石充提

使用已有币种 `ORIGIN_STONE`，钱包 `decimals=3`，1 彩石 = 1 ORIGIN_STONE，金额请求使用正整数字符串。玩家无需长期绑定，按单填写转出/收款噜噜 UID。噜噜上下分仅接受整数彩石，前端仍传实际数量（例如 "100"），不乘 1000；后端按 3 位精度记账，所有订单和配置 currency 返回 ORIGIN_STONE，不再新增 LULU 币种。单笔最大 9223372036854775。

| 方法与路径 | 用途 |
| --- | --- |
| GET /v1/lulu/config | 通道状态及平台收付 UID |
| POST /v1/lulu/deposits | `{request_id,lulu_uid,amount}` 创建上分订单，随后转赠；实际到账自动匹配入账 |
| POST /v1/lulu/withdrawals | 同样字段创建下分订单并冻结 ORIGIN_STONE |
| GET /v1/lulu/orders | 本人订单，可选 kind/status/limit/offset，返回 items |
| GET /v1/admin/lulu/orders | 后台订单，同样筛选和分页 |
| POST /v1/admin/lulu/orders/{orderID}/review | `{action,evidence,first_password}` 审核或对账，路径 UUID |
| GET /v1/admin/lulu/config | 读取配置，admin/operator |
| PUT /v1/admin/lulu/config | 保存配置，admin/operator，需二级密码及最新 version |
| POST /v1/admin/lulu/send-code | 发送验证码，admin/operator，需二级密码 |
| POST /v1/admin/lulu/login | 短信登录并保存 UID、Token，admin/operator，需二级密码 |
| GET /v1/admin/lulu/health | 最近完整采集成功时间及去敏错误状态 |

### 玩家流程

1. `GET /v1/lulu/config` 查看通道开关、平台收款 UID 和 1:1 比例。
2. 上分前 `POST /v1/lulu/deposits`，例如：

   ```json
   {"request_id":"deposit-001","lulu_uid":"1234567","amount":"100"}
   ```

   建议先成功创建订单，再在噜噜向返回的 `receiver_uid` 转赠 **100** 彩石。
   订单转账窗口为创建前 3 分钟至创建后 3 分钟；同一平台收款号+转出 UID 最多有一笔有效待处理申请。
   多个不同噜噜号可给同一个平台玩家上分。相同用户+方向+request_id 防重，
   修改 UID 或金额后重用键返回 409。
3. 采集进程读取真实到账，按收款号、转出 UID、整数数量、订单转账时间窗口匹配。
   实际到账一经匹配即自动入账，不要求后台批准。
   余额、订单、到账归属、账本、余额通知 outbox 在同一事务提交。
   `GET /v1/lulu/orders?kind=deposit` 查看 `confirmed`，随后刷新统一钱包。
4. 下分 `POST /v1/lulu/withdrawals`，请求字段与上分相同，但 `lulu_uid` 是
   **目标收款号**。创建成功立即冻结 ORIGIN_STONE。`GET /v1/lulu/orders?kind=withdrawal`
   查看进度，余额通过现有钱包接口读取。

这采用用户确认的“填写转出 UID + 查询实际转赠记录”归属政策，**并不验证 UID
所有权**。别人抢先用相同 UID 和金额创建申请仍存在冒领风险；当前没有绑定、
转赠备注验证或验证码。不能向玩家声称已经验证身份。需要强归属验证时应另行扩展。
允许转赠后 3 分钟内下单，窗口外、金额不符和未认领转入只存入 `lulu_receipts`，不自动加分。

### 后台审核和对账

本节订单审核和对账要求当前 active admin/operator 会话；审核操作另验证后台全局一级密码。配置和短信登录权限见下节。
用户公开 ID 与现有接口一致，噜噜 UID 始终是字符串。

- `GET /v1/admin/lulu/orders?kind=withdrawal&status=requested&limit=50&offset=0`
- `POST /v1/admin/lulu/orders/{orderID}/review`

  ```json
  {"action":"approve","evidence":"收款账号和申请数量已核对","first_password":"后台一级密码"}
  ```

- `approve`：requested → approved，**不扣除冻结额，也不在 HTTP 请求里转赠**。
- `reject`：requested → rejected，原子解冻。不能驳回已派发订单。
- 专用进程：approved → sending，先持久化，再调用一次真实转赠。
  明确成功 → confirmed，扣除冻结额；发送前查询失败 → failed，解冻；
  发送后任何不明响应（含业务错误但无已验证的失败语义）→ unknown，保持冻结。
- unknown 必须先人工核对真实转出记录，再提交 `confirm_paid`（完成扣款）或
  `confirm_not_paid`（解冻），`evidence` 保存具体核对依据。
- 进程崩溃遗留的 sending 在持有账号独占锁的进程启动时转 unknown。
  不重发 unknown/sending。切勿通过人工下分接口再次扣该笔余额。
- `GET /v1/admin/lulu/health` 返回最近一次完整采集成功时间及去敏错误状态；
  不返回 Token、手机号、加密密钥或原始服务商报文。

列表参数为 kind/status/limit/offset，按创建时间、ID 倒序，返回 `{items:[]}`。
当前没有单独订单详情接口，可从列表找到订单 ID。审核成功结果可安全重试；
相反决定或非允许状态返回 409。审计和资金变化在同一事务，密码不写审计。
真实玩家才可创建订单，虚拟账号和非玩家不能充提。即使开发环境关闭鉴权，
这些 API 也不允许匿名操作。

### LULU 后台收付设置

admin/operator 可调用 `GET /v1/admin/lulu/config` 和 `PUT /v1/admin/lulu/config`。PUT 完整提交 `{enabled,version,second_password}`，验证后台全局二级密码。读取后携带最新 version 保存，成功返回新版本；过期版本、未暂停的账号切换或存在在途订单返回 409。玩家 `/v1/lulu/config` 与专用进程使用同一数据库配置，不再读取环境变量中的开关/收付号。协议密钥可通过 PUT 写入，Token 只通过短信登录获取，读取只返回配置状态，不返回凭据。

Swagger 测试 LULU 接口时，在 Authorize 的 bearerAuth 中填写管理员登录返回的 access_token 原文（不加 `Bearer ` 前缀），请求应包含 `Authorization: Bearer <access_token>`。文档更新后刷新页面重新授权；账号再次登录会使旧会话失效。

后台 PUT `/v1/admin/lulu/config` 新增可选 `api_url`、`protocol_key`、`scan_start_at`。协议密钥空字符串或省略保留；token 字段不再接受；GET 新增 `api_url`、`scan_start_at`、`token_configured`、`protocol_key_configured`。首次启用须具备合法 UID、HTTPS 地址、协议密钥、登录 Token 和非未来采集时间；更换 UID 须通过新账号短信登录，修改 API 地址先暂停，已有水位禁止调晚起始时间。服务器须配置 LULU_CONFIG_ENCRYPTION_KEY，缺失或无法解密返回 503。


首次保存基础配置的请求示例（version 使用最新值，地址、密钥和时间替换为实际值）：

```json
{
  "enabled":false,
  "version":3,
  "api_url":"https://example.invalid",
  "protocol_key":"32字节协议密钥的Base64",
  "scan_start_at":"2026-09-08T00:00:00+08:00",
  "second_password":"后台全局二级密码"
}
```

GET 和 PUT 成功返回 receiver_uid、enabled、version、updated_at、api_url、scan_start_at、token_configured、protocol_key_configured。api_url、scan_start_at 省略保留；protocol_key 省略或空字符串保留，值必须为 32 字节密钥的 Base64。API 地址只允许 HTTPS，不含用户信息、查询参数和片段。UID 由短信登录取得，PUT 不接受 receiver_uid，后端保留当前账号；GET 仍返回该字段供只读展示。空配置也不允许接管在途订单。已有进度时调早采集起点不会重扫历史。停用不撤销已派发付款。

### 噜噜短信登录

日常流程与原采集器一致，不需要手工复制Token：

1. 先保持通道关闭，用配置接口保存 api_url、protocol_key、scan_start_at；receiver_uid 由后端维护，禁止提交 receiver_uid 或 token 字段。
2. GET 配置获取 version，POST `/v1/admin/lulu/send-code` 提交 `{phone,version,second_password}`。
3. 收到短信后 POST `/v1/admin/lulu/login` 提交 `{phone,code,version,second_password}`。
4. 登录成功自动取得UID、加密保存Token，返回配置和新version；首次登录仍保持关闭，确认配置后用新version启用。已有同UID的启用通道登录成功后继续运行。

两个接口admin/operator可用，使用平台管理员Token和全局二级密码。无需旧噜噜Token即可登录；手机号为11位数字，验证码为4–8位数字。验证码、Token和协议密钥不写入审计或响应。保存过程复核版本和账号切换规则；返回不同UID时须先暂停并处理旧在途单。版本冲突后重新读取配置，必要时重新获取短信验证码。

发码整个通道60秒一次，登录5秒一次，失败也占用限流窗口；不自动重试短信。上游必须返回成功码，HTTP 200的业务失败不视为成功。接口失败不替换现有Token。Token失效后仍需人工输入短信验证码，未实现免验证码自动续期。手动Token配置入口已删除，提交 token 字段（包括空字符串）返回400；无Token文件读取兼容逻辑。发码成功返回 {"status":"sent"}；登录成功返回配置对象及新 version。

### 请求、响应与错误处理

所有接口使用平台会话 Authorization: Bearer <access_token>，不是噜噜 Token。订单归属于当前登录玩家。request_id 由前端自动生成（建议 UUID），不让玩家填写；同次操作超时重试复用原值。

创建成功返回 201 和订单对象；列表、配置、登录、审核成功返回 200。订单字段包含 id（UUID）、user_id（平台公开数字 ID）、kind、request_id、lulu_uid、receiver_uid、amount（字符串）、currency、status、created_at、expires_at，以及可选 receipt_id、evidence。下分不使用 expires_at。

审核仅支持下分，上分审核返回 409。上分状态为 requested → confirmed，超时未到账变为 expired；没有 matched 状态。延迟采集到的流水若实际发生于有效期内，过期订单仍可入账。采集默认每 5 秒一轮，网络耗时可能延迟，不能承诺 5 秒到账。订单变化后刷新钱包，不自行累加余额。

| 状态码 | 处理 |
| --- | --- |
| 400 | 参数错误或提交已删除的 token 字段，修正请求 |
| 401 / 403 | 检查登录、角色、账号状态及后台操作密码 |
| 409 | 幂等参数、订单状态或配置版本冲突，重新查询后处理 |
| 429 | 短信或登录限流，等待后再操作 |
| 502 | 上游登录或短信请求失败 |
| 503 | 通道或凭据加密配置不可用，联系后端排查 |

字段约束和响应 schema 见 [OpenAPI](openapi.yaml)。前端接入流程统一维护在本节。

噜噜订单列表 status 可省略或为空；非空仅允许 requested、approved、sending、unknown、confirmed、rejected、failed、expired。非法值（含旧 matched）返回 400。发码接口不接受 code 字段，空字符串或 null 同样返回 400；验证码只提交给登录接口。

噜噜上分转出 UID、下分收款 UID 均不能与平台收付 UID 相同；否则返回 400：玩家噜噜账号不能与平台收付账号相同，请填写玩家自己的噜噜账号 ID。

GET /v1/admin/bets 每条投注新增 balance（该投注币种的当前可用余额）和 frozen_balance（当前冻结余额），均为实际金额字符串，前端不再转换精度。余额为查询时的钱包状态，不是下注时余额，不汇总其他币种。

后台轮询 GET /v1/admin/lulu/health：token_invalid=true 表示采集收到上游 HTTP 401/403，展示 last_error 并引导短信重新登录；不是主动推送。采集成功后清除提示。普通网络异常仅更新 last_error，不代表 Token 已失效。token_configured 只表示存有凭据，不代表凭据有效。

噜噜 health 新增 last_cycle（最近采集成功或失败摘要）、down_at（当前 Token 掉线起点，Unix 毫秒）、recovered_at（最近恢复时间，Unix 毫秒）、last_down_ms（上次掉线持续毫秒数）。时间无记录为 0，摘要无记录为空。HTTP 401/403 首次设置 down_at，重复失败不覆盖；后续网络失败不清除 Token 掉线状态，只有完整采集成功才清除 down_at 并更新恢复时间和时长。登录成功本身不代表采集恢复。部署前执行 0075 迁移。


## 聊天图片消息

客服和公共聊天支持每条消息一张 JPEG、PNG 或 WebP 图片，可附带不超过 2000 字的文字。先使用现有 POST /v1/uploads/authorize，按返回的上传方式写入文件并完成确认，取得 upload.id；上传大小遵循上传接口限制。PDF 不能作为聊天图片。随后通过 WebSocket 发送：

```json
{"v":1,"type":"chat.send","room_id":"房间 UUID","request_id":"客户端生成的唯一标识","body":"可选说明","image_upload_id":"已确认的上传 UUID"}
```

纯图片允许 body 为空；无图片时仍必须有文字。图片必须属于发送者，禁止传外部图片 URL。重复请求复用 request_id，返回原消息，不替换原内容。发送确认、历史列表和 chat.message.created 均包含 image_upload_id、image_url，文字消息省略这两项。

image_url 指向 GET /v1/uploads/{uploadID}/content，需要携带平台 Bearer Token；浏览器使用 fetch 获取 Blob 后通过 URL.createObjectURL 展示并适时 revokeObjectURL，不把 Token 放进 URL。上传者可读取本人文件；其他人只可读取其有权查看房间内的可见图片消息，隐藏或删除消息不再授予附件访问权限。图片同时发布到公共房间后，其他已登录用户可读取。

部署前执行 0076_chat_images.sql，再更新 API、Realtime。后端接口已支持，前端需实现上传按钮和图片渲染。

噜噜充值允许先转赠、后下单，但转赠时间最多早于订单创建时间 3 分钟（含边界）。已采集的未认领流水在创建订单时匹配，晚采集的流水同样使用此窗口。收款 UID、转出 UID 和数量必须一致，同一流水只能入账一次；创建订单响应可能直接为 confirmed。超过 3 分钟的提前转赠不会自动入账，不验证转出 UID 所有权。

0077 起新建噜噜充值订单付款期限为 3 分钟。采集进程每轮自动更新过期状态（通道关闭时也执行），列表查询也会更新过期状态。以 expires_at 为准；旧订单保留已给出的截止时间。若延迟采集到实际发生于有效窗口内的付款，expired 仍可转为 confirmed。

噜噜重复创建待处理充值单返回 409，code=lulu_deposit_pending，包含 lulu_uid 和 retry_after_seconds（按原订单 expires_at 计算并向上取整的剩余秒数）。error 包含账号与等待提示；前端可据 retry_after_seconds 倒计时，但倒计时结束不代表已入账，应刷新订单状态。其他幂等或审核冲突不返回此 code。


### 噜噜上游转赠流水

GET /v1/admin/lulu/transfers?direction=received&page=1&size=50，direction=sent 查询转出。仅 admin/operator 携平台 Token 调用，后端使用当前配置的噜噜凭据；无需传 UID 或噜噜 Token。通道须开启。page 为 1–1000，size 为 1–100，默认 1/50。收到和转出分别分页，total 是上游返回总数，不代表无限历史覆盖。

响应包含 receiver_uid、page、size、total、items。每条记录返回 id（观测指纹，不是上游交易号）、direction、counterparty_uid、nickname、item_id、amount（收到为正、转出为负的整数字符串）、occurred_at、linked。item_id=102201 是彩石，其他物品也原样展示。

收到记录若已入账关联充值单，linked=true 并返回 order_id、order_status、link_method=receipt_id；否则 linked=false、link_reason=no_matching_receipt。转出彩石记录按当前平台 UID、对方 UID、金额及自动提现扣款账本完成时间（与转赠时间前后相差不超过 30 秒，包含边界）匹配，唯一候选返回 linked=true、order_id、order_status 和 link_method=account_uid_amount_completion_window。这是时间与金额推断的展示关联，不是上游交易号核实，前端应显示“匹配提现订单（时间金额匹配）”。不会据此确认提现或改变余额。无候选为 no_matching_withdrawal，多笔候选为 ambiguous_withdrawal，同页多条流水对应同一订单为 ambiguous_transfer，其他道具为 unsupported_item；均 linked=false。人工确认的提现不参与推断。扣款与转赠时间相差超过 30 秒时不匹配；不同分页存在相似流水时此推断也不能作为财务对账凭证。

### 当前平台噜噜账号余额

`GET /v1/admin/lulu/balance`，admin/operator 携平台登录 Token 调用，无请求参数、无需操作密码；噜噜 Token、UID、协议密钥均由后端读取。通道暂停时也可查询。每次实时请求，整体超时 15 秒，不缓存。

```json
{"receiver_uid":"34445963","item_id":102201,"balance":"5160.07704","queried_at":"2026-09-09T15:30:00Z"}
```

balance 为保留原始精度的十进制字符串，表示平台噜噜账号的彩石库存，不是玩家 ORIGIN_STONE 钱包余额。查询不更改余额、订单或账本。失败返回中文 error，不能显示为 0：400 配置不完整；401 平台登录无效；403 无权限；502 噜噜登录失效或上游异常；503 凭据解密/服务不可用；504 查询超时。只有上游明确返回零，才展示零余额。
