// 测试计划的端到端冒烟（零依赖，Node 22 自带 fetch / http）。
//
// 用法：
//   node scripts/smoke-plan.mjs                     # 默认连 http://127.0.0.1:8127
//   HRP_SMOKE_BASE=http://127.0.0.1:9000 node scripts/smoke-plan.mjs
//
// 与 smoke-suite.mjs 同一套路：自带被测桩服务，不依赖外网。
//
// 覆盖（对应 docs/用例集与测试计划设计.md 第 3 节）：
//   - cron 在保存时校验（6 段 / 非法 / 空表达式 / 非法时区 → 40000）
//   - 每个计划自带时区，next_fire_at 按该时区算
//   - 成员（用例集）全量替换、顺序即执行顺序、跨项目拒绝
//   - ⭐ 真实定时触发：配 `*/1 * * * *` 等它自己跑一次，验证
//     trigger_type=cron、plan_id 关联、last_fired_at 回写
//   - 禁用后不再触发
//
// ⚠️ 这一步要真的等一个调度点，因此脚本会跑 60~90 秒，是刻意的。

import http from 'node:http';

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
const STUB_PORT = Number(process.env.HRP_SMOKE_STUB_PORT || 8132);
const USER = process.env.HRP_SMOKE_USER || 'admin';
const PASS = process.env.HRP_SMOKE_PASS || 'admin123';

let pass = 0;
let fail = 0;
const failures = [];

function check(name, cond, detail = '') {
	if (cond) {
		pass++;
		console.log(`  [OK] ${name}`);
	} else {
		fail++;
		failures.push(name + (detail ? ` — ${detail}` : ''));
		console.log(`  [FAIL] ${name}${detail ? ` — ${detail}` : ''}`);
	}
}

function eq(name, actual, want) {
	check(name, actual === want, `got ${JSON.stringify(actual)}, want ${JSON.stringify(want)}`);
}

function startStub() {
	return new Promise((resolve) => {
		const srv = http.createServer((req, res) => {
			const body = JSON.stringify({ code: 0, msg: 'ok', path: req.url });
			res.writeHead(200, {
				'Content-Type': 'application/json',
				'Content-Length': Buffer.byteLength(body),
			});
			res.end(body);
		});
		srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv));
	});
}

let cookie = '';

async function call(method, path, body) {
	const res = await fetch(API + path, {
		method,
		headers: {
			'Content-Type': 'application/json',
			...(cookie ? { Cookie: cookie } : {}),
		},
		body: body === undefined ? undefined : JSON.stringify(body),
	});
	for (const c of res.headers.getSetCookie?.() || []) cookie = c.split(';')[0];
	const text = await res.text();
	let json;
	try {
		json = JSON.parse(text);
	} catch {
		json = { code: -1, message: `响应不是 JSON: ${text.slice(0, 200)}` };
	}
	return { status: res.status, body: json };
}

function data(r) {
	if (r.body.code !== 0) throw new Error(`${r.body.code} ${r.body.message}`);
	return r.body.data;
}

function caseBody(code) {
	return {
		code,
		name: '冒烟用例' + code,
		module: '冒烟',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [] },
		steps: [
			{
				seq: 1,
				step_type: 'request',
				name: '请求',
				enabled: true,
				request: { method: 'GET', url: '/echo', headers: {}, body: null, body_type: 'none' },
				extract: [],
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
			},
		],
	};
}

// 轮询直到某个条件成立
async function waitFor(name, fn, timeoutMs = 95000, intervalMs = 1000) {
	const deadline = Date.now() + timeoutMs;
	let last;
	while (Date.now() < deadline) {
		last = await fn();
		if (last) return last;
		await new Promise((r) => setTimeout(r, intervalMs));
	}
	throw new Error(`等待「${name}」超时（${timeoutMs}ms）`);
}

const stub = await startStub();
console.log(`被测桩服务已启动：http://127.0.0.1:${STUB_PORT}`);
console.log(`后端：${API}\n`);

