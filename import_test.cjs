// 纯 Node DOM 模拟回归：node --test import_test.cjs home_test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { webcrypto } = require('node:crypto');

class Element {
  constructor(tag) {
    this.tagName = tag;
    this.className = '';
    this.children = [];
    this.parentNode = null;
    this.attributes = {};
    this.disabled = false;
    this.checked = false;
    this.value = '';
    this.text = '';
    this.style = {};
    this.classList = {
      add: name => { this.className += ' ' + name; },
      remove: name => { this.className = this.className.split(' ').filter(n => n !== name).join(' '); }
    };
  }
  set textContent(value) {
    this.text = String(value);
    this.children.forEach(c => { c.parentNode = null; });
    this.children = [];
  }
  get textContent() { return this.text + this.children.map(c => c.textContent).join(''); }
  set innerHTML(_) { throw new Error('禁止通过 innerHTML 插入内容'); }
  appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
  setAttribute(key, value) { this.attributes[key] = String(value); }
  getAttribute(key) { return this.attributes[key]; }
  remove() {
    if (this.parentNode) this.parentNode.children = this.parentNode.children.filter(c => c !== this);
    this.parentNode = null;
  }
  contains(node) { return this === node || this.children.some(c => c.contains(node)); }
}
class TestFormData {
  constructor() { this.values = new Map(); }
  append(key, value) { this.values.set(key, value); }
  get(key) { return this.values.get(key); }
}
const all = (root, predicate) => [root, ...root.children.flatMap(c => all(c, () => true))].filter(predicate);
const byClass = (root, name) => all(root, e => e.className.split(' ').includes(name));
const byName = (root, name) => all(root, e => e.name === name)[0];
const click = element => element.onclick({ stopPropagation() {} });
const input = (element, value) => { element.value = value; (element.oninput || element.onchange)(); };
const check = photo => { const el = byClass(photo, 'import-reviewed')[0]; el.checked = true; el.onchange(); };
const flush = () => new Promise(resolve => setImmediate(resolve));
const file = (name = '练习.png', props = {}) => ({ name, type: 'image/png', size: 1024, lastModified: 123, ...props });
const question = (props = {}) => ({ qno: '第4题', qtype: '计算题', stem: '计算 $-\\frac{1}{2}$', student_answer: '$2^3$', uncertainties: [], ...props });
const recognition = (questions = [question()]) => ({ success: true, questions, model: 'mock-model' });
const httpError = (status, message = '字段校验失败') => ({ ok: false, status, json: async () => ({ success: false, error: message }) });
const deferred = () => { let resolve, reject; const promise = new Promise((r, j) => { resolve = r; reject = j; }); return { promise, resolve, reject }; };

function fixture({ config = {}, hash = '#/import/数学', dateClass = Date } = {}) {
  const nodes = Object.fromEntries(['app', 'crumb', 'toast', 'token-state', 'btn-token'].map(id => [id, new Element('div')]));
  const listeners = new Map();
  const calls = [], prompts = [], replies = [], createdURLs = [], revokedURLs = [], mathCalls = [];
  const location = { hash, pathname: '/', search: '' };
  const storage = new Map([['mistake_token', 'test-token']]);
  const control = {
    config: { success: true, enabled: true, model: 'mock-model', max_photos: 20, max_photo_bytes: 10485760, max_questions: 30, dry_run: false, ...config },
    recognize: () => recognition(),
    confirm: call => {
      const data = JSON.parse(call.options.body.get('metadata'));
      return { success: true, ids: data.questions.map((_, i) => 'entry-' + i), imported: data.questions.length, already_imported: false, dry_run: false };
    },
    raw: null
  };
  function emit(name, event = {}) { for (const handler of [...(listeners.get(name) || [])]) handler(event); }
  const window = {
    addEventListener: (name, fn) => { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn); },
    removeEventListener: (name, fn) => listeners.get(name)?.delete(fn),
    renderMathInElement: node => mathCalls.push(node.textContent)
  };
  const context = vm.createContext({
    window, location,
    history: { replaceState: (_, __, url) => { location.hash = url.includes('#') ? url.slice(url.indexOf('#')) : ''; } },
    document: { getElementById: id => nodes[id], createElement: tag => new Element(tag), createTextNode: text => { const n = new Element('#text'); n.textContent = text; return n; }, body: new Element('body') },
    localStorage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) },
    confirm: message => { prompts.push(message); return replies.length ? replies.shift() : true; },
    prompt: () => null,
    crypto: webcrypto, Uint8Array, AbortController, FormData: TestFormData, URLSearchParams, Date: dateClass,
    URL: { createObjectURL: f => { const url = 'blob:local-test/' + createdURLs.length; createdURLs.push({ url, file: f }); return url; }, revokeObjectURL: url => revokedURLs.push(url) },
    setTimeout: () => 1, clearTimeout: () => {},
    fetch: async (url, options) => {
      const call = { url, options };
      calls.push(call);
      if (control.raw) return control.raw(call);
      let data;
      if (url === '/api/import/config') data = control.config;
      else if (url === '/api/subjects') data = { subjects: [{ subject: '数学', name: '七年级上册数学', count: 0, last_date: '' }] };
      else if (url.startsWith('/api/entries?')) data = { items: [], facets: {} };
      else if (url === '/api/import/recognize') data = await control.recognize(call);
      else if (url === '/api/import/confirm') data = await control.confirm(call);
      else throw new Error('未模拟的请求：' + url);
      return { ok: true, status: 200, json: async () => data };
    }
  });
  for (const script of ['app.js', 'home.js', 'subject.js', 'import.js']) vm.runInContext(fs.readFileSync(path.join(__dirname, 'web', script), 'utf8'), context, { filename: script });
  context.app.boot();
  const main = nodes.app;
  const photos = () => byClass(main, 'import-photo');
  const add = (...files) => { const el = byClass(main, 'import-picker')[0]; el.files = files; el.onchange(); };
  const recognize = (photo, numbers = '第4题') => { input(byName(photo, 'question_numbers'), numbers); click(byClass(photo, 'import-recognize')[0]); };
  const confirmPhoto = photo => click(byClass(photo, 'import-confirm')[0]);
  const requests = suffix => calls.filter(c => c.url === '/api/import/' + suffix);
  const navigate = hash => { location.hash = hash; emit('hashchange'); };
  return { context, main, nodes, calls, control, prompts, replies, createdURLs, revokedURLs, mathCalls, location, emit, listeners, photos, add, recognize, confirmPhoto, requests, navigate };
}
async function readyFixture(count = 1) {
  const f = fixture(); await flush();
  for (let i = 0; i < count; i++) f.add(file('练习' + i + '.png'));
  for (const p of f.photos()) f.recognize(p);
  await flush();
  return f;
}

