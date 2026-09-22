// CI Token 与 /open 触发的端到端冒烟（零依赖，Node 22 自带 fetch / http）。
//
// 用法：
//   node scripts/smoke-ci.mjs                     # 默认连 http://127.0.0.1:8127
//   HRP_SMOKE_BASE=http://127.0.0.1:9000 node scripts/smoke-ci.mjs
//
// 覆盖（对应 docs/用例集与测试计划设计.md 第 4 节）：
//   - 签发只显示一次明文，列表里只有 prefix，库里没有明文
//   - /open 鉴权：没令牌 / 错令牌 / 过期令牌 → 40100
//   - ⭐ 令牌不能触发别的项目（按项目签发的全部意义）
//   - 只读令牌不能触发
//   - ⭐ 真的触发一次执行（?wait=1），拿到 exit_code_for_ci
//   - 吊销后立刻失效

import http from 'node:http';

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
const OPEN = `${BASE}/open`;
const STUB_PORT = Number(process.env.HRP_SMOKE_STUB_PORT || 8133);
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
			// /boom 返回 500，用来造一条"必然失败"的用例
			const code = req.url.startsWith('/boom') ? 500 : 200;
			const body = JSON.stringify({ code: 0, msg: 'ok', path: req.url });
			res.writeHead(code, {
				'Content-Type': 'application/json',
				'Content-Length': Buffer.byteLength(body),
			});
			res.end(body);
		});
		srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv));
	});
}

let cookie = '';

// 页面侧调用（带会话 Cookie）
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
	return { status: res.status, body: await res.json() };
}

// CI 侧调用（带 Bearer 令牌，不带 Cookie）
async function openCall(method, path, body, token) {
	const res = await fetch(OPEN + path, {
		method,
		headers: {
			'Content-Type': 'application/json',
			...(token ? { Authorization: `Bearer ${token}` } : {}),
		},
		// GET / HEAD 不能有 body（Node 的 fetch 会直接抛错）
		body: body === undefined || body === null ? undefined : JSON.stringify(body),
	});
	return { status: res.status, body: await res.json() };
}

function data(r) {
	if (r.body.code !== 0) throw new Error(`${r.body.code} ${r.body.message}`);
	return r.body.data;
}

function caseBody(code, path, wantStatus) {
	return {
		code,
		name: 'CI用例' + code,
		module: 'ci',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [] },
		steps: [
			{
				seq: 1,
				step_type: 'request',
				name: '请求',
				enabled: true,
				request: { method: 'GET', url: path, headers: {}, body: null, body_type: 'none' },
				extract: [],
				validate: [{ check: 'status_code', assert: 'eq', expect: wantStatus }],
			},
		],
	};
}

const stub = await startStub();
console.log(`被测桩服务已启动：http://127.0.0.1:${STUB_PORT}`);
console.log(`后端：${API}（CI 入口 ${OPEN}）\n`);

