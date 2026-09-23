// 参数化（M3 ③-b）端到端冒烟：数据集 CRUD + 编译转换 + 真实引擎迭代。
//
// 用法：
//   node scripts/smoke-param.mjs                     # 默认连 http://127.0.0.1:8127
//
// 覆盖（对应 docs/M3调试体验设计.md 验收 ②）：
//   - list 数据集：行式数据 → 关联参数（成对，不是笛卡尔积）
//   - csv 数据集：${P()} 引用 + limit 派生文件
//   - limit 在数据集与引用两级生效
//   - 引用不存在的数据集 → 编译期明确报错
//   - 真实执行：CSV 3 行 → 3 次迭代（对账 step_total）
//   - 派生 CSV 确实写进了运行工作区（执行成功即为证）

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

// 桩：回显查询参数。
function startStub() {
	return new Promise((resolve) => {
		const srv = http.createServer((req, res) => {
			const body = JSON.stringify({ code: 0, path: req.url });
			res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
			res.end(body);
		});
		srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv));
	});
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
	const stub = await startStub();
	const login = await call('POST', '/auth/login', { username: USER, password: PASS });
	check('登录成功', login.body.code === 0);

	const proj = data(await call('POST', '/projects', { code: `pm${stamp}`, name: '参数化验收项目' }));
	const env = data(await call('POST', `/projects/${proj.id}/environments`, {
		name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
	}));

	// =========================================================================
	console.log('\n== list 数据集：CRUD ==');
	// =========================================================================
	const dsList = data(await call('POST', `/projects/${proj.id}/datasets`, {
		name: '用户数据',
		source: 'list',
		inline: [{ username: 'alice', password: 'pw1' }, { username: 'bob', password: 'pw2' }],
		strategy: 'sequential',
		limit: 0,
	}));
	check('建 list 数据集成功', !!dsList?.id, JSON.stringify(dsList));
	check('row_count = 2', dsList.row_count === 2, JSON.stringify(dsList));
	check('columns 按字典序', JSON.stringify(dsList.columns) === JSON.stringify(['password', 'username']), JSON.stringify(dsList.columns));
	check('limit_effective = 2', dsList.limit_effective === 2, String(dsList.limit_effective));

	const dup = await call('POST', `/projects/${proj.id}/datasets`, {
		name: '用户数据', source: 'list', inline: [{ a: 1 }],
	});
	check('同项目重名被拒（40001）', dup.body.code === 40001, `code=${dup.body.code}`);

	const badSource = await call('POST', `/projects/${proj.id}/datasets`, {
		name: 'x', source: 'random_data', inline: [{ a: 1 }],
	});
	check('非法 source 被拒', badSource.body.code !== 0, `code=${badSource.body.code}`);

	const emptyInline = await call('POST', `/projects/${proj.id}/datasets`, {
		name: 'y', source: 'list', inline: [],
	});
	check('空 inline 被拒', emptyInline.body.code !== 0, `code=${emptyInline.body.code}`);

	// =========================================================================
	console.log('\n== csv 数据集：上传 + 行数统计 ==');
	// =========================================================================
	const csvText = 'username,password\nalice,pw1\nbob,pw2\ncarol,pw3\n';
	const dsCsv = data(await call('POST', `/projects/${proj.id}/datasets`, {
		name: 'CSV用户',
		source: 'csv',
		csv_name: `users_${stamp}.csv`,
		csv_text: csvText,
		strategy: 'sequential',
		limit: 0,
	}));
	check('建 csv 数据集成功', !!dsCsv?.id, JSON.stringify(dsCsv));
	check('csv row_count = 3', dsCsv.row_count === 3, JSON.stringify(dsCsv));
	check('csv columns 正确', JSON.stringify(dsCsv.columns) === JSON.stringify(['username', 'password']), JSON.stringify(dsCsv.columns));

	const csvBack = data(await call('GET', `/datasets/${dsCsv.id}/csv`));
	check('CSV 内容可读回', csvBack.csv_text === csvText, JSON.stringify(csvBack).slice(0, 120));

	const badCsv = await call('POST', `/projects/${proj.id}/datasets`, {
		name: '坏CSV', source: 'csv', csv_name: 'b.csv', csv_text: 'a,b\n1\n',
	});
	check('列数不一致的 CSV 被拒', badCsv.body.code !== 0, `code=${badCsv.body.code}`);

	const evil = await call('POST', `/projects/${proj.id}/datasets`, {
		name: '穿越', source: 'csv', csv_name: '../evil.csv', csv_text: csvText,
	});
	check('路径穿越文件名被拒', evil.body.code !== 0, `code=${evil.body.code}`);

	// =========================================================================
	console.log('\n== 编译转换：list 数据集 → 关联 pairs ==');
	// =========================================================================
	const tcList = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tcl${stamp}`,
		name: 'list参数化用例',
		module: 'pm',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [], datasets: [{ name: '用户数据', limit: 0 }] },
		steps: [
			{
				seq: 1, step_type: 'request', name: '请求', enabled: true,
				request: { method: 'GET', url: '/get', params: { u: '$username', p: '$password' } },
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
			},
		],
	}));
	const y1 = data(await call('GET', `/cases/${tcList.id}/yaml?env_id=${env.id}`));
	check('YAML 含关联参数键', y1.yaml.includes('password-username:') || y1.yaml.includes('username-password:'), y1.yaml.slice(0, 400));
	check('YAML 不含平台保留键 datasets', !y1.yaml.includes('datasets'), y1.yaml.slice(0, 400));
	check('YAML 含数据行 alice', y1.yaml.includes('alice'), '');

	// =========================================================================
	console.log('\n== 编译转换：csv 数据集 → ${P()} 引用 ==');
	// =========================================================================
	const tcCsv = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tcc${stamp}`,
		name: 'csv参数化用例',
		module: 'pm',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [], datasets: [{ name: 'CSV用户', limit: 0 }] },
		steps: [
			{
				seq: 1, step_type: 'request', name: '请求', enabled: true,
				request: { method: 'GET', url: '/get', params: { u: '$username' } },
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
			},
		],
	}));
	const y2 = data(await call('GET', `/cases/${tcCsv.id}/yaml?env_id=${env.id}`));
	check('YAML 含 ${P(data/...)} 引用', y2.yaml.includes('${P(data/'), y2.yaml.slice(0, 400));

	// limit=2 → 派生文件
	const tcCsvLim = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tclim${stamp}`,
		name: 'csv参数化limit用例',
		module: 'pm',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [], datasets: [{ name: 'CSV用户', limit: 2 }] },
		steps: [
			{
				seq: 1, step_type: 'request', name: '请求', enabled: true,
				request: { method: 'GET', url: '/get', params: { u: '$username' } },
				validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
			},
		],
	}));
	const y3 = data(await call('GET', `/cases/${tcCsvLim.id}/yaml?env_id=${env.id}`));
	check('limit 引用指向派生文件 __l2', y3.yaml.includes('__l2.csv'), y3.yaml.slice(0, 400));

	// =========================================================================
	console.log('\n== 引用不存在的数据集 → 编译期报错 ==');
	// =========================================================================
	const tcGhost = data(await call('POST', `/projects/${proj.id}/cases`, {
		code: `tcg${stamp}`,
		name: '幽灵引用用例',
		module: 'pm',
		priority: 'P0',
		status: 'active',
		config: { verify: false, variables: {}, export: [], datasets: [{ name: '不存在的数据集', limit: 0 }] },
		steps: [
			{
				seq: 1, step_type: 'request', name: '请求', enabled: true,
				request: { method: 'GET', url: '/get' },
			},
		],
	}));
	check('保存成功（保存侧不做数据集存在性校验）', !!tcGhost.id);
	const yGhost = await call('GET', `/cases/${tcGhost.id}/yaml?env_id=${env.id}`);
	check('YAML 预览时报数据集不存在（50003）', yGhost.body.code === 50003, `code=${yGhost.body.code} msg=${yGhost.body.message?.slice(0, 80)}`);
	const vGhost = await call('POST', `/cases/${tcGhost.id}/validate?env_id=${env.id}`);
	check('静态校验也报（50004 或 50003）', vGhost.body.code === 50004 || vGhost.body.code === 50003, `code=${vGhost.body.code}`);

	// =========================================================================
	console.log('\n== ⭐ 真实执行：CSV 3 行 → 3 次迭代 ==');
	// =========================================================================
	const run = data(await call('POST', '/runs', {
		project_id: proj.id, target_type: 'case', target_id: tcCsv.id, env_id: env.id, options: {},
	}));
	const runId = run.run_id;
	let runRow = null;
	let runCases = null;
	for (let i = 0; i < 40; i++) {
		await new Promise((r) => setTimeout(r, 500));
		const d = data(await call('GET', `/runs/${runId}`));
		runRow = d.run;
		runCases = d.cases;
		if (['success', 'failed', 'error', 'canceled'].includes(runRow.status)) break;
	}
	// 参数化迭代统计在 case result 层（cases[0]），不在 run 层 —— 见 CaseResultView。
	const case0 = runCases?.[0];
	check('CSV 参数化执行完成', runRow?.status === 'success', JSON.stringify(runRow));
	check('⭐ step_total = 3（3 行数据全迭代）', case0?.step_total === 3, `实际 ${case0?.step_total}`);
	check('step_passed = 3', case0?.step_passed === 3, `实际 ${case0?.step_passed}`);

	// limit=2 的用例 → 2 次迭代
	const run2 = data(await call('POST', '/runs', {
		project_id: proj.id, target_type: 'case', target_id: tcCsvLim.id, env_id: env.id, options: {},
	}));
	let runRow2 = null;
	let runCases2 = null;
	for (let i = 0; i < 40; i++) {
		await new Promise((r) => setTimeout(r, 500));
		const d = data(await call('GET', `/runs/${run2.run_id}`));
		runRow2 = d.run;
		runCases2 = d.cases;
		if (['success', 'failed', 'error', 'canceled'].includes(runRow2.status)) break;
	}
	const case0_2 = runCases2?.[0];
	check('⭐ limit=2 时 step_total = 2', runRow2?.status === 'success' && case0_2?.step_total === 2,
		`status=${runRow2?.status} step_total=${case0_2?.step_total}`);

	// =========================================================================
	console.log('\n== 删除数据集 ==');
	// =========================================================================
	const del = await call('DELETE', `/datasets/${dsList.id}`);
	check('删除成功', del.body.code === 0);
	const after = await call('GET', `/datasets/${dsList.id}`);
	check('删除后查询 40002', after.body.code === 40002, `code=${after.body.code}`);

	stub.close();
	console.log(`\n===== ${pass} 项通过，${fail} 项失败 =====`);
	failures.forEach((f) => console.log(`  - ${f}`));
	process.exit(fail ? 1 : 0);
}

main().catch((e) => {
	console.error('冒烟脚本异常:', e);
	process.exit(1);
});
