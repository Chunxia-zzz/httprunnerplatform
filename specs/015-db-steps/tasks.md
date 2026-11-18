# R1 任务清单

- [x] T1 模型：DbSource 表 + test_step 新列（db_source_name/db_sql/db_params）+ 迁移注册
- [x] T2 db_source 服务：CRUD（管理员写/全员读、password 掩码与回传保持）、项目隔离
- [x] T3 dbstepsvc：SQL 执行（三驱动 DSN）、回环端点 POST /dbstep（token 校验、
      conn 项目白名单、错误透传）、per-run token 注册表与终态关闭
- [x] T4 执行链路：run/run_suite 生成 token 写 .env 并登记、终态关闭；loopback URL 配置
- [x] T5 编译器：db 步骤渲染为回环 POST（conn/sql/params 体 + token 头 + 动态变量）
- [x] T6 校验器：conn 存在性/SQL 非空/params JSON/写操作位置警告
- [x] T7 用例服务：StepReq 透传 db 字段
- [x] T8 调试台：db 步骤服务层直执行（不经引擎）
- [x] T9 前端：StepEditor 数据库面板 + 环境页数据库连接区块 + API 封装
- [x] T10 单测：DSN 构造、端点、编译产物、校验器、掩码
- [x] T11 端到端（真实引擎 + sqlite）：造数→HTTP→查库断言全流程、断言错归因、
      SQL 错可读、跨项目拒绝、token 失效、调试直执行、掩码
- [x] T12 文档同步：契约 /dbstep 与 db_source、数据库设计 db_source 表、
      引擎实测 A33（回环+token 机制）、产品规划 R1 ✅、README、验收指南
- [x] T13 全量门禁 check.sh + 提交推送 + CI 确认