test('识别后只产生草稿，不自动核对或提交；未勾选无法确认', async () => {
  const f = await readyFixture();
  const p = f.photos()[0];
  assert.match(p.textContent, /待核对/);
  assert.equal(byClass(p, 'import-reviewed')[0].checked, false);
  assert.equal(f.requests('confirm').length, 0);
  f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 0);
  assert.match(p.textContent, /请先成功识别并勾选已核对/);
});

test('所有草稿字段有 label；任何字段与批次编辑均撤销核对', async () => {
  const f = await readyFixture();
  const p = f.photos()[0];
  const q = byClass(p, 'import-question')[0];
  for (const key of ['qno', 'qtype', 'stem', 'student_answer', 'chapter', 'source', 'tags', 'analysis', 'note']) {
    const el = byName(q, key);
    assert.equal(el.parentNode.tagName, 'label', key);
    check(p);
    input(el, key === 'source' ? '自我复习' : '编辑内容');
    assert.equal(byClass(p, 'import-reviewed')[0].checked, false, key);
  }
  for (const key of ['subject', 'date', 'chapter', 'source']) {
    check(p);
    input(byName(byClass(f.main, 'import-metadata')[0], key), key === 'date' ? '2026-10-13' : '新值');
    assert.equal(byClass(p, 'import-reviewed')[0].checked, false, key);
  }
});

test('多图题号独立，队列最多 2 并发，重复点击不会重复识别', async () => {
  const f = fixture(); await flush();
  const jobs = [];
  f.control.recognize = () => { const job = deferred(); jobs.push(job); return job.promise; };
  f.add(file('a.png'), file('b.png'), file('c.png'));
  f.photos().forEach((p, i) => input(byName(p, 'question_numbers'), ['第4题', '第10题(1)', '第12题'][i]));
  click(byClass(f.main, 'import-start')[0]);
  click(byClass(f.main, 'import-start')[0]);
  for (const p of f.photos()) click(byClass(p, 'import-recognize')[0]);
  assert.equal(f.requests('recognize').length, 2);
  assert.deepEqual(f.requests('recognize').map(c => c.options.body.get('question_numbers')), ['第4题', '第10题(1)']);
  assert.match(f.photos()[2].textContent, /排队中/);
  jobs[0].resolve(recognition()); await flush();
  assert.equal(f.requests('recognize').length, 3);
  assert.equal(f.requests('recognize')[2].options.body.get('question_numbers'), '第12题');
  jobs[1].resolve(recognition()); jobs[2].resolve(recognition()); await flush();
  assert.equal(f.requests('confirm').length, 0);
});

test('识别失败保留可重试状态，不自动重试收费请求', async () => {
  const f = fixture(); await flush(); f.add(file());
  f.control.recognize = () => { throw new Error('模拟识别失败'); };
  f.recognize(f.photos()[0]); await flush(); await flush();
  assert.equal(f.requests('recognize').length, 1);
  assert.match(f.photos()[0].textContent, /失败（可重试识别）/);
  f.control.recognize = () => recognition();
  click(byClass(f.photos()[0], 'import-recognize')[0]); await flush();
  assert.equal(f.requests('recognize').length, 2);
  assert.match(f.photos()[0].textContent, /待核对/);
});

test('确认进行中防重复提交并禁用编辑、删除和离开；成功照片永久锁定', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  const job = deferred(); f.control.confirm = () => job.promise;
  check(p); f.confirmPhoto(p); f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 1);
  assert.equal(byName(p, 'stem').disabled, true);
  assert.equal(byClass(p, 'import-remove-photo')[0].disabled, true);
  click(byClass(p, 'import-remove-photo')[0]);
  assert.equal(f.photos().length, 1);
  f.navigate('#/');
  assert.equal(f.location.hash, '#/import/数学');
  assert.equal(f.photos()[0], p);
  let prevented = false; const event = { preventDefault() { prevented = true; } };
  f.emit('beforeunload', event);
  assert.equal(prevented, true);
  assert.match(event.returnValue, /后台完成/);
  job.resolve({ success: true, ids: ['saved'], imported: 1, already_imported: false, dry_run: false }); await flush();
  assert.match(p.textContent, /成功（已入库）/);
  f.confirmPhoto(p); click(byClass(p, 'import-recognize')[0]); click(byClass(p, 'import-remove-photo')[0]);
  assert.equal(f.requests('confirm').length, 1);
  assert.equal(f.requests('recognize').length, 1);
  assert.equal(f.photos().length, 1);
  assert.equal(byClass(p, 'import-reviewed')[0].disabled, true);
});

