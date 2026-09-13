# Lulu 三游戏共享结算轮次设计

## 目标

星海逃杀（`xdy`）、怒翎破阵（`lh`）和绿茵疾冲（`race`）的同一上游期号在平台内各只创建一条 `round`。直选、上下、左右、单双和躲避是该轮次内的投注玩法，不再各自创建独立轮次。哈希游戏继续保持既有行为。

## 当前问题

`0086_lulu_external_draws.sql` 为每种投注方式创建一个 `game_type`。`externaldraw.Service.createRounds` 按 `game_type` 插入轮次，因此 `xdy` 的一个上游期号会产生五条轮次。每条都有独立状态和结算，监控页便显示同一期存在多个“等待结算中”卡片。

这种结构也让同一场外部开奖可能发生部分玩法已结算、部分玩法未结算的状态分叉；它与哈希游戏“一条玩法类型轮次，多个玩法配置共享该轮次”的模型不一致。

## 选择的方案

采用真实共享轮次，而非只在界面聚合：

1. 三个外部游戏各有一个启用的共享 `game_type`：`lulu-xdy`、`lulu-lh`、`lulu-race`。
2. 每个共享 `game_type` 的规则保存该游戏完整的标准化开奖号码集合及上游结果映射。
3. 每个投注方式配置为一个独立的 `lulu_play`。其配置包含：玩法代码、显示名、允许选择、赔率、各币种投注上下限、结果映射与是否为躲避玩法。
4. 投注请求仍使用原有 `play_mode` 字段，但三游戏使用新的稳定代码（`direct`、`up_down`、`left_right`、`odd_even`、`dodge`、`winner`）。后端依玩法配置校验选择与限额，并将赔率写入现有快照列。
5. 结算时由共享游戏规则从已确认的上游结果产出标准化结果；每笔投注再根据其 `play_mode` 配置判定输赢。现有资金、账本、outbox 与幂等结算事务保持不变。

## 数据模型与迁移

新增迁移 `0087_lulu_shared_rounds.sql`：

- 新建 `lulu_play_configs`，以 `game_type_id + code` 唯一，存储 `name`、`sort_order`、`enabled`、`outcomes`、`result_map`、`dodge_mode`、`payout_multiplier`、`payout_divisor`、按币种下注限额。
- 创建或更新三个共享 `game_type`；其 `rules.source` 均为 `lulu_ws`，`rules.extras.external_game` 分别为 `xdy`、`lh`、`race`，并储存用于取得开奖号码的完整原始房间结果集合。
- 停用旧的 `lulu-xdy-*`、`lulu-lh-winner`、`lulu-race-*` `game_type`，但不删除它们，也不重写历史 `rounds`、`bets`、账本或 outbox 数据。
- 对尚未结算且属于旧三游戏类型的轮次执行取消退款，以免部署后遗留资金无法结算；已结算与已取消的历史轮次保持不变。
- 外部开奖结果表 `external_draw_rounds` 不变；其 `(source, game, external_round)` 唯一性继续作为上游期号幂等键。

迁移必须在发布新版 API 与 Worker 的同一版本中运行。新 Worker 启动后，收到同一上游期号的关闭事件只会对相应共享 `game_type` 插入一条轮次，冲突键保证重复消息不重复建期。

## 应用层设计

### 玩法配置

在 `internal/domain/game` 引入共享 Lulu 配置解析与校验：

- 读取共享 `game_type` 的上游游戏标识和开奖结果映射。
- 按 `round_id` 和 `play_mode` 读取已启用 `lulu_play_configs`。
- 统一校验 `selection.pick`、币种限额、赔率和躲避规则。

`betting.Service.PlaceBet` 在识别共享 Lulu 规则时要求有效 `play_mode`，使用对应玩法配置而不是 `game_types.rules` 的单一赔率/结果池；仍将选择、玩法代码和赔率快照写入既有 `bets` 与 `bet_placements` 字段。三游戏不使用 `game_room_id`，也不启用哈希的跨房间限制或订单合并。

`settlement.Service.SettleRound` 读取每笔投注的 `play_mode`。共享 Lulu 轮次的结果源只读取确认的外部结果并返回原始标准化房间号；结算服务根据每笔玩法配置将该结果映射为该玩法的 outcome，再使用该玩法的躲避/命中规则判定。非 Lulu 的结算行为保持不变。

### 外部开奖同步

`externaldraw.Service.createRounds` 继续依据 `external_game` 查询启用玩法，但查询结果从多个投注方式类型变为每游戏一个共享 `game_type`。`ON CONFLICT(game_type_id, sequence)` 保留，保证关闭事件重投不会重复创建轮次。

`LuluResultSource` 将返回共享游戏的标准化原始结果，而非在结果源阶段按旧 `game_type` 映射；玩法映射移动到每笔投注结算处，以支持同轮次的不同玩法。

## API 与客户端兼容

- `GET /v1/rounds?game_type=lulu-xdy` 等新代码返回每期一条开放轮次。
- 投注接口继续使用现有字段，但三游戏请求必须提交 `play_mode`。`game_room_id` 为空。
- 游戏配置响应为三游戏返回共享游戏类型及其 `lulu_plays`，使后台和玩家可以渲染玩法标签、赔率、选择与限额。
- 旧 `lulu-xdy-direct` 等代码在迁移后不再返回开放轮次；客户端应切换到共享代码。历史订单仍按其原始 `game_type` 查询和展示。
- OpenAPI 与前端对接文档必须同步更新，不保留将旧玩法代码作为新投注入口的描述。

## 哈希未开奖的处理边界

本次共享轮次改造不改变哈希调度与结算。当前本地的 TRON 取块、到期结算和 Worker 回归测试通过；测试环境故障必须以部署后的 Worker 日志和数据库轮次状态为依据，优先检查 `due round settlement failed` 与 `TRON block height query failed`。不得以猜测修改哈希结算规则。

## 验收与测试

1. 同一 `xdy`、`lh` 或 `race` 的关闭事件只创建一个共享轮次；重复事件不新增轮次。
2. 一条共享轮次可接受该游戏的所有有效玩法投注，拒绝缺失、未知或不匹配的 `play_mode`/选择/限额。
3. 同一共享轮次中各玩法按各自赔率、结果映射和躲避规则结算，钱包、账本与 outbox 仍由现有幂等事务处理。
4. 未结算旧轮次在迁移中取消并退款；历史结算记录和资金不被改写。
5. 现有哈希、K 线、外部开奖、Worker 与 API 合约测试全部通过。
