// 导出功能（M4-c）端到端冒烟：GET /projects/{id}/export 返回合法 zip。
//
// 用法：node scripts/smoke-export.mjs
// zip 内容的逐文件断言在 Go 单测（exportsvc_test.go）里完成，
// 这里验证 HTTP 层：状态码 / Content-Type / 附件名 / zip 魔数。

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

	const proj = data(await call('POST', '/projects', { code: `expx${stamp}`, name: `导出冒烟 ${stamp}` }));
	data(await call('POST', `/projects/${proj.id}/environments`, {
		name: '环境A', base_url: 'http://127.0.0.1:8133', variables: {}, is_default: true,
	}));
	const tc = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tc${stamp}`, name: '导出冒烟用例', module: 'smoke', priority: 'P1', status: 'active',
		config: { verify: false, variables: {}, export: [] },
		steps: [{
			seq: 1, step_type: 'request', name: '步骤一', enabled: true,
			request: { method: 'GET', url: '/ok', headers: {}, body: null, body_type: 'none' },
			extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
		}],
	}));
	check('造数完成', !!proj.id && !!tc.id);

	const res = await fetch(`${API}/projects/${proj.id}/export`, { headers: { Cookie: cookie } });
	check('HTTP 200', res.status === 200, String(res.status));
	check('Content-Type 是 zip', (res.headers.get('content-type') || '').includes('zip'), res.headers.get('content-type'));
	const cd = res.headers.get('content-disposition') || '';
	check('带附件名', cd.includes('attachment; filename=') && cd.includes(`expx${stamp}-export-`), cd);
	const buf = Buffer.from(await res.arrayBuffer());
	check('zip 魔数 PK', buf.length > 100 && buf[0] === 0x50 && buf[1] === 0x4b, `size=${buf.length}`);
	check('包大小合理（> 500B）', buf.length > 500, `size=${buf.length}`);

	// 错误路径：不存在的项目
	const res2 = await fetch(`${API}/projects/999999999/export`, { headers: { Cookie: cookie } });
	check('不存在的项目返回 JSON 错误', res2.headers.get('content-type').includes('json') && res2.status >= 400,
		`${res2.status} ${res2.headers.get('content-type')}`);

	console.log(`\n${pass} 项通过，${fail} 项失败`);
	if (fail) { failures.forEach((f) => console.log('  - ' + f)); process.exit(1); }
}

main().catch((e) => { console.error('脚本异常：', e); process.exit(1); });
