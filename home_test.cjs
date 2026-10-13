// Frontend regression tests without browser dependencies: node --test home_test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class Element {
  constructor(tag, className = '', text = '') {
    this.tagName = tag;
    this.className = className;
    this.children = [];
    this.disabled = false;
    this.textContent = text;
  }
  appendChild(child) { this.children.push(child); return child; }
  set textContent(value) { this.text = value; this.children = []; }
  get textContent() { return this.text + this.children.map(c => c.textContent).join(''); }
}
function matching(root, predicate) {
  return [root, ...root.children.flatMap(c => matching(c, predicate))].filter(predicate);
}
function byClass(root, name) {
  return matching(root, e => e.className.split(' ').includes(name));
}
const flush = () => new Promise(resolve => setImmediate(resolve));

function fixture() {
  const main = new Element('main');
  const subjects = [{ subject: '数学', name: '七年级上册数学', count: 3, last_date: '2026-09-21' }];
  const calls = [], prompts = [], replies = [], toasts = [];
  const control = { fail: false, wait: null };
  const context = vm.createContext({
    window: {},
    location: { hash: '#/' },
    document: { getElementById: () => main },
    prompt: (message, initial) => { prompts.push(initial); return replies.shift(); },
    app: {
      el: (tag, cls, text) => new Element(tag, cls || '', text === undefined ? '' : text),
      toast: message => toasts.push(message),
      api: async (url, options) => {
        calls.push({ url, options });
        if (url === '/api/subjects') return { subjects: JSON.parse(JSON.stringify(subjects)) };
        assert.equal(url, '/api/subject');
        if (control.wait) await control.wait;
        if (control.fail) throw new Error('测试失败');
        subjects[0].name = options.body.name || subjects[0].subject;
        return { success: true };
      }
    }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'web/home.js'), 'utf8'), context);
  const render = () => context.views.home.render(main);
  const rename = () => byClass(main, 'pen')[0];
  const click = button => button.onclick({ stopPropagation() {} });
  return { main, calls, prompts, replies, toasts, control, render, rename, click };
}
function assertSingleHome(f, name) {
  assert.equal(byClass(f.main, 'section-title').length, 1, 'only one homepage heading');
  assert.equal(byClass(f.main, 'cards').length, 1, 'only one card container');
  assert.equal(byClass(f.main, 'card').length, 1, 'only one subject card');
  assert.equal(byClass(f.main, 'name')[0].textContent, name);
}

test('repeated rendering replaces the old homepage', async () => {
  const f = fixture();
  f.render(); await flush();
  f.render(); await flush();
  assertSingleHome(f, '七年级上册数学');
});

test('consecutive renames update the existing card without fetching another homepage', async () => {
  const f = fixture();
  f.render(); await flush();
  const card = byClass(f.main, 'card')[0];
  for (const name of ['七年级上册数学1', '我的数学错题本']) {
    f.replies.push(name);
    f.click(f.rename()); await flush();
    assertSingleHome(f, name);
    assert.equal(byClass(f.main, 'card')[0], card, 'card is updated in place');
  }
  assert.deepEqual(f.prompts, ['七年级上册数学', '七年级上册数学1']);
  assert.equal(f.calls.filter(c => c.url === '/api/subjects').length, 1);
});

test('canceling a rename does not submit or change the card', async () => {
  const f = fixture(); f.render(); await flush();
  f.replies.push(null); f.click(f.rename()); await flush();
  assertSingleHome(f, '七年级上册数学');
  assert.equal(f.calls.length, 1);
  assert.equal(f.rename().disabled, false);
});

test('a failed rename keeps the old name and permits retry', async () => {
  const f = fixture(); f.render(); await flush();
  f.control.fail = true;
  f.replies.push('新名字'); f.click(f.rename()); await flush();
  assertSingleHome(f, '七年级上册数学');
  assert.equal(f.rename().disabled, false);
  assert.match(f.toasts.at(-1), /保存失败/);
  f.control.fail = false;
  f.replies.push('重试成功'); f.click(f.rename()); await flush();
  assertSingleHome(f, '重试成功');
});

test('an empty name restores the subject default', async () => {
  const f = fixture(); f.render(); await flush();
  f.replies.push('   '); f.click(f.rename()); await flush();
  assertSingleHome(f, '数学');
  assert.equal(f.calls.at(-1).options.body.name, '');
});

test('pending rename blocks duplicate submission and does not overwrite a different view', async () => {
  const f = fixture(); f.render(); await flush();
  let release;
  f.control.wait = new Promise(resolve => { release = resolve; });
  const button = f.rename();
  f.replies.push('保存中的名字'); f.click(button);
  assert.equal(button.disabled, true);
  f.click(button);
  assert.equal(f.calls.filter(c => c.url === '/api/subject').length, 1);
  f.main.textContent = '已进入题目页';
  release(); await flush();
  assert.equal(f.main.textContent, '已进入题目页', 'late response must not append homepage');
  assert.equal(button.disabled, false);
});