test('确认失败冻结 payload，原样重试使用同一 import_id、metadata 和 File', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  input(byName(p, 'tags'), '分数、指数');
  f.control.confirm = () => { throw new Error('回执丢失'); };
  check(p); f.confirmPhoto(p); await flush();
  assert.equal(f.requests('confirm').length, 1);
  assert.match(p.textContent, /入库结果不确定/);
  assert.match(p.textContent, /重试确认（相同内容）/);
  const first = f.requests('confirm')[0].options.body;
  const data = JSON.parse(first.get('metadata'));
  assert.match(data.import_id, /^[0-9a-f]{32}$/);
  assert.equal(data.confirmed, true);
  assert.equal(data.subject, '数学');
  assert.deepEqual(data.questions[0].tags, ['分数', '指数']);
  assert.equal(byName(p, 'stem').disabled, true);
  input(byName(p, 'stem'), '试图覆盖冻结草稿');
  input(byName(byClass(f.main, 'import-metadata')[0], 'subject'), '英语');
  click(byClass(p, 'import-remove-question')[0]);
  click(byClass(p, 'import-recognize')[0]);
  assert.equal(f.requests('recognize').length, 1);
  f.control.confirm = () => ({ success: true, ids: ['saved'], imported: 1, already_imported: true, dry_run: false });
  f.confirmPhoto(p); f.confirmPhoto(p); await flush();
  const second = f.requests('confirm')[1].options.body;
  assert.equal(first.get('metadata'), second.get('metadata'));
  assert.equal(first.get('photo'), second.get('photo'));
  assert.match(p.textContent, /此前已入库（未重复写入）/);
});

test('确认失败照片的 payload 不受其他照片编辑影响，每图使用独立 import_id', async () => {
  const f = await readyFixture(2); const [a, b] = f.photos();
  f.control.confirm = () => { throw new Error('连接断开'); };
  check(a); f.confirmPhoto(a); await flush();
  const first = f.requests('confirm')[0].options.body.get('metadata');
  input(byName(b, 'stem'), '另一张图的修订题干');
  f.control.confirm = call => ({ success: true, ids: ['saved'], imported: 1, already_imported: false, dry_run: false });
  check(b); f.confirmPhoto(b); await flush();
  f.confirmPhoto(a); await flush();
  const payloads = f.requests('confirm').map(c => c.options.body.get('metadata'));
  assert.equal(payloads[0], payloads[2]);
  assert.notEqual(JSON.parse(first).import_id, JSON.parse(payloads[1]).import_id);
  assert.equal(JSON.parse(payloads[1]).questions[0].stem, '另一张图的修订题干');
});

test('apiForm 带 token/signal，不手设 multipart Content-Type，JSON api 保持 JSON 头', async () => {
  const f = fixture(); await flush();
  const controller = new AbortController();
  const form = new TestFormData(); form.append('photo', file());
  await f.context.app.apiForm('/api/import/recognize', { body: form, signal: controller.signal, headers: { 'Content-Type': 'bad', 'content-type': 'bad', 'X-Test': 'yes' } });
  const request = f.requests('recognize')[0];
  assert.equal(request.options.headers['X-Mistake-Token'], 'test-token');
  assert.equal(request.options.headers['X-Test'], 'yes');
  assert.equal(Object.keys(request.options.headers).some(k => k.toLowerCase() === 'content-type'), false);
  assert.equal(request.options.body, form);
  assert.equal(request.options.signal, controller.signal);
  await f.context.app.api('/api/subjects', { method: 'POST', body: { a: 1 }, signal: controller.signal });
  const json = f.calls.at(-1);
  assert.equal(json.options.headers['Content-Type'], 'application/json');
  assert.equal(json.options.body, '{"a":1}');
  assert.equal(json.options.signal, controller.signal);
});

test('非 JSON/HTTP 错误给出友好提示，失败响应不能假成功', async () => {
  const f = fixture(); await flush();
  f.control.raw = () => ({ ok: false, status: 502, json: async () => { throw new SyntaxError('<html>'); } });
  await assert.rejects(f.context.app.apiForm('/bad', { body: new TestFormData() }), /非 JSON.*502/);
  f.control.raw = () => ({ ok: true, status: 200, json: async () => ({ success: false, error: '识别未启用' }) });
  await assert.rejects(f.context.app.apiForm('/bad', {}), /识别未启用/);
});

test('取消 hash 离开保留相同 DOM、核对状态、草稿和 URLs；import 到另一科目也确认', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  input(byName(p, 'stem'), '必须保留的草稿'); check(p);
  const before = f.main.children[0];
  for (const hash of ['#/', '#/import/英语']) {
    f.replies.push(false); f.navigate(hash);
    assert.equal(f.main.children[0], before);
    assert.equal(f.location.hash, '#/import/数学');
    assert.equal(byName(p, 'stem').value, '必须保留的草稿');
    assert.equal(byClass(p, 'import-reviewed')[0].checked, true);
    assert.equal(f.revokedURLs.length, 0);
  }
  f.replies.push(true); f.navigate('#/import/英语'); await flush();
  assert.equal(f.revokedURLs.length, 1);
  assert.equal(f.photos().length, 0);
  assert.equal(byName(byClass(f.main, 'import-metadata')[0], 'subject').value, '英语');
  assert.equal(f.listeners.get('beforeunload').size, 1);
});

