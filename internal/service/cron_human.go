package service

import (
	"fmt"
	"strconv"
	"strings"
)

// 本文件把 cron 表达式翻成一句中文。
//
// 为什么值得写：列表页上如果只有 `*/15 9-18 * * 1-5`，用户每次都要
// 在脑子里编译一遍。而这恰恰是最容易出错的地方 —— 记错的人不会去核对，
// 只会以为自己配对了。翻成"工作日 9-18 点每 15 分钟"之后，
// 配错了自己一眼就能看出来。
//
// 覆盖不到的写法**原样返回**而不是猜：一句看不懂的原文，
// 好过一句看起来通顺但意思错了的中文。

// describeCronHuman 把 cron 表达式翻成中文短句。
func describeCronHuman(expr string) string {
	e := strings.TrimSpace(expr)
	if e == "" {
		return ""
	}
	if strings.HasPrefix(e, "@") {
		return describeDescriptor(e)
	}

	fields := strings.Fields(e)
	if len(fields) != 5 {
		return e
	}
	mi, ho, dm, mo, dw := fields[0], fields[1], fields[2], fields[3], fields[4]

	// 全通配 ⇒ "每分钟"。`* * * * *` 是最容易写错的一个表达式：
	// 很多人以为它是"每小时一次"，实际是 60 倍的量。这里必须说准。
	if mi == "*" && ho == "*" && dm == "*" && mo == "*" && dw == "*" {
		return "每分钟"
	}
	// 时分都固定 ⇒ "每天 HH:MM"，最常见的形态给它最直白的说法。
	if isSingle(mi) && isSingle(ho) && dm == "*" && mo == "*" && dw == "*" {
		return fmt.Sprintf("每天 %s:%s", pad2(ho), pad2(mi))
	}
	// 分固定、时任意 ⇒ "每小时的第 M 分"
	if isSingle(mi) && ho == "*" && dm == "*" && mo == "*" && dw == "*" {
		n, _ := singleOf(mi)
		return fmt.Sprintf("每小时的第 %d 分", n)
	}
	// 分是步长、时任意 ⇒ "每 N 分钟"。
	// 必须放在通用分支之前：通用分支会先写"每小时"再写"每 15 分"，
	// 拼出"每小时每 15 分"这种前后打架的话。
	if n, ok := stepOf(mi); ok && ho == "*" && dm == "*" && mo == "*" && dw == "*" {
		return fmt.Sprintf("每 %d 分钟", n)
	}

	var b strings.Builder
	if mo != "*" {
		b.WriteString(describeField(mo, "月", "每月", ""))
	}
	if dm != "*" {
		b.WriteString(describeField(dm, "日", "每日", "号"))
	}
	if dw != "*" {
		b.WriteString(describeWeekday(dw))
	}
	if ho != "*" {
		b.WriteString(describeField(ho, "点", "每小时", "点"))
	} else if b.Len() == 0 {
		b.WriteString("每小时")
	}
	// 分钟为 0 时不再追加"第 0 分"：说"工作日 9 点"就够了，
	// 加个"第 0 分"反而让人怀疑是不是另有含义。
	if mi != "*" && mi != "0" {
		b.WriteString(describeField(mi, "分", "每分钟", "分"))
	}
	out := b.String()
	if out == "" {
		return e
	}
	return out
}

// describeDescriptor 处理 @daily / @every 30m 这类描述符。
func describeDescriptor(e string) string {
	switch strings.ToLower(e) {
	case "@yearly", "@annually":
		return "每年 1 月 1 日 00:00"
	case "@monthly":
		return "每月 1 日 00:00"
	case "@weekly":
		return "每周日 00:00"
	case "@daily", "@midnight":
		return "每天 00:00"
	case "@hourly":
		return "每小时"
	}
	if strings.HasPrefix(strings.ToLower(e), "@every ") {
		return "每 " + strings.TrimSpace(e[len("@every "):])
	}
	return e
}

// describeField 描述某一字段。step 形态（*/n）说成"每 n 单位"。
func describeField(field, unit, allText, suffix string) string {
	if field == "*" {
		return allText
	}
	if n, ok := stepOf(field); ok {
		if unit == "点" {
			return fmt.Sprintf("每 %d 小时", n)
		}
		return fmt.Sprintf("每 %d %s", n, unit)
	}
	if n, ok := singleOf(field); ok {
		if unit == "点" {
			return fmt.Sprintf("%d 点", n)
		}
		return fmt.Sprintf("第 %d %s", n, unit)
	}
	return fmt.Sprintf("%s%s", field, suffix)
}

// describeWeekday 描述星期字段（0 与 7 都是周日）。
func describeWeekday(field string) string {
	names := map[int]string{
		0: "日", 1: "一", 2: "二", 3: "三", 4: "四", 5: "五", 6: "六", 7: "日",
	}
	if field == "1-5" {
		return "工作日 "
	}
	if field == "0,6" || field == "6,0" {
		return "周末 "
	}
	if n, ok := singleOf(field); ok {
		if name, ok2 := names[n]; ok2 {
			return "每周" + name + " "
		}
	}
	return "周{" + field + "} "
}

func isSingle(s string) bool { _, ok := singleOf(s); return ok }

func singleOf(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// stepOf 解析 `*/n` 与 `a-b/n`，返回步长。
func stepOf(s string) (int, bool) {
	idx := strings.LastIndex(s, "/")
	if idx < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s[idx+1:]))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func pad2(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%02d", n)
}
