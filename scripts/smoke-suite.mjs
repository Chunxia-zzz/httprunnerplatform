// 用例集执行的端到端冒烟（零依赖，Node 22 自带 fetch / http）。
//
// 用法：
//   node scripts/smoke-suite.mjs                     # 默认连 http://127.0.0.1:8127
//   HRP_SMOKE_BASE=http://127.0.0.1:9000 node scripts/smoke-suite.mjs
//
// 为什么自己起一个被测桩服务，而不是打 httpbin：
//   冒烟要能在断网机器上跑，也要能确定性地造出「通过」与「失败」两种结果。
//   打公网的话，一次网络抖动就会让断言结果变得不可复现。
//
// 覆盖的执行语义（对应 docs/用例集与测试计划设计.md）：
//   - 串行执行，成员顺序即执行顺序
//   - on_failure=continue：一条失败不影响后面继续跑
//   - on_failure=abort：失败后不再跑后面的，且不报对账不一致
//   - 成员被删除 → 记为 error，标识 (已删除)
//   - 成员被禁用 → 记为 skipped，不影响整批结论
//   - 用例数对账：expected / actual / count_mismatch

import http from 'node:http';
import { spawn } from 'node:child_process';

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
const STUB_PORT = Number(process.env.HRP_SMOKE_STUB_PORT || 8131);
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

// ---------------------------------------------------------------------------
// 被测桩服务：GET /echo → 200 JSON；GET /boom → 500
// ---------------------------------------------------------------------------
function startStub() {
	return new Promise((resolve) => {
		const srv = http.createServer((req, res) => {
			const code = req.url.startsWith('/boom') ? 500 : 200;
			const body = JSON.stringify({ code: 0, msg: 'ok', path: req.url });
			res.writeHead(code, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
			res.end(body);
		});
		srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv));
	});
}

// ---------------------------------------------------------------------------
// API 客户端
// ---------------------------------------------------------------------------
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
	const setCookie = res.headers.getSetCookie?.() || [];
	for (const c of setCookie) cookie = c.split(';')[0];
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

async function waitRun(runID, timeoutMs = 120000) {
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		const r = await call('GET', `/runs/${runID}`);
		const d = r.body.data;
		if (!d) throw new Error(`执行 ${runID} 不存在`);
		const st = d.run?.status ?? d.status;
		if (st && st !== 'queued' && st !== 'running') return d;
		await new Promise((r2) => setTimeout(r2, 500));
	}
	throw new Error(`执行 ${runID} 在 ${timeoutMs}ms 内没有进入终态`);
}

