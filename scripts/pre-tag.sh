#!/usr/bin/env bash
# 打 tag 前的检查。用法：bash scripts/pre-tag.sh
# 退出码：0 = 全部通过，1 = 有问题需要修
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

fail=0
section() { printf '\n== %s\n' "$1"; }
ok()      { printf '   ok\n'; }
bad()     { printf '   FAIL: %s\n' "$1"; fail=1; }

section "gofmt"
if out=$(gofmt -l . 2>&1) && [ -z "$out" ]; then
	ok
else
	bad "以下文件未格式化，运行 gofmt -w ."
	printf '%s\n' "$out"
fi

section "go build"
if out=$(go build ./... 2>&1); then ok; else bad "编译失败"; printf '%s\n' "$out"; fi

section "go vet"
if out=$(go vet ./... 2>&1); then ok; else bad "vet 有输出"; printf '%s\n' "$out"; fi

section "go test -race"
if out=$(go test -race ./... 2>&1); then
	ok
	printf '%s\n' "$out" | sed 's/^/   /'
else
	bad "测试失败"
	printf '%s\n' "$out"
fi

section "文档里的旧接口契约"
if hits=$(grep -n 'localhost:8080/kv/' README.md 2>/dev/null) && [ -n "$hits" ]; then
	bad "README 里还有旧路径 /kv/（应为 /v1/kv/）"
	printf '%s\n' "$hits"
else
	ok
fi

section "工作区状态"
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	printf '   警告：工作区有未提交改动，这些改动不会包含在 tag 里\n'
	git status --short | sed 's/^/   /'
else
	ok
fi

printf '\n'
if [ "$fail" -eq 0 ]; then
	printf '✅ 全部通过，可以打 tag：git tag vX.Y-name\n'
else
	printf '❌ 有检查未通过，先修复再打 tag\n'
fi
exit "$fail"
