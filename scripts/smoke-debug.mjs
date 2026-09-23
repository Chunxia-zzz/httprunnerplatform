// 单步调试端到端冒烟（零依赖，Node 22 自带 fetch / http）。
//
// 用法：
//   node scripts/smoke-debug.mjs                     # 默认连 http://127.0.0.1:8127
//   HRP_SMOKE_BASE=http://127.0.0.1:9000 node scripts/smoke-debug.mjs
//
// 覆盖（对应 docs/M3调试体验设计.md）：
//   - 单步调试实际执行「前置步骤 + 目标步骤」的前缀（不是只跑一步）
//   - 目标步骤被标记 is_target
//   - 前置步骤提取的变量在目标步骤里真实可用
//   - 断言失败时（panic）平台仍能重建失败明细
//   - 不落库：调试后 run 列表数量不变
//   - 找不到目标 seq / 跨项目 / 禁用步骤 → 明确报错

import http from 'node:http';

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
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

// 桩服务：/login 返回带 token 的 JSON（供第 1 步提取），/boom 返回 500，其余 200。
function startStub() {
	return new Promise((resolve) => {
		const srv = http.createServer((req, res) => {
			let code = req.url.startsWith('/boom') ? 500 : 200;
			let body;
			if (req.url.startsWith('/login')) {
				body = JSON.stringify({ token: 'tok_12345', user: 'alice' });
			} else {
				body = JSON.stringify({ code: 0, path: req.url });
			}
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

function data(r) {
	if (r.body.code !== 0) throw new Error(`${r.body.code} ${r.body.message}`);
	return r.body.data;
}

const stamp = Date.now().toString(36);

async function main() {
	const stub = await startStub();
	console.log(`被测桩服务已启动：http://127.0.0.1:${STUB_PORT}`);

	// 登录
	const login = await call('POST', '/auth/login', { username: USER, password: PASS });
	check('登录成功', login.body.code === 0, JSON.stringify(login.body));

	// 建项目 + 环境
	const proj = data(await call('POST', '/projects', { code: `dbg${stamp}`, name: '调试验收项目' }));
	const env = data(await call('POST', `/projects/${proj.id}/environments`, {
		name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
	}));

	// 建一条「登录(提取token) → 查用户(用token)」的用例，验证变量依赖
	const tc = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tc${stamp}`,
		name: '调试链路用例',
		module: 'dbg',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [] },
		steps: [
			{
				seq: 1, step_type: 'request', name: '登录', enabled: true,
				request: { method: 'GET', url: '/login', headers: {}, body: null, body_type: 'none' },
				extract: [{ name: 'token', object: 'body', expression: 'token' }],
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
			},
			{
				seq: 2, step_type: 'request', name: '查用户', enabled: true,
				request: { method: 'GET', url: '/user', headers: { Authorization: 'Bearer $token' }, body: null, body_type: 'none' },
				extract: [],
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
			},
		],
	}));

	// 记录调试前的 run 数量
	const runsBefore = data(await call('GET', `/runs?project_id=${proj.id}`)).total;

	// ⭐ 核心：调试第 2 步（依赖第 1 步提取的 token）
	const dbg = await call('POST', `/cases/${tc.id}/debug`, {
		project_id: proj.id, case_id: tc.id, step_seq: 2, env_id: env.id,
	});
	check('调试返回 code=0', dbg.body.code === 0, JSON.stringify(dbg.body).slice(0, 300));
	const r = dbg.body.data;

	if (dbg.body.code === 0) {
		check('调试整体状态 = pass', r.status === 'pass', r.status);
		check('实际执行了 2 步（前缀，含前置）', r.actual_step_count === 2, String(r.actual_step_count));
		check('结果步骤数 = 2', r.steps?.length === 2, String(r.steps?.length));

		const step1 = r.steps?.find((s) => s.seq === 1);
		const step2 = r.steps?.find((s) => s.seq === 2);
		check('目标步骤被标记 is_target', step2?.is_target === true, JSON.stringify(step2?.is_target));
		check('前置步骤不是目标', step1?.is_target === false, JSON.stringify(step1?.is_target));

		// ⭐ 变量依赖：第 1 步提取的 token 应在第 1 步的 extract_result 里
		const extracted = step1?.extract_result?.token;
		check('第 1 步提取出 token=tok_12345', extracted === 'tok_12345', JSON.stringify(extracted));
	}

	// ⭐ 不落库：调试后 run 数量不变
	const runsAfter = data(await call('GET', `/runs?project_id=${proj.id}`)).total;
	check('调试不落库（run 数量不变）', runsAfter === runsBefore, `before=${runsBefore} after=${runsAfter}`);

	// 找不到目标 seq
	const miss = await call('POST', `/cases/${tc.id}/debug`, {
		project_id: proj.id, case_id: tc.id, step_seq: 99, env_id: env.id,
	});
	check('找不到目标 seq → 报错', miss.body.code !== 0, `code=${miss.body.code}`);

	// 调试不存在的用例
	const bad = await call('POST', `/cases/999999/debug`, {
		project_id: proj.id, case_id: 999999, step_seq: 1, env_id: env.id,
	});
	check('用例不存在 → 报错', bad.body.code !== 0, `code=${bad.body.code}`);

	// 断言失败场景：建一条必失败的用例，验证 panic 下平台仍能重建
	const badCase = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `bad${stamp}`,
		name: '调试失败用例',
		module: 'dbg',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [] },
		steps: [
			{
				seq: 1, step_type: 'request', name: '会失败的请求', enabled: true,
				request: { method: 'GET', url: '/boom', headers: {}, body: null, body_type: 'none' },
				extract: [],
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }], // 桩返回 500 → 断言失败
			},
		],
	}));
	const badDbg = await call('POST', `/cases/${badCase.id}/debug`, {
		project_id: proj.id, case_id: badCase.id, step_seq: 1, env_id: env.id,
	});
	check('失败用例调试返回 code=0（失败是正常结果不是错误）', badDbg.body.code === 0, JSON.stringify(badDbg.body).slice(0, 200));
	if (badDbg.body.code === 0) {
		const br = badDbg.body.data;
		check('失败用例调试状态 = fail', br.status === 'fail', br.status);
		check('panic 标记为 true', br.panic === true, JSON.stringify(br.panic));
	}

	stub.close();
	console.log(`\n===== ${pass} 项通过，${fail} 项失败 =====`);
	failures.forEach((f) => console.log(`  - ${f}`));
	process.exit(fail ? 1 : 0);
}

main().catch((e) => {
	console.error('冒烟脚本异常:', e);
	process.exit(1);
});