// ---------------------------------------------------------------------------
// 用例构造
// ---------------------------------------------------------------------------
function caseBody(code, path, wantStatus, name, status = 'active') {
	return {
		code,
		name,
		module: '冒烟',
		priority: 'P0',
		status,
		config: { verify: false, variables: {}, export: [] },
		steps: [
			{
				seq: 1,
				step_type: 'request',
				name: '请求' + path,
				enabled: true,
				request: { method: 'GET', url: path, headers: {}, body: null, body_type: 'none' },
				extract: [],
				validate: [{ check: 'status_code', assert: 'eq', expect: wantStatus }],
			},
		],
	};
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------
const stub = await startStub();
console.log(`被测桩服务已启动：http://127.0.0.1:${STUB_PORT}`);
console.log(`后端：${API}\n`);

try {
	// 1) 登录
	console.log('[1] 登录');
	const lg = await call('POST', '/auth/login', { username: USER, password: PASS });
	eq('登录返回 code=0', lg.body.code, 0);
	const me = await call('GET', '/auth/me');
	eq('拿到当前用户', me.body.data?.username, USER);

	// 2) 项目与环境
	console.log('\n[2] 项目与环境');
	const stamp = Date.now().toString().slice(-6);
	const proj = data(await call('POST', '/projects', {
		code: `smk${stamp}`, name: '冒烟项目', description: '用例集执行冒烟',
	}));
	const projectID = proj.id;
	check('项目创建成功', projectID > 0);

	const env = data(await call('POST', `/projects/${projectID}/environments`, {
		name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
	}));
	const envID = env.id;
	check('环境创建成功', envID > 0);

	// 3) 用例：一条必然通过、一条必然失败
	console.log('\n[3] 用例');
	const cOK = data(await call('POST', `/projects/${projectID}/cases`,
		caseBody(`ok${stamp}`, '/echo', 200, '应当通过')));
	const cBad = data(await call('POST', `/projects/${projectID}/cases`,
		caseBody(`bad${stamp}`, '/echo', 500, '应当失败')));
	const cOK2 = data(await call('POST', `/projects/${projectID}/cases`,
		caseBody(`ok2${stamp}`, '/echo', 200, '应当通过2')));
	check('三条用例创建成功', cOK.id > 0 && cBad.id > 0 && cOK2.id > 0);

	// 4) 用例集 + 成员
	console.log('\n[4] 用例集与成员');
	const su = data(await call('POST', `/projects/${projectID}/suites`, {
		code: `su${stamp}`, name: '冒烟用例集', execute_mode: 'sequential', on_failure: 'continue',
	}));
	eq('默认 execute_mode=sequential', su.execute_mode, 'sequential');
	eq('默认 on_failure=continue', su.on_failure, 'continue');

	const members = data(await call('PUT', `/suites/${su.id}/cases`, { case_ids: [cOK.id, cBad.id, cOK2.id] }));
	eq('成员数', members.length, 3);
	eq('成员顺序即执行顺序', members.map((m) => m.case_code).join(','),
		[`ok${stamp}`, `bad${stamp}`, `ok2${stamp}`].join(','));
	check('成员都可跑', members.every((m) => m.runnable === true), JSON.stringify(members.map((m) => m.runnable)));
	eq('列表带成员数', data(await call('GET', `/projects/${projectID}/suites`)).list[0].case_count, 3);

	// 5) 执行：continue
	console.log('\n[5] 执行（on_failure=continue）');
	const run1 = data(await call('POST', '/runs', {
		project_id: projectID, target_type: 'suite', target_id: su.id, env_id: envID, options: {},
	}));
	check('拿到 run_id', run1.run_id > 0);
	const d1 = await waitRun(run1.run_id);
	const r1 = d1.run;
	console.log(`      status=${r1.status} total=${r1.total} passed=${r1.passed} failed=${r1.failed} error=${r1.error} expected=${r1.expected_case_count} actual=${r1.actual_case_count} mismatch=${r1.count_mismatch}`);
	eq('target_type 记为 suite', r1.target_type, 'suite');
	eq('三条都跑了', r1.total, 3);
	eq('预期 3 条', r1.expected_case_count, 3);
	eq('实际 3 条', r1.actual_case_count, 3);
	eq('对账一致', r1.count_mismatch, false);
	eq('两条通过', r1.passed, 2);
	eq('一条失败', r1.failed, 1);
	eq('整批结论是 failed', r1.status, 'failed');

	const cases1 = d1.cases || [];
	eq('用例级结果 3 条', cases1.length, 3);
	const s1 = cases1.map((c) => `${c.case_code}:${c.status}`).join(' ');
	console.log(`      ${s1}`);
	eq('第一条通过', cases1[0].status, 'pass');
	eq('第二条失败', cases1[1].status, 'fail');
	eq('第三条仍然跑了（continue 生效）', cases1[2].status, 'pass');
	check('步骤有落库', cases1.every((c) => c.step_total >= 1), JSON.stringify(cases1.map((c) => c.step_total)));

	// 步骤明细（验证 insertCaseLayers 在用例集路径下也生效）
	const detail = data(await call('GET', `/runs/${run1.run_id}/cases`));
	const anyCase = (detail.cases || detail)[0];
	check('步骤明细有落库', (anyCase?.steps || []).length >= 1,
		`steps=${(anyCase?.steps || []).length}`);

	// 6) abort：失败后不再跑后面
	console.log('\n[6] 执行（on_failure=abort）');
	const suAbort = data(await call('POST', `/projects/${projectID}/suites`, {
		code: `ab${stamp}`, name: '遇错即停', on_failure: 'abort',
	}));
	data(await call('PUT', `/suites/${suAbort.id}/cases`, { case_ids: [cBad.id, cOK.id] }));
	const run2 = data(await call('POST', '/runs', {
		project_id: projectID, target_type: 'suite', target_id: suAbort.id, env_id: envID, options: {},
	}));
	const d2 = await waitRun(run2.run_id);
	const r2 = d2.run;
	console.log(`      status=${r2.status} total=${r2.total} expected=${r2.expected_case_count} actual=${r2.actual_case_count} mismatch=${r2.count_mismatch}`);
	eq('只跑了一条就停', r2.total, 1);
	eq('expected 收缩到 1', r2.expected_case_count, 1);
	eq('actual=1', r2.actual_case_count, 1);
	eq('abort 不报对账不一致', r2.count_mismatch, false);
	eq('整批结论是 failed 而不是 error', r2.status, 'failed');

	// 7) 成员被删除 → 记为 error，标识 (已删除)，且不影响其余用例的对账
	console.log('\n[7] 成员被删除');
	const suDel = data(await call('POST', `/projects/${projectID}/suites`, {
		code: `dl${stamp}`, name: '含已删除成员', on_failure: 'continue',
	}));
	data(await call('PUT', `/suites/${suDel.id}/cases`, { case_ids: [cOK.id, cOK2.id] }));

	// ⭐ 这条规则比"已删除成员怎么记"更重要：**被引用的用例不允许删除**。
	// 用例集成员如果被悄悄删掉，用户只会看到"这次少跑了一条"却找不到原因，
	// 所以宁可在删除时就挡住。因此"已删除成员"这条分支在页面上走不到，
	// 它的覆盖放在 internal/service/run_suite_test.go 里直接操作 DB。
	const delRes = await call('DELETE', `/cases/${cOK2.id}`);
	eq('被用例集引用的用例不允许删除 → 40003', delRes.body.code, 40003);
	check('删除被拒时说明原因', String(delRes.body.message || '').includes('用例集'),
		delRes.body.message);

	const memDel = data(await call('GET', `/suites/${suDel.id}/cases`));
	eq('成员视图仍列两条', memDel.length, 2);
	check('成员都还可跑', memDel.every((m) => m.runnable === true),
		JSON.stringify(memDel.map((m) => m.runnable)));

	const run3 = data(await call('POST', '/runs', {
		project_id: projectID, target_type: 'suite', target_id: suDel.id, env_id: envID, options: {},
	}));
	const d3 = await waitRun(run3.run_id);
	const r3 = d3.run;
	console.log(`      status=${r3.status} total=${r3.total} passed=${r3.passed} error=${r3.error} expected=${r3.expected_case_count} actual=${r3.actual_case_count}`);
	eq('两条都跑了', r3.total, 2);
	eq('expected=2', r3.expected_case_count, 2);
	eq('actual=2', r3.actual_case_count, 2);
	eq('对账一致', r3.count_mismatch, false);
	eq('整批结论是 success', r3.status, 'success');

	// 移出用例集之后才允许删除 —— 保护的是"引用"而不是"用例本身"。
	// 注意 cOK2 同时还是第 5 步那个用例集的成员，两处都要移出。
	data(await call('PUT', `/suites/${suDel.id}/cases`, { case_ids: [cOK.id] }));
	data(await call('PUT', `/suites/${su.id}/cases`, { case_ids: [cOK.id, cBad.id] }));
	eq('所有引用都移出后可以删除', (await call('DELETE', `/cases/${cOK2.id}`)).body.code, 0);

	// 8) 成员被禁用 → 记为 skipped，整批结论不受影响
	console.log('\n[8] 成员被禁用');
	const suSkip = data(await call('POST', `/projects/${projectID}/suites`, {
		code: `sk${stamp}`, name: '含已禁用成员', on_failure: 'continue',
	}));
	data(await call('PUT', `/suites/${suSkip.id}/cases`, { case_ids: [cOK.id, cBad.id] }));
	const dis = await call('PUT', `/cases/${cBad.id}`,
		caseBody(`bad${stamp}`, '/echo', 500, '应当失败', 'disabled'));
	eq('禁用用例返回 code=0', dis.body.code, 0);

	const memSkip = data(await call('GET', `/suites/${suSkip.id}/cases`));
	eq('已禁用的成员标为不可跑', memSkip[1].runnable, false);

	const run4 = data(await call('POST', '/runs', {
		project_id: projectID, target_type: 'suite', target_id: suSkip.id, env_id: envID, options: {},
	}));
	const d4 = await waitRun(run4.run_id);
	const r4 = d4.run;
	console.log(`      status=${r4.status} total=${r4.total} passed=${r4.passed} skipped=${r4.skipped} expected=${r4.expected_case_count} actual=${r4.actual_case_count}`);
	eq('结果两条', r4.total, 2);
	eq('一条通过', r4.passed, 1);
	eq('一条跳过', r4.skipped, 1);
	eq('expected 只算能跑的那条', r4.expected_case_count, 1);
	eq('actual=1', r4.actual_case_count, 1);
	eq('禁用不触发对账不一致', r4.count_mismatch, false);
	eq('整批结论是 success', r4.status, 'success');
	const cases4 = d4.cases || [];
	check('有一条记为 skipped', cases4.some((c) => c.status === 'skipped'),
		JSON.stringify(cases4.map((c) => `${c.case_code}:${c.status}`)));

	// 9) 参数校验
	console.log('\n[9] 入参校验');
	const bad = await call('POST', '/runs', {
		project_id: projectID, target_type: 'suite', target_id: 999999, options: {},
	});
	eq('用例集不存在 → 40002', bad.body.code, 40002);
	const badType = await call('POST', '/runs', {
		project_id: projectID, target_type: 'plan', target_id: su.id, options: {},
	});
	eq('target_type=plan 暂不支持 → 40000', badType.body.code, 40000);

	// 10) 执行列表能按 suite 过滤
	console.log('\n[10] 执行列表');
	const runs = data(await call('GET', `/runs?project_id=${projectID}&target_type=suite`));
	check('能按 target_type=suite 过滤', runs.total >= 2, `total=${runs.total}`);
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
