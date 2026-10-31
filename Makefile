# httprunnerplatform 检查与构建入口
#
# 用法（Linux/macOS/CI 直接 make；Windows 无 make 时用 bash scripts/check.sh）：
#   make check        # 全部门槛：gofmt / vet / test -cover / 前端单测 / 前端类型
#   make test         # 只跑后端测试（带覆盖率）
#   make web-test     # 只跑前端单测
#
# 四条核心命令与 docs/项目进度检查报告.md 的建议一一对应：
#   gofmt -l / go vet / go test -cover / vue-tsc（npm run typecheck）
# 前端单测（vitest）是第 5 条，报告落地时一并纳入门槛。

GO ?= go

.PHONY: check fmt-check vet test web-test web-typecheck

check: fmt-check vet test web-test web-typecheck
	@echo ""
	@echo "==== check 全部通过 ===="

# gofmt 检查：有任何未格式化文件即失败，并列出文件名。
# 刻意不用 gofmt -w（检查门槛只报告，不代改）。
fmt-check:
	@echo "==== gofmt -l（应无输出） ===="
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; else echo "OK：所有 .go 文件已格式化"; fi

vet:
	@echo "==== go vet ===="
	$(GO) vet ./...

test:
	@echo "==== go test -cover ===="
	$(GO) test -cover ./...

web-test:
	@echo "==== 前端单测（vitest） ===="
	cd web && npm test

web-typecheck:
	@echo "==== 前端类型（vue-tsc） ===="
	cd web && npm run typecheck