try {
	// 1) 登录
	console.log('[1] 登录');
	eq('登录返回 code=0', (await call('POST', '/auth/login', { username: USER, password: PASS })).body.code, 0);

	// 2) 项目 / 环境 / 用例 / 用例集
	console.log('\n[2] 项目、环境、用例、用例集');
	const stamp = Date.now().toString().slice(-6);
	const proj = data(await call('POST', '/projects', {
		code: `pl${stamp}`, name: '计划冒烟项目', description: '',
	}));
	const projectID = proj.id;
	const env = data(await call('POST', `/projects/${projectID}/environments`, {
		name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
	}));
	const c1 = data(await call('POST', `/projects/${projectID}/cases`, caseBody(`pc${stamp}`)));
	const su = data(await call('POST', `/projects/${projectID}/suites`, {
		code: `ps${stamp}`, name: '冒烟用例集', on_failure: 'continue',
	}));
	data(await call('PUT', `/suites/${su.id}/cases`, { case_ids: [c1.id] }));
	check('前置数据就绪', projectID > 0 && env.id > 0 && c1.id > 0 && su.id > 0);

	// 3) cron 与时区在保存时校验
	console.log('\n[3] cron 与时区校验（保存时就拦住）');
	const bad = [
		{ name: '6 段带秒', body: { cron_expr: '0 0 9 * * *' } },
		{ name: '不是 cron', body: { cron_expr: '每天九点' } },
		{ name: '空表达式', body: { cron_expr: '' } },
		{ name: '分钟越界', body: { cron_expr: '99 * * * *' } },
		{ name: '非法时区', body: { cron_expr: '0 9 * * *', timezone: 'Asia/Beijing' } },
		{ name: '触发方式 ci', body: { cron_expr: '0 9 * * *', trigger_type: 'ci' } },
	];
	for (const c of bad) {
		const r = await call('POST', `/projects/${projectID}/plans`, {
			name: '非法' + c.name, trigger_type: 'cron', ...c.body,
		});
		eq(`${c.name} → 40000`, r.body.code, 40000);
	}
	// 手动计划不校验 cron
	const manual = data(await call('POST', `/projects/${projectID}/plans`, {
		name: '手动计划', trigger_type: 'manual',
	}));
	eq('手动计划创建成功', manual.trigger_type, 'manual');
	eq('默认时区是 Asia/Shanghai', manual.timezone, 'Asia/Shanghai');
	eq('新建计划默认不启用', manual.enabled, false);

	// 4) 建一个每分钟跑的定时计划
	console.log('\n[4] 定时计划（每分钟）');
	const plan = data(await call('POST', `/projects/${projectID}/plans`, {
		name: '每分钟回归',
		description: '冒烟用',
		env_id: env.id,
		trigger_type: 'cron',
		cron_expr: '* * * * *',
		timezone: 'Asia/Shanghai',
		enabled: true,
		suite_ids: [su.id],
	}));
	check('计划创建成功', plan.id > 0);
	eq('cron 落库', plan.cron_expr, '* * * * *');

	const view = data(await call('GET', `/plans/${plan.id}`));
	check('给出下次执行时间', !!view.next_fire_at, JSON.stringify(view.next_fire_at));
	eq('成员数', view.suite_count, 1);
	console.log(`      cron=${view.cron_expr} 人类可读=${view.cron_human || '(空)'} 时区=${view.timezone} 下次=${view.next_fire_at}`);

	// 5) 成员
	console.log('\n[5] 计划成员');
	const members = data(await call('GET', `/plans/${plan.id}/suites`));
	eq('成员一条', members.length, 1);
	eq('成员标识', members[0].suite_code, `ps${stamp}`);
	eq('成员可跑', members[0].runnable, true);
	eq('成员带用例数', members[0].case_count, 1);

	// 跨项目拒绝
	const proj2 = data(await call('POST', '/projects', { code: `pl2${stamp}`, name: '另一个项目' }));
	const su2 = data(await call('POST', `/projects/${proj2.id}/suites`, { code: 'other', name: '别的项目的用例集' }));
	const cross = await call('PUT', `/plans/${plan.id}/suites`, { suite_ids: [su.id, su2.id] });
	eq('跨项目成员 → 40000', cross.body.code, 40000);
	const after = data(await call('GET', `/plans/${plan.id}/suites`));
	eq('失败后成员没被改成半截', after.length, 1);

	// 6) ⭐ 等它自己跑一次
	console.log('\n[6] 等调度点（最多 90 秒，这是刻意的）');
	const fired = await waitFor('计划自动触发', async () => {
		const v = data(await call('GET', `/plans/${plan.id}`));
		return v.last_fired_at ? v : null;
	});
	console.log(`      last_fired_at=${fired.last_fired_at} last_run_id=${fired.last_run_id} skip=${fired.last_skip_reason || '(无)'}`);
	check('回写了 last_fired_at', !!fired.last_fired_at);
	eq('没有被记为跳过', fired.last_skip_reason, '');

	// 执行记录里要能看出"这是定时任务跑的"
	const runs = data(await call('GET', `/runs?project_id=${projectID}&target_type=suite`));
	const cronRuns = runs.list.filter((r) => r.trigger_type === 'cron');
	check('执行记录里出现 trigger_type=cron', cronRuns.length >= 1,
		`共 ${runs.total} 条，cron ${cronRuns.length} 条`);
	check('执行记录关联到计划', cronRuns.some((r) => r.plan_id === plan.id),
		JSON.stringify(cronRuns.map((r) => r.plan_id)));

	// 等这条执行跑完，确认真的跑出了结果
	const runID = fired.last_run_id;
	const done = await waitFor('执行进入终态', async () => {
		const d = data(await call('GET', `/runs/${runID}`));
		const st = d.run?.status ?? d.status;
		return st && st !== 'queued' && st !== 'running' ? d : null;
	}, 60000);
	const r = done.run;
	console.log(`      run=${runID} status=${r.status} total=${r.total} passed=${r.passed}`);
	eq('定时执行真的跑了用例', r.total, 1);
	eq('结果是通过', r.status, 'success');

	// 7) 禁用后不再触发
	console.log('\n[7] 禁用后不再触发');
	data(await call('PUT', `/plans/${plan.id}/enabled`, { enabled: false }));
	const off = data(await call('GET', `/plans/${plan.id}`));
	eq('开关已关闭', off.enabled, false);
	const firedAtBefore = off.last_fired_at;
	// 等过至少一个调度点（约 70 秒）
	await new Promise((r) => setTimeout(r, 70000));
	const still = data(await call('GET', `/plans/${plan.id}`));
	eq('禁用期间没有再触发', still.last_fired_at, firedAtBefore);

	// 8) 列表与删除
	console.log('\n[8] 列表与删除');
	const list = data(await call('GET', `/projects/${projectID}/plans`));
	check('列表能查到', list.total >= 2, `total=${list.total}`);
	const offList = data(await call('GET', `/projects/${projectID}/plans?enabled=false`));
	check('能按 enabled 过滤', offList.total >= 1, `total=${offList.total}`);
	eq('删除计划返回 code=0', (await call('DELETE', `/plans/${plan.id}`)).body.code, 0);
	eq('删除后读不到 → 40002', (await call('GET', `/plans/${plan.id}`)).body.code, 40002);
	// 计划删掉之后，它引用的用例集应该能删了
	eq('计划删除后用例集可删', (await call('DELETE', `/suites/${su.id}`)).body.code, 0);
} catch (e) {
	fail++;
	failures.push(`脚本异常：${e.message}`);
	console.log(`\n[异常] ${e.stack}`);
} finally {
	stub.close();
}

console.log(`\n===== 冒烟结果：${pass} 通过 / ${fail} 失败 =====`);
if (failures.length) {
	for (const f of failures) console.log(`  ✗ ${f}`);
	process.exit(1);
}
