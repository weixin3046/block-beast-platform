# 后台单笔作废及普通用户/代理补齐

已获批准的参考对齐任务。只在当前本地 codex/reference-business-alignment 分支修改后端；不提交、不推送、不部署、不新建工作区、不调用远程服务器、不派生子代理。保留其他未提交修改。

先读 README.md、docs/architecture.md、docs/frontend-api.md、docs/openapi.yaml 相关接口和 AGENTS.md。使用测试先行。

## 要求

1. 新增后台单笔作废命令：仅 accepted 未最终结算，封盘后也可；按轮次→投注→钱包顺序锁，与结算互斥。admin/operator + second_password；request_id/reason必填，按操作人/request_id幂等，参数不一致409；真实单本金退款一次，模拟单只改状态；余额/账本/outbox/审计/幂等结果同事务。保存操作人、原因、期号；列表分页items/total。不能改已结算输赢。不删除历史。迁移编号0062保留给你。
2. 后台普通玩家创建：后端ID、强密码hash、登录名唯一、默认昵称、确认头像、零钱包和player角色；不可赋予后台角色、不可附带初始真钱余额；admin/operator且应用层复核角色，审计同事务。复用已有虚拟创建共享实现合理但不可先创建虚拟后转真实。
3. 后台绑定无上级用户：输入公开数字ID，不覆盖已有上级、防环、禁虚拟关系；序列化所有绑定路径避免并发成环，维护ltree路径包括已有后代；应用层复核admin/operator，审计同事务。原玩家Bind不能绕过并发保障。

## 文件范围
新建应用服务/测试和HTTP文件；可编辑 agent/service.go 绑定链路、operations/analytics.go 提取共享创建实现。不要编辑 server.go、cmd/api/main.go、docs/openapi.yaml、docs/frontend-api.md（主代理接线并同步）。通过新HTTP文件提供 Option 和 registerXXXRoutes(mux) 函数，告知主代理接线方法。避免修改已有返水/排行榜文件。

## 验证
本地数据库已应用0060/0061；DSN postgresql://postgres@/controls_test_v52?host=/private/tmp/block-beast-adjustments.e1pY52&port=55439 。GOCACHE=/private/tmp/block-beast-money-cache.1v3zZV 。需要本地socket权限可升级；只此本地测试库，勿访问任何远程。
测试覆盖失败权限、重复/参数冲突、模拟、已结算拒绝、绑定循环及重复、玩家创建角色/零钱包。金额HTTP只用实际金额字符串（内部int最小单位）。中文错误。

完整报告写 docs/superpowers/plans/admin-completion-report.md，包括文件、接口、测试命令/结果、TDD证据、接线方法。返回简短状态和关注点。
