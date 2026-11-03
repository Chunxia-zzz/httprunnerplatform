# httprunnerplatform Constitution

## Core Principles

### I. 不假绿（NON-NEGOTIABLE）
平台自算的值（重建的断言、重建的 URL、派生统计）必须显式标注来源，绝不与引擎权威值混同展示；宁可不给结论，也不给「看起来正常」的假象。执行前校验 + 执行后用例数对账，防静默漏跑。

### II. 一个用例一个子进程
执行隔离是归因的地基：单条用例失败不传染整批；失败归因以引擎 stderr 证据 + 步骤配对判据为准，平台只补偿引擎缺陷（如断言失败 panic），不篡改证据。

### III. DB 是唯一真源，YAML 单向编译
用例数据以数据库为准，YAML 是编译产物（单向）；封死双向同步。文件与 DB 不一致不是状态，是缺陷。

### IV. 双驱动字段交集
同一套 GORM 模型必须同时适配 MySQL 8 与纯 Go SQLite：JSON 一律存 TEXT、不用 ENUM、不用 `FOR UPDATE`/`JSON_EXTRACT` 等方言；并发靠进程内任务队列与独立工作区，不依赖数据库锁。

### V. 测试先行，门槛只能涨
所有功能有单测（优先用真实引擎产物做夹具）；CI 门禁（gofmt / vet / 总覆盖率 / ESLint / vitest / gitleaks）为硬约束，覆盖率只能涨不能跌；文档与代码不一致按缺陷处理。

## Additional Constraints

- 引擎锁死 hrp v4.3.6（官方二进制，子进程调用，不引入其 Go 库——CGO 依赖会毁掉交叉编译）；平台对其缺陷只做防御式补偿。
- 调度器**单实例**成立；多实例需先实现数据库行级锁抢调度权。
- 定位：30 人测试团队，默认 SQLite 单文件交付；MySQL 为可切换后端（切换前必须预发完整回归）。
- 单分支 main 直推（个人 side project），质量由 CI 门禁 + pre-push hook 保证，不设分支保护。
- 运行期零外网依赖；引擎遥测强制关闭（DISABLE_GA=true / DISABLE_SENTRY=true，字面量）。

## Development Workflow

- 开发流程：spec-kit SDD 六步（specify → clarify → plan → tasks → implement → converge）；缺陷走 bug 三件套（assess → fix → test，终局结论 verified / partial / failed）。
- 每次交付：`bash scripts/check.sh` 本地全绿 → push → CI 全绿才算完成；文档（README + docs/）必须与代码同步，未实现的显式标注「裁剪未做」。
- 交叉引用编号（实测 A 系、BUG 系）先存在后引用；文档内部链接不得有死链。