test('确认离开中止识别、清空排队任务和监听，迟到响应不能重建草稿', async () => {
  const f = fixture(); await flush();
  const job = deferred(); f.control.recognize = () => job.promise;
  f.add(file('a.png'), file('b.png'), file('c.png'));
  for (const p of f.photos()) input(byName(p, 'question_numbers'), '第1题');
  click(byClass(f.main, 'import-start')[0]);
  const requests = f.requests('recognize');
  f.replies.push(false); f.navigate('#/');
  assert.equal(requests[0].options.signal.aborted, false);
  f.replies.push(true); f.navigate('#/'); await flush();
  assert.equal(requests.length, 2);
  for (const r of requests) assert.equal(r.options.signal.aborted, true);
  assert.equal(f.revokedURLs.length, 3);
  assert.equal(f.listeners.get('beforeunload').size, 0);
  assert.equal(f.requests('config')[0].options.signal.aborted, true);
  job.resolve(recognition()); await flush();
  assert.equal(f.photos().length, 0);
  assert.equal(f.requests('recognize').length, 2);
  assert.equal(byClass(f.main, 'home-import').length, 1);
});

test('同一文件本批两次选择不重复；拒绝非图片、空图、超 10 MiB、超过 20 张', async () => {
  const f = fixture(); await flush();
  const same = file(); f.add(same, same); f.add(same, { ...same });
  assert.equal(f.photos().length, 1);
  assert.equal(f.createdURLs.length, 1);
  f.add(file('a.gif', { type: 'image/gif' }), file('empty.png', { size: 0 }), file('large.png', { size: 10485761 }));
  assert.equal(f.photos().length, 1);
  f.add(file('边界.jpg', { size: 10485760, type: 'image/jpeg' }));
  assert.equal(f.photos().length, 2);
  f.add(...Array.from({ length: 20 }, (_, i) => file(i + '.png')));
  assert.equal(f.photos().length, 20);
  assert.equal(f.createdURLs.length, 20);
  assert.match(f.main.textContent, /本批最多 20/);
});

test('题号变更令旧草稿失效，即使改回原值也必须重识别；覆盖编辑必须确认', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  input(byName(p, 'stem'), '人工修订'); check(p);
  input(byName(p, 'question_numbers'), '第10题(1)');
  input(byName(p, 'question_numbers'), '第4题');
  assert.match(p.textContent, /题号已变更/);
  check(p); f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 0);
  f.replies.push(false); click(byClass(p, 'import-recognize')[0]);
  assert.equal(f.requests('recognize').length, 1);
  assert.match(f.prompts.at(-1), /覆盖.*编辑/);
  assert.equal(byName(p, 'stem').value, '人工修订');
  click(byClass(f.main, 'import-start')[0]);
  assert.equal(f.requests('recognize').length, 1, '全批不能绕过覆盖警告');
  f.replies.push(true); click(byClass(p, 'import-recognize')[0]); await flush();
  assert.equal(f.requests('recognize').length, 2);
  assert.equal(byClass(p, 'import-reviewed')[0].checked, false);
});

test('重识别失败保留旧编辑，但不能提交旧结果', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  input(byName(p, 'note'), '保留的备注');
  f.control.recognize = () => { throw new Error('模型失败'); };
  click(byClass(p, 'import-recognize')[0]); await flush();
  assert.equal(byName(p, 'note').value, '保留的备注');
  assert.match(p.textContent, /旧草稿仅供参考/);
  check(p); f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 0);
});

test('移除多识别题会撤销核对，移除照片撤销其 ObjectURL', async () => {
  const f = fixture(); await flush();
  f.control.recognize = () => recognition([question(), question({ qno: '第10题(1)' })]);
  f.add(file()); f.recognize(f.photos()[0]); await flush();
  const p = f.photos()[0]; check(p);
  click(byClass(p, 'import-remove-question')[1]);
  assert.equal(byClass(p, 'import-question').length, 1);
  assert.equal(byClass(p, 'import-reviewed')[0].checked, false);
  check(p); f.confirmPhoto(p); await flush();
  assert.equal(JSON.parse(f.requests('confirm')[0].options.body.get('metadata')).questions.length, 1);
  f.add(file('待移除.png')); click(byClass(f.photos()[1], 'import-remove-photo')[0]);
  assert.equal(f.photos().length, 1);
  assert.equal(f.revokedURLs.length, 1);
});

test('未配置和配置失败禁识别，显示原因而不假成功', async () => {
  const f = fixture({ config: { enabled: false } }); await flush(); f.add(file());
  f.recognize(f.photos()[0]);
  assert.equal(f.requests('recognize').length, 0);
  assert.equal(byClass(f.main, 'import-start')[0].disabled, true);
  assert.match(f.main.textContent, /识别未启用/);
  f.control.raw = () => { throw new Error('配置服务不可用'); };
  click(all(f.main, e => e.tagName === 'button' && e.textContent === '重新读取配置')[0]); await flush();
  assert.match(f.main.textContent, /无法读取识别配置，识别已禁用/);
  assert.equal(f.requests('recognize').length, 0);
});

test('dry_run 回执不是入库成功，也不允许再次提交', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  f.control.confirm = () => ({ success: true, ids: [], imported: 0, already_imported: false, dry_run: true });
  check(p); f.confirmPhoto(p); await flush();
  assert.match(p.textContent, /演练完成（未入库）/);
  assert.match(byClass(f.main, 'import-summary')[0].textContent, /已入库成功 0.*剩余未入库 1/);
  f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 1);
});

