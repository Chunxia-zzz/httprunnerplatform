// 源码可编辑（M3 ③-c 4.2）后端冒烟：YAML 反解析 → 落库 → 往返等价。
//
// 用法：
//   node scripts/smoke-yaml-save.mjs
//
// 覆盖（对应 docs/M3调试体验设计.md 4.2）：
//   - 渲染 YAML → 修改 → SaveYAML 落库（name/steps 生效）
//   - 非法 YAML → code=50003 + 带「第 N 行」
//   - 缺 config.name → 拒绝保存
//   - 反解析后正向重编译语义等价（name、步骤数、URL 一致）

import http from 'node:http';

const BASE = process.env.HRP_SMOKE_BASE || 'http://127.0.0.1:8127';
const API = `${BASE}/api/v1`;
const STUB_PORT = 8134;
const USER = process.env.HRP_SMOKE_USER || 'admin';
const PASS = process.env.HRP_SMOKE_PASS || 'admin123';

let pass = 0, fail = 0;
const failures = [];
function check(name, cond, detail = '') {
  if (cond) { pass++; console.log(`  [OK] ${name}`); }
  else { fail++; failures.push(name + (detail ? ` — ${detail}` : '')); console.log(`  [FAIL] ${name}${detail ? ` — ${detail}` : ''}`); }
}

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
    method, headers: { 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  for (const c of res.headers.getSetCookie?.() || []) cookie = c.split(';')[0];
  return { status: res.status, body: await res.json() };
}
function data(r) { if (r.body.code !== 0) throw new Error(`${r.body.code} ${r.body.message}`); return r.body.data; }

const stamp = Date.now().toString(36);

async function main() {
  const stub = await startStub();
  const login = await call('POST', '/auth/login', { username: USER, password: PASS });
  check('登录成功', login.body.code === 0);

  const proj = data(await call('POST', '/projects', { code: `ys${stamp}`, name: '源码保存验收项目' }));
  const env = data(await call('POST', `/projects/${proj.id}/environments`, {
    name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
  }));

  const tc = data(await call('POST', `/projects/${proj.id}/cases`, {
    code: `tc${stamp}`, name: '源码保存用例', module: 'ys', priority: 'P0', status: 'active',
    config: { verify: false, variables: {}, export: [] },
    steps: [
      { seq: 1, step_type: 'request', name: '请求', enabled: true, request: { method: 'GET', url: '/get' } },
    ],
  }));
  check('建用例成功', !!tc.id);

  // 1. 渲染 YAML
  const preview = data(await call('GET', `/cases/${tc.id}/yaml?env_id=${env.id}`));
  check('渲染 YAML 成功', preview.yaml && preview.yaml.includes('config:'));

  // 2. 修改 YAML：改 name + 加第 2 步 + 改 URL
  const modified = preview.yaml
    .replace('name: 源码保存用例', 'name: 源码保存用例_改')
    .replace('url: /get', 'url: /get?x=1')
    + `\n    - name: 第二步骤\n      request:\n          method: POST\n          url: /post\n`;

  const saved = await call('PUT', `/cases/${tc.id}/yaml`, { yaml: modified });
  check('SaveYAML 成功', saved.body.code === 0, JSON.stringify(saved.body));

  // 3. 验证落库：name 改了、步骤变成 2 个
  const detail = data(await call('GET', `/cases/${tc.id}`));
  check('name 已更新', detail.name === '源码保存用例_改', `实际 ${detail.name}`);
  check('步骤数 = 2', detail.steps.length === 2, `实际 ${detail.steps.length}`);

  // 4. 反向重编译：语义等价（2 步，URL 正确）
  const rePreview = data(await call('GET', `/cases/${tc.id}/yaml?env_id=${env.id}`));
  check('重编译含第 2 步', rePreview.yaml.includes('第二步骤'));
  check('重编译含改过的 URL', rePreview.yaml.includes('/get?x=1'));

  // 5. 非法 YAML → 报错带行号
  const bad = await call('PUT', `/cases/${tc.id}/yaml`, { yaml: 'config:\n  name: [unclosed' });
  check('非法 YAML 报 50003', bad.body.code === 50003, `code=${bad.body.code}`);
  check('报错含「第 N 行」', /第\s*\d+\s*行/.test(bad.body.message || ''), bad.body.message);

  // 6. 缺 config.name → 拒绝
  const noname = await call('PUT', `/cases/${tc.id}/yaml`, {
    yaml: 'config:\n    verify: false\nteststeps:\n    - name: x\n      request:\n          method: GET\n          url: /get\n',
  });
  check('缺 name 被拒', noname.body.code === 50003, `code=${noname.body.code} msg=${noname.body.message}`);

  console.log(`\n===== ${pass} 项通过，${fail} 项失败 =====`);
  if (fail) { failures.forEach((f) => console.log('  - ' + f)); process.exitCode = 1; }
  stub.close();
}

main().catch((e) => { console.error(e); process.exitCode = 1; });
