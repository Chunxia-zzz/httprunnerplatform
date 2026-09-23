// 用例基线对比（M4-d）端到端冒烟：GET /projects/{id}/baseline 返回最近 N 次执行对比。
//
// 用法：node scripts/smoke-baseline.mjs
// 聚合与 Delta 信号的逐项断言在 Go 单测（baseline_test.go）里完成，
// 这里验证 HTTP 层：登录态、参数校验、返回结构、跨项目拒绝、无历史空返回。

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
const USER = process.env.HRP_SMOKE_USER || 'admin';
const PASS = process.env.HRP_SMOKE_PASS || 'admin123';

let pass = 0, fail = 0;
const failures = [];
function check(name, cond, detail = '') {
	if (cond) { pass++; console.log(`  [OK] ${name}`); }
	else { fail++; failures.push(name + (detail ? ` — ${detail}` : '')); console.log(`  [FAIL] ${name}${detail ? ` — ${detail}` : ''}`); }
}

let cookie = '';
async function call(method, path, body) {
	const res = await fetch(API + path, {
		method,
		headers: { 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) },
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
	const login = await call('POST', '/auth/login', { username: USER, password: PASS });
	check('登录成功', login.body.code === 0);

	const proj = data(await call('POST', '/projects', { code: `base${stamp}`, name: `基线冒烟 ${stamp}` }));
	data(await call('POST', `/projects/${proj.id}/environments`, {
		name: '环境A', base_url: 'http://127.0.0.1:8133', variables: {}, is_default: true,
	}));
	const tc = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tc${stamp}`, name: '基线冒烟用例', module: 'smoke', priority: 'P1', status: 'active',
		config: { verify: false, variables: {}, export: [] },
		steps: [{
			seq: 1, step_type: 'request', name: '步骤一', enabled: true,
			request: { method: 'GET', url: '/ok', headers: {}, body: null, body_type: 'none' },
			extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
		}],
	}));
	check('造数完成', !!proj.id && !!tc.id);

	// 1) 无历史：应返回空 runs。
	const empty = await call('GET', `/projects/${proj.id}/baseline?case_id=${tc.id}&limit=5`);
	check('无历史返回 code=0', empty.body.code === 0, JSON.stringify(empty.body));
	const emptyData = empty.body.data;
	check('无历史 total_runs=0', emptyData.total_runs === 0, String(emptyData.total_runs));
	check('无历史 runs 为空数组', Array.isArray(emptyData.runs) && emptyData.runs.length === 0);

	// 2) 缺 case_id：应报参数错。
	const bad = await call('GET', `/projects/${proj.id}/baseline?limit=5`);
	check('缺 case_id 返回参数错误', bad.body.code !== 0, `code=${bad.body.code}`);

	// 3) 跨项目拒绝：用另一个项目 id 查这条用例。
	const other = data(await call('POST', '/projects', { code: `basx${stamp}`, name: `基线冒烟二号 ${stamp}` }));
	const cross = await call('GET', `/projects/${other.id}/baseline?case_id=${tc.id}`);
	check('跨项目查询返回错误', cross.body.code !== 0, `code=${cross.body.code}`);

	// 4) 结构完整性：返回的字段里应带 case_code / config_name。
	check('返回带 case_code', emptyData.case_code === `tc${stamp}`, emptyData.case_code);
	check('返回带 config_name', emptyData.config_name === '基线冒烟用例', emptyData.config_name);

	console.log(`\n${pass} 项通过，${fail} 项失败`);
	if (fail) { failures.forEach((f) => console.log('  - ' + f)); process.exit(1); }
}

main().catch((e) => { console.error('脚本异常：', e); process.exit(1); });