test('部分成功显示成功与剩余数，返回列表重新读取该科目', async () => {
  const f = await readyFixture(2); const [a, b] = f.photos();
  check(a); f.confirmPhoto(a); await flush();
  assert.match(byClass(f.main, 'import-summary')[0].textContent, /已入库成功 1.*剩余未入库 1/);
  assert.equal(byClass(b, 'import-reviewed')[0].checked, false);
  f.replies.push(false); click(byClass(f.main, 'import-back')[0]); f.emit('hashchange');
  assert.match(f.prompts.at(-1), /成功 1.*剩余未入库 1/);
  assert.equal(f.photos()[1], b);
  f.replies.push(true); click(byClass(f.main, 'import-back')[0]); f.emit('hashchange'); await flush();
  assert.equal(f.calls.filter(c => c.url === '/api/entries?subject=' + encodeURIComponent('数学')).length, 1);
  assert.equal(f.photos().length, 0);
});

test('使用本地当天、科目与错题本名称分离，允许新科目', async () => {
  class LocalDate extends Date {
    getFullYear() { return 2026; }
    getMonth() { return 9; }
    getDate() { return 13; }
    toISOString() { return '2026-10-12T16:30:00.000Z'; }
  }
  const f = fixture({ dateClass: LocalDate }); await flush();
  const metadata = byClass(f.main, 'import-metadata')[0];
  assert.equal(byName(metadata, 'date').value, '2026-10-13');
  assert.equal(byName(metadata, 'subject').value, '数学');
  assert.match(byClass(f.main, 'import-subject-name')[0].textContent, /错题本名称：七年级上册数学.*科目：数学/);
  assert.doesNotMatch(f.main.textContent, /科目 key|新科目 key/);
  input(byName(metadata, 'subject'), '新科目');
  f.add(file()); f.recognize(f.photos()[0]); await flush(); check(f.photos()[0]); f.confirmPhoto(f.photos()[0]); await flush();
  assert.equal(JSON.parse(f.requests('confirm')[0].options.body.get('metadata')).subject, '新科目');
});

test('文件名、模型内容与疑点只作为文本，原图仅为自建 blob，题干/作答支持 KaTeX 预览', async () => {
  const f = fixture(); await flush();
  const hostile = '<img src=x onerror=alert(1)>';
  f.control.recognize = () => recognition([question({ stem: hostile, student_answer: hostile, uncertainties: [hostile] })]);
  f.add(file(hostile)); f.recognize(f.photos()[0]); await flush();
  const p = f.photos()[0];
  assert.match(p.textContent, /<img src=x onerror=alert\(1\)>/);
  for (const b of byClass(p, 'import-preview')) click(b);
  assert.deepEqual(f.mathCalls, [hostile, hostile]);
  assert.equal(all(p, e => e.tagName === 'img').length, 1);
  const original = byClass(p, 'import-original')[0];
  assert.match(original.href, /^blob:local-test\//);
  assert.equal(original.target, '_blank');
  assert.equal(original.rel, 'noopener noreferrer');
  assert.match(f.main.textContent, /无疑点不代表无错误，注意负号\/分数\/指数/);
});

test('api 在 fetch 与解析响应阶段都保留 AbortError，401 提示重设 token', async () => {
  const f = fixture(); await flush();
  const abort = new Error('已取消'); abort.name = 'AbortError';
  f.control.raw = () => { throw abort; };
  await assert.rejects(f.context.app.api('/cancel'), e => e === abort);
  f.control.raw = () => ({ ok: true, status: 200, json: async () => { throw abort; } });
  await assert.rejects(f.context.app.apiForm('/cancel', {}), e => e === abort);
  f.control.raw = () => ({ ok: false, status: 401 });
  await assert.rejects(f.context.app.apiForm('/unauthorized', {}), /重设 token/);
});

test('遵守服务端更严格的图片与题数上限，超限识别结果不能提交', async () => {
  const f = fixture({ config: { max_photos: 2, max_photo_bytes: 2048, max_questions: 1 } }); await flush();
  f.add(file('超限.png', { size: 2049 }), file('a.png'), file('b.png'), file('c.png'));
  assert.equal(f.photos().length, 2);
  assert.match(f.main.textContent, /本批最多 2/);
  f.control.recognize = () => recognition([question(), question({ qno: '第10题' })]);
  f.recognize(f.photos()[0]); await flush();
  assert.match(f.photos()[0].textContent, /超出题数限制/);
  check(f.photos()[0]); f.confirmPhoto(f.photos()[0]);
  assert.equal(f.requests('confirm').length, 0);
});

test('空识别与格式错误不生成可核对草稿', async () => {
  const f = fixture(); await flush(); f.add(file());
  const p = f.photos()[0];
  for (const response of [recognition([]), recognition([question({ stem: { text: '错误格式' } })]), recognition([question({ uncertainties: null })])]) {
    f.control.recognize = () => response;
    f.recognize(p); await flush();
    assert.match(p.textContent, /失败/);
    assert.equal(byClass(p, 'import-question').length, 0);
    check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0);
  }
});

test('缺失或不完整入库回执保持结果不确定，不会把部分结果标成完成', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  check(p);
  for (const response of [
    { success: true, ids: ['saved'], imported: 1 },
    { success: true, ids: [], imported: 0, dry_run: false },
    { success: true, ids: [], imported: 1, dry_run: false }
  ]) {
    f.control.confirm = () => response;
    f.confirmPhoto(p); await flush();
    assert.match(p.textContent, /入库结果不确定/);
    assert.match(byClass(f.main, 'import-summary')[0].textContent, /已入库成功 0.*剩余未入库 1/);
  }
  assert.equal(new Set(f.requests('confirm').map(c => c.options.body.get('metadata'))).size, 1);
});