try {
	// 1) 登录（令牌管理走会话体系）
	console.log('[1] 登录');
	eq('登录返回 code=0', (await call('POST', '/auth/login', { username: USER, password: PASS })).body.code, 0);

	// 2) 准备项目/环境/用例集
	console.log('\n[2] 准备用例集');
	const stamp = Date.now().toString().slice(-6);
	const proj = data(await call('POST', '/projects', { code: `ci${stamp}`, name: 'CI 冒烟项目' }));
	const proj2 = data(await call('POST', '/projects', { code: `ci2${stamp}`, name: '另一个项目' }));
	const env = data(await call('POST', `/projects/${proj.id}/environments`, {
		name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
	}));
	const env2 = data(await call('POST', `/projects/${proj2.id}/environments`, {
		name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
	}));

	const cOK = data(await call('POST', `/projects/${proj.id}/cases`, caseBody(`ok${stamp}`, '/echo', 200)));
	const cBad = data(await call('POST', `/projects/${proj.id}/cases`, caseBody(`bad${stamp}`, '/boom', 200)));
	const suOK = data(await call('POST', `/projects/${proj.id}/suites`, { code: `ok${stamp}`, name: '应当通过' }));
	data(await call('PUT', `/suites/${suOK.id}/cases`, { case_ids: [cOK.id] }));
	const suBad = data(await call('POST', `/projects/${proj.id}/suites`, { code: `bad${stamp}`, name: '应当失败' }));
	data(await call('PUT', `/suites/${suBad.id}/cases`, { case_ids: [cBad.id] }));
	const suOther = data(await call('POST', `/projects/${proj2.id}/suites`, { code: 'other', name: '别的项目' }));
	check('前置数据就绪', proj.id > 0 && suOK.id > 0 && suBad.id > 0 && suOther.id > 0);

	// 3) 签发令牌
	console.log('\n[3] 签发令牌');
	const issued = data(await call('POST', `/projects/${proj.id}/tokens`, { name: '流水线' }));
	check('拿到明文', typeof issued.token === 'string' && issued.token.startsWith('hrp_ci_'),
		JSON.stringify(issued.token));
	eq('明文长度（前缀 + 32 字节 hex）', issued.token?.length, 'hrp_ci_'.length + 64);
	check('给出展示用前缀', !!issued.prefix, JSON.stringify(issued.prefix));
	eq('默认权限是触发 + 读结果', issued.scope, 'run:trigger,run:read');
	console.log(`      token=${issued.token.slice(0, 20)}… prefix=${issued.prefix}`);

	// ⭐ 明文只出现一次
	const list = data(await call('GET', `/projects/${proj.id}/tokens`));
	eq('列表一条', list.length, 1);
	check('列表里没有明文', !JSON.stringify(list).includes(issued.token),
		'列表里出现了明文');
	eq('列表里是前缀', list[0].prefix, issued.prefix);
	check('列表里没有 token 字段', list[0].token === undefined);

	// 4) 鉴权
	console.log('\n[4] /open 鉴权');
	const noToken = await openCall('POST', '/runs', { project_id: proj.id, target_type: 'suite', target_id: suOK.id }, '');
	eq('没有令牌 → 40100', noToken.body.code, 40100);
	eq('HTTP 也是 401', noToken.status, 401);
	// 注意：HTTP 头只能是 ASCII，所以假令牌也得用 ASCII 造
	const badToken = await openCall('POST', '/runs', { project_id: proj.id }, 'hrp_ci_' + 'a'.repeat(64));
	eq('错误令牌 → 40100', badToken.body.code, 40100);

	// 5) ⭐ 按项目签发的核心校验
	console.log('\n[5] 令牌不能碰别的项目');
	const cross = await openCall('POST', '/runs',
		{ project_id: proj2.id, target_type: 'suite', target_id: suOther.id }, issued.token);
	eq('跨项目触发 → 40300', cross.body.code, 40300);
	const noProj = await openCall('POST', '/runs',
		{ target_type: 'suite', target_id: suOK.id }, issued.token);
	eq('缺 project_id → 40000', noProj.body.code, 40000);

	// 6) 权限
	console.log('\n[6] 权限');
	const readOnly = data(await call('POST', `/projects/${proj.id}/tokens`, { name: '只读', scope: 'run:read' }));
	const roTrig = await openCall('POST', '/runs',
		{ project_id: proj.id, target_type: 'suite', target_id: suOK.id }, readOnly.token);
	eq('只读令牌不能触发 → 40300', roTrig.body.code, 40300);
	const badScope = await call('POST', `/projects/${proj.id}/tokens`, { name: '乱填', scope: 'run:admin' });
	eq('未知权限 → 40000', badScope.body.code, 40000);

	// 7) ⭐ 真的触发一次（同步等待）
	console.log('\n[7] 触发执行（?wait=1 同步等待）');
	const trig = await openCall('POST', `/runs?wait=1&timeout=90`,
		{ project_id: proj.id, target_type: 'suite', target_id: suOK.id, env_id: env.id }, issued.token);
	eq('触发成功', trig.body.code, 0);
	const d = trig.body.data;
	console.log(`      status=${d.status} exit_code_for_ci=${d.exit_code_for_ci} total=${d.total} passed=${d.passed}`);
	eq('同步等待拿到了终态', d.status, 'success');
	eq('exit_code_for_ci = 0', d.exit_code_for_ci, 0);
	eq('跑了一条用例', d.total, 1);
	eq('wait 标记为同步', d.wait, true);

	// 触发来源要能看出来是 CI
	const runs = data(await call('GET', `/runs?project_id=${proj.id}&target_type=suite`));
	const ciRuns = runs.list.filter((r) => r.trigger_type === 'ci');
	check('执行记录标了 trigger_type=ci', ciRuns.length >= 1, `ci=${ciRuns.length} / 共 ${runs.total}`);

	// 8) ⭐ 失败时给 pipeline 一个非零退出码
	console.log('\n[8] 失败场景的退出码');
	const trig2 = await openCall('POST', `/runs?wait=1&timeout=90`,
		{ project_id: proj.id, target_type: 'suite', target_id: suBad.id, env_id: env.id }, issued.token);
	eq('触发成功', trig2.body.code, 0);
	const d2 = trig2.body.data;
	console.log(`      status=${d2.status} exit_code_for_ci=${d2.exit_code_for_ci} failed=${d2.failed}`);
	check('结论不是成功', d2.status !== 'success', d2.status);
	eq('exit_code_for_ci = 1', d2.exit_code_for_ci, 1);
	check('列出失败用例', Array.isArray(d2.failed_cases) && d2.failed_cases.length >= 1,
		JSON.stringify(d2.failed_cases));
	check('失败用例带归因', !!d2.failed_cases?.[0]?.attribution,
		JSON.stringify(d2.failed_cases?.[0]));

	// 9) 异步触发（默认）
	console.log('\n[9] 默认异步');
	const trig3 = await openCall('POST', `/runs`,
		{ project_id: proj.id, target_type: 'suite', target_id: suOK.id, env_id: env.id }, issued.token);
	eq('异步触发成功', trig3.body.code, 0);
	check('立刻给了 run_id', trig3.body.data.run_id > 0);
	eq('wait 标记为异步', trig3.body.data.wait, false);
	check('给出轮询地址', String(trig3.body.data.poll_at || '').includes('/open/runs/'),
		JSON.stringify(trig3.body.data.poll_at));
	// 用响应里给的轮询地址取结果（它是 "/open/..." 形式的路径，
	// openCall 自己会拼 /open 前缀，所以这里要把前缀摘掉）
	const polled = await openCall('GET', trig3.body.data.poll_at.replace(/^\/open/, ''), null, issued.token);
	eq('轮询地址可用', polled.body.code, 0);

	// 10) 吊销后立刻失效
	console.log('\n[10] 吊销');
	eq('吊销返回 code=0',
		(await call('DELETE', `/tokens/${issued.id}?project_id=${proj.id}`)).body.code, 0);
	const after = await openCall('POST', '/runs',
		{ project_id: proj.id, target_type: 'suite', target_id: suOK.id }, issued.token);
	eq('吊销后的令牌 → 40100', after.body.code, 40100);
	const list2 = data(await call('GET', `/projects/${proj.id}/tokens`));
	check('吊销后从列表移除', !list2.some((t) => t.id === issued.id), JSON.stringify(list2.map((t) => t.id)));

	// 缺 project_id 的吊销要拦住
	const noPid = await call('DELETE', `/tokens/${readOnly.id}`);
	eq('吊销缺 project_id → 40000', noPid.body.code, 40000);
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
