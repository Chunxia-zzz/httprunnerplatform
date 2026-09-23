// 导入功能（M4-b）端到端冒烟：multipart 上传 → 预览 → 提交 → 重名去重。
//
// 用法：node scripts/smoke-import.mjs
// 前置：后端 8127 在跑（不需要引擎——转换由后端调 hrp convert 完成，
//       本机 hrp 由 hrpclient.Resolve 定位；找不到时 preview 会报错，
//       此时本脚本无法进行，属环境问题）。
//
// 覆盖（对应 M4 验收 ②「能把 Postman 集合批量导入成用例」的 HAR/curl 等价路径）：
//   - HAR 自动识别 + 预览（不落库）
//   - 提交落库 + 断言被解析（字典形态 validate）
//   - 同名文件再导一次 → 自动加后缀，不撞唯一约束
//   - curl 格式识别与导入
//   - 未知格式 / 空文件明确报错

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
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

async function upload(path, filename, content, format) {
	const fd = new FormData();
	fd.append('file', new Blob([content]), filename);
	if (format) fd.append('format', format);
	const res = await fetch(API + path, {
		method: 'POST',
		headers: cookie ? { Cookie: cookie } : {},
		body: fd,
	});
	for (const c of res.headers.getSetCookie?.() || []) cookie = c.split(';')[0];
	return { status: res.status, body: await res.json() };
}

const HAR = JSON.stringify({
	log: {
		version: '1.2',
		creator: { name: 'probe', version: '1' },
		entries: [
			{
				startedDateTime: '2026-09-23T16:00:00+08:00',
				time: 100,
				request: {
					method: 'GET',
					url: 'http://127.0.0.1:8133/login?u=alice',
					httpVersion: 'HTTP/1.1',
					headers: [{ name: 'Authorization', value: 'Bearer tok' }],
					queryString: [{ name: 'u', value: 'alice' }],
					cookies: [],
					headersSize: 60,
					bodySize: 0,
				},
				response: {
					status: 200,
					statusText: 'OK',
					httpVersion: 'HTTP/1.1',
					headers: [],
					cookies: [],
					content: { size: 30, mimeType: 'application/json', text: '{"token":"tok_12345"}' },
					redirectURL: '',
					headersSize: 50,
					bodySize: 30,
				},
				cache: {},
				timings: { send: 1, wait: 90, receive: 9 },
			},
		],
	},
});

const stamp = Date.now().toString(36);

async function main() {
	const login = await call('POST', '/auth/login', { username: USER, password: PASS });
	check('登录成功', login.body.code === 0);

	const proj = data(await call('POST', '/projects', { code: `imp${stamp}`, name: `导入验收 ${stamp}` }));

	// ① 预览（auto 识别 HAR），不落库
	const preview = await upload(`/projects/${proj.id}/import/preview`, 'mini.har', HAR, '');
	check('预览成功', preview.body.code === 0, JSON.stringify(preview.body));
	check('识别为 har', preview.body.data?.detected === 'har', preview.body.data?.detected);
	const cases = preview.body.data?.cases || [];
	check('产出 1 个候选用例', cases.length === 1, `实际 ${cases.length}`);
	check('占位名替换为文件名 mini', cases[0]?.name === 'mini', cases[0]?.name);
	check('code 为 imp_ 前缀', String(cases[0]?.code || '').startsWith('imp_mini'), cases[0]?.code);
	check('步骤摘要含 GET /login', (cases[0]?.summary || []).join().includes('GET /login'), JSON.stringify(cases[0]?.summary));

	// 预览不落库
	const listAfterPreview = data(await call('GET', `/projects/${proj.id}/cases?page=1&page_size=50`));
	check('预览不落库', listAfterPreview.total === 0, `实际 ${listAfterPreview.total}`);

	// ② 提交落库
	const commit = await upload(`/projects/${proj.id}/import/commit`, 'mini.har', HAR, '');
	check('提交成功', commit.body.code === 0, JSON.stringify(commit.body));
	check('创建 1 条', commit.body.data?.created?.length === 1, JSON.stringify(commit.body.data));
	check('无失败项', commit.body.data?.failed?.length === 0, JSON.stringify(commit.body.data?.failed));
	const created = commit.body.data?.created?.[0];

	// 落库内容抽查：断言（HAR 字典形态）被解析
	const detail = data(await call('GET', `/cases/${created.id}`));
	check('用例含 1 个步骤', detail.steps?.length === 1, `实际 ${detail.steps?.length}`);
	const validate = detail.steps?.[0]?.validate || [];
	check('断言已解析（status_code/equals/200）',
		validate.length >= 1 && validate[0].check === 'status_code' && String(validate[0].expect) === '200',
		JSON.stringify(validate));

	// ③ 同文件再导：名字自动加后缀
	const commit2 = await upload(`/projects/${proj.id}/import/commit`, 'mini.har', HAR, '');
	check('第二次导入成功', commit2.body.code === 0, JSON.stringify(commit2.body));
	check('第二次也创建 1 条', commit2.body.data?.created?.length === 1, JSON.stringify(commit2.body.data));
	check('重名自动加后缀', commit2.body.data?.created?.[0]?.name !== created.name,
		`${commit2.body.data?.created?.[0]?.name} vs ${created.name}`);

	// ④ curl 格式
	const CURL = 'curl -H "Authorization: Bearer tok" "http://127.0.0.1:8133/login?u=alice"';
	const curlCommit = await upload(`/projects/${proj.id}/import/commit`, 'cmd.txt', CURL, 'curl');
	check('curl 导入成功', curlCommit.body.code === 0, JSON.stringify(curlCommit.body));
	check('curl 创建 1 条', curlCommit.body.data?.created?.length === 1, JSON.stringify(curlCommit.body.data));

	// ⑤ 错误路径
	const bad = await upload(`/projects/${proj.id}/import/preview`, 'x.txt', 'hello world', '');
	check('未知格式报错', bad.body.code !== 0, JSON.stringify(bad.body));
	const empty = await upload(`/projects/${proj.id}/import/preview`, 'x.har', '', 'auto');
	check('空文件报错', empty.body.code !== 0, JSON.stringify(empty.body));

	console.log(`\n${pass} 项通过，${fail} 项失败`);
	if (fail) {
		failures.forEach((f) => console.log('  - ' + f));
		process.exit(1);
	}
}

main().catch((e) => {
	console.error('脚本异常：', e);
	process.exit(1);
});