test('批次章节出处作为每题默认值，每题修订独立进入确认 metadata', async () => {
  const f = fixture(); await flush();
  const metadata = byClass(f.main, 'import-metadata')[0];
  input(byName(metadata, 'chapter'), '第一章');
  input(byName(metadata, 'source'), '校内作业');
  f.add(file()); f.recognize(f.photos()[0]); await flush();
  const p = f.photos()[0];
  assert.equal(byName(p, 'chapter').value, '第一章');
  assert.equal(byName(p, 'source').value, '校内作业');
  input(byName(p, 'chapter'), '第二章');
  input(byName(p, 'source'), '自我复习');
  input(byName(p, 'analysis'), '遗漏负号');
  input(byName(p, 'note'), '复习分数运算');
  check(p); f.confirmPhoto(p); await flush();
  const data = JSON.parse(f.requests('confirm')[0].options.body.get('metadata'));
  assert.equal(data.chapter, '第一章');
  assert.equal(data.source, '校内作业');
  assert.equal(data.questions[0].chapter, '第二章');
  assert.equal(data.questions[0].source, '自我复习');
  assert.equal(data.questions[0].analysis, '遗漏负号');
  assert.equal(data.questions[0].note, '复习分数运算');
});

test('requestJSON 保留 HTTP status，区分明确后端拒绝与代理非 JSON 错误', async () => {
  const f = fixture(); await flush();
  for (const status of [400, 409, 413, 500]) {
    f.control.raw = () => httpError(status);
    await assert.rejects(f.context.app.apiForm('/status', {}), e => e.status === status && e.serverRejected === true);
  }
  f.control.raw = () => ({ ok: false, status: 400, json: async () => { throw new SyntaxError('代理 HTML'); } });
  await assert.rejects(f.context.app.apiForm('/status', {}), e => e.status === 400 && e.serverRejected === false);
});

test('首次明确 400 恢复编辑与批次信息，撤销核对，修改后使用同一编号重新提交', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  const metadata = byClass(f.main, 'import-metadata')[0];
  input(byName(p, 'note'), '原备注');
  f.control.raw = () => httpError(400, '服务端字段校验失败');
  check(p); f.confirmPhoto(p); await flush();
  assert.match(p.textContent, /尚未写入，已恢复编辑/);
  assert.equal(byClass(p, 'import-status')[0].textContent, '待核对');
  assert.equal(byClass(p, 'import-reviewed')[0].checked, false);
  assert.equal(byName(p, 'note').value, '原备注');
  assert.equal(byName(p, 'note').disabled, false);
  assert.equal(byClass(p, 'import-remove-photo')[0].disabled, false);
  assert.equal(byName(metadata, 'subject').disabled, false);
  assert.equal(byClass(p, 'import-confirm')[0].disabled, true);
  f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 1, '必须重新核对');
  input(byName(metadata, 'chapter'), '修订章节');
  input(byName(p, 'note'), '修订备注');
  f.control.raw = null;
  check(p); f.confirmPhoto(p); await flush();
  const [before, after] = f.requests('confirm').map(c => JSON.parse(c.options.body.get('metadata')));
  assert.equal(before.import_id, after.import_id);
  assert.equal(after.chapter, '修订章节');
  assert.equal(after.questions[0].note, '修订备注');
  assert.match(p.textContent, /成功（已入库）/);
});

test('400 恢复仅当前照片；本批仍有冻结照片时批次信息不解锁', async () => {
  const f = await readyFixture(2); const [a, b] = f.photos();
  f.control.confirm = () => { throw new Error('第一张回执未知'); };
  check(a); f.confirmPhoto(a); await flush();
  const first = f.requests('confirm')[0].options.body.get('metadata');
  f.control.raw = () => httpError(400);
  check(b); f.confirmPhoto(b); await flush();
  assert.equal(byName(a, 'stem').disabled, true);
  assert.equal(byName(b, 'stem').disabled, false);
  assert.equal(byClass(b, 'import-reviewed')[0].checked, false);
  assert.equal(byName(byClass(f.main, 'import-metadata')[0], 'subject').disabled, true);
  assert.match(a.textContent, /入库结果不确定/);
  f.control.raw = null;
  f.control.confirm = () => ({ success: true, ids: ['saved'], imported: 1, dry_run: false });
  f.confirmPhoto(a); await flush();
  assert.equal(f.requests('confirm')[2].options.body.get('metadata'), first);
});

test('没有不确定历史时连续明确 400 仍可修正，不会自动重试', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  f.control.raw = () => httpError(400);
  for (let i = 0; i < 2; i++) {
    input(byName(p, 'note'), '第' + i + '次修订');
    check(p); f.confirmPhoto(p); await flush();
    assert.equal(byName(p, 'note').disabled, false);
    assert.equal(byClass(p, 'import-reviewed')[0].checked, false);
    assert.equal(f.requests('confirm').length, i + 1);
  }
});

test('网络、5xx、409、代理 413 与未知回执均冻结；后续 400 也不得解冻', async () => {
  for (const kind of ['network', 500, 409, 413, 'unknown', 'html400']) {
    const f = await readyFixture(); const p = f.photos()[0];
    f.control.raw = () => {
      if (kind === 'network') throw new TypeError('连接断开');
      if (kind === 'unknown') return { ok: true, status: 200, json: async () => ({ success: true }) };
      if (kind === 'html400') return { ok: false, status: 400, json: async () => { throw new SyntaxError('代理错误'); } };
      return httpError(kind, '请求失败');
    };
    check(p); f.confirmPhoto(p); await flush();
    assert.match(p.textContent, /入库结果不确定/, String(kind));
    assert.equal(byName(p, 'stem').disabled, true, String(kind));
    const payload = f.requests('confirm')[0].options.body;
    f.control.raw = () => httpError(400, '重试校验失败');
    f.confirmPhoto(p); await flush();
    assert.equal(byName(p, 'stem').disabled, true, String(kind));
    assert.equal(byClass(p, 'import-confirm')[0].textContent, '重试确认（相同内容）');
    assert.equal(f.requests('confirm')[1].options.body.get('metadata'), payload.get('metadata'));
    assert.equal(f.requests('confirm')[1].options.body.get('photo'), payload.get('photo'));
    assert.equal(byName(byClass(f.main, 'import-metadata')[0], 'subject').disabled, true);
  }
});

test('明确 400 恢复后若发生不确定失败，之后 400 不能再次恢复编辑', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  f.control.raw = () => httpError(400);
  check(p); f.confirmPhoto(p); await flush();
  input(byName(p, 'stem'), '已修订的题干');
  f.control.raw = () => { throw new Error('响应丢失'); };
  check(p); f.confirmPhoto(p); await flush();
  const frozen = f.requests('confirm')[1].options.body.get('metadata');
  f.control.raw = () => httpError(400);
  f.confirmPhoto(p); await flush();
  assert.match(p.textContent, /入库结果不确定/);
  assert.equal(byName(p, 'stem').disabled, true);
  assert.equal(f.requests('confirm')[2].options.body.get('metadata'), frozen);
});

test('逐题所有长度上限均在发请求前拦截，草稿保持可编辑且撤销核对', async () => {
  const limits = { qno: 64, qtype: 64, stem: 12000, student_answer: 8000, chapter: 128, source: 16, analysis: 4000, note: 2000 };
  for (const [key, max] of Object.entries(limits)) {
    const f = await readyFixture(); const p = f.photos()[0];
    input(byName(p, key), '字'.repeat(max + 1));
    check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0, key);
    assert.match(p.textContent, new RegExp('最多 ' + max + ' 个字符'));
    assert.equal(byName(p, key).disabled, false, key);
    assert.equal(byClass(p, 'import-reviewed')[0].checked, false, key);
    assert.equal(byName(byClass(f.main, 'import-metadata')[0], 'subject').disabled, false);
  }
});

test('科目及批次章节、出处的长度上限同样在请求前校验', async () => {
  for (const [key, max] of Object.entries({ subject: 64, chapter: 128, source: 16 })) {
    const f = await readyFixture(); const p = f.photos()[0];
    const metadata = byClass(f.main, 'import-metadata')[0];
    input(byName(metadata, key), '字'.repeat(max + 1));
    check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0, key);
    assert.match(p.textContent, new RegExp('最多 ' + max + ' 个字符'));
    assert.equal(byName(metadata, key).disabled, false);
  }
});

test('长度边界使用 Unicode 码点计数，中文与补充平面字符不按 UTF-16 双倍计算', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  const rune = '\u{1f600}';
  const metadata = byClass(f.main, 'import-metadata')[0];
  input(byName(metadata, 'subject'), rune.repeat(64));
  input(byName(metadata, 'chapter'), rune.repeat(128));
  for (const [key, max] of Object.entries({ qno: 64, qtype: 64, stem: 12000, student_answer: 8000, chapter: 128, analysis: 4000, note: 2000 })) input(byName(p, key), rune.repeat(max));
  input(byName(p, 'tags'), Array.from({ length: 20 }, (_, i) => rune.repeat(30) + String(i).padStart(2, '0')).join('、'));
  check(p); f.confirmPhoto(p); await flush();
  assert.equal(f.requests('confirm').length, 1);
  assert.match(p.textContent, /成功（已入库）/);
  const data = JSON.parse(f.requests('confirm')[0].options.body.get('metadata'));
  assert.equal(Array.from(data.questions[0].stem).length, 12000);
  assert.equal(data.questions[0].tags.length, 20);
});

test('知识点超过 20 个或单个超过 32 个码点不提交', async () => {
  for (const tags of [Array.from({ length: 21 }, (_, i) => '知识点' + i).join('、'), '字'.repeat(33), '\u{1f600}'.repeat(33)]) {
    const f = await readyFixture(); const p = f.photos()[0];
    input(byName(p, 'tags'), tags); check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0);
    assert.match(p.textContent, /知识点最多 20 个|知识点最多 32 个字符/);
  }
});

test('识别题号超过 500 码点不产生收费请求，500 个补充平面字符可通过长度校验', async () => {
  const f = fixture(); await flush(); f.add(file()); const p = f.photos()[0];
  for (const value of ['字'.repeat(501), '\u{1f600}'.repeat(501)]) {
    f.recognize(p, value);
    assert.equal(f.requests('recognize').length, 0);
    assert.match(p.textContent, /最多 500 个字符/);
  }
  f.recognize(p, '\u{1f600}'.repeat(500)); await flush();
  assert.equal(f.requests('recognize').length, 1);
});

test('日期真实有效校验覆盖闰年、月份天数、零年与格式', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  const date = byName(byClass(f.main, 'import-metadata')[0], 'date');
  for (const value of ['2025-02-29', '1900-02-29', '2026-04-31', '2026-00-10', '2026-13-01', '2026-01-00', '2026-01-32', '0000-01-01', '2026-2-03', '2026-10-13junk', '2026-10-13\n']) {
    input(date, value); check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0, value);
    assert.match(p.textContent, /真实有效/);
    assert.equal(date.disabled, false);
  }
  input(date, '2000-02-29'); check(p); f.confirmPhoto(p); await flush();
  assert.equal(f.requests('confirm').length, 1);
});

test('提交前重新检查照片大小，不对超限照片发送确认请求', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  f.control.config.max_photo_bytes = 512;
  click(all(f.main, e => e.tagName === 'button' && e.textContent === '重新读取配置')[0]); await flush();
  check(p); f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 0);
  assert.match(p.textContent, /照片不能为空且不得超过/);
  assert.equal(byClass(p, 'import-remove-photo')[0].disabled, false);
});

test('同图规范化后重复题号不提交，包含空白、全角、前导零与小题括号', async () => {
  for (const [first, second] of [
    ['第4题', ' ００４． '], ['第4题', '第 ４ 题、'],
    ['第10题(1)', ' 第１０题（１） '], ['第0题', '000'], ['第4题', '\u00a0第４题\u3000']
  ]) {
    const f = fixture(); await flush();
    f.control.recognize = () => recognition([question({ qno: first }), question({ qno: second })]);
    f.add(file()); f.recognize(f.photos()[0]); await flush(); const p = f.photos()[0];
    check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0, second);
    assert.match(p.textContent, /题号重复/);
    assert.equal(byName(p, 'qno').disabled, false);
  }
});

test('不同照片同题号不拒绝，同图不同小题题号也不误判', async () => {
  const f = fixture(); await flush();
  f.control.recognize = () => recognition([question({ qno: '第10题(1)' }), question({ qno: '第10题(2)' })]);
  f.add(file('a.png'), file('b.png'));
  for (const p of f.photos()) f.recognize(p);
  await flush();
  for (const p of f.photos()) { check(p); f.confirmPhoto(p); await flush(); }
  assert.equal(f.requests('confirm').length, 2);
  assert.match(byClass(f.main, 'import-summary')[0].textContent, /已入库成功 2 张（4 题）/);
});

test('单行字段换行、控制字符与无效出处在请求前拦截', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  for (const [key, value] of [['qtype', '第一行\n第二行'], ['note', '错误\u0000字符'], ['source', '未定义出处']]) {
    const el = byName(p, key), before = el.value;
    input(el, value); check(p); f.confirmPhoto(p);
    assert.equal(f.requests('confirm').length, 0, key);
    assert.equal(el.disabled, false);
    input(el, before);
  }
});

test('删除全部题后显示无保留题目，不能核对提交，可重新识别或移除照片', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  check(p); click(byClass(p, 'import-remove-question')[0]);
  assert.match(byClass(p, 'import-status')[0].textContent, /无保留题目/);
  assert.match(byClass(p, 'import-result-hint')[0].textContent, /重新识别此图，或移除照片/);
  assert.equal(byClass(p, 'import-reviewed')[0].checked, false);
  assert.equal(byClass(p, 'import-reviewed')[0].disabled, true);
  assert.equal(byClass(p, 'import-confirm')[0].disabled, true);
  check(p); f.confirmPhoto(p);
  assert.equal(f.requests('confirm').length, 0);
  click(byClass(p, 'import-recognize')[0]); await flush();
  assert.equal(byClass(p, 'import-question').length, 1);
  assert.equal(byClass(p, 'import-status')[0].textContent, '待核对');
  click(byClass(p, 'import-remove-question')[0]); click(byClass(p, 'import-remove-photo')[0]);
  assert.equal(f.photos().length, 0);
  assert.equal(f.revokedURLs.length, 1);
});

test('原图与题号同在左栏，编辑核对区在右栏，响应式 CSS 限制长内容宽度', async () => {
  const f = await readyFixture(); const p = f.photos()[0];
  const layout = byClass(p, 'import-photo-layout')[0];
  const reference = byClass(p, 'import-photo-reference')[0];
  const editor = byClass(p, 'import-photo-editor')[0];
  assert.deepEqual(layout.children, [reference, editor]);
  assert.equal(reference.children[0], byClass(p, 'import-original')[0]);
  assert.equal(reference.children[1], byName(p, 'question_numbers').parentNode);
  assert.equal(editor.contains(byClass(p, 'import-questions')[0]), true);
  assert.equal(editor.contains(byClass(p, 'import-reviewed')[0]), true);
  const css = fs.readFileSync(path.join(__dirname, 'web/style.css'), 'utf8');
  assert.match(css, /\.import-photo-layout\s*\{[^}]*grid-template-columns: minmax\(0, \.85fr\) minmax\(0, 1\.15fr\)/);
  assert.match(css, /\.import-photo-reference\s*\{[^}]*position: sticky/);
  assert.match(css, /@media \(max-width: 850px\)[\s\S]*?\.import-photo-reference\s*\{[^}]*position: static/);
  assert.match(css, /\.import-page fieldset\s*\{[^}]*min-width: 0/);
  assert.match(css, /\.import-math-preview\s*\{[^}]*min-width: 0;[^}]*max-width: 100%;[^}]*overflow-x: auto/);
});

test('首页和科目页入口正确且重复路由不叠加首页', async () => {
  const f = fixture({ hash: '#/' }); await flush();
  const homeButton = byClass(f.main, 'home-import')[0].children[0];
  click(homeButton); f.emit('hashchange'); await flush();
  assert.equal(f.location.hash, '#/import');
  f.navigate('#/subject/数学'); await flush();
  const subjectButton = all(f.main, e => e.tagName === 'button' && e.textContent === '导入照片')[0];
  click(subjectButton); f.emit('hashchange'); await flush();
  assert.equal(decodeURIComponent(f.location.hash), '#/import/数学');
  f.navigate('#/'); await flush(); f.emit('hashchange');
  assert.equal(byClass(f.main, 'home-import').length, 1);
  assert.equal(byClass(f.main, 'cards').length, 1);
});
