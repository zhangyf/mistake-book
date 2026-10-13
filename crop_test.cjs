// crop.js 的前端回归测试：node --test crop_test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const KEY = 'exam-bank/数学/2026-10-第三章有理数/images/2026-10-13-import-9f44.jpg';

class El {
  constructor(tag) {
    this.tagName = tag;
    this.className = '';
    this.style = {};
    this.children = [];
    this.textContent = '';
    this.onclick = null;
  }
  appendChild(child) { this.children.push(child); return child; }
  remove() {}
  addEventListener() {}
}

function load() {
  const document = {
    createElement: tag => new El(tag),
    body: new El('body'),
    getElementById: () => null
  };
  const context = vm.createContext({ window: {}, document });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'web/crop.js'), 'utf8'), context);
  return context.crop;
}
const crop = load();

function ref(over) {
  return Object.assign({ key: KEY, x: 0.12, y: 0.34, w: 0.55, h: 0.21, ratio: 1.618 }, over || {});
}
const blockOf = r => crop.block(r);
const blockText = r => '```figure\n' + [r.key, r.x, r.y, r.w, r.h, r.ratio].join(' ') + '\n```';

// 块格式必须和后端 figure.go 一模一样（Go 侧同样断言这个字面量）
test('block 输出与服务端同格式', () => {
  assert.equal(blockOf(ref()), '```figure\n' + KEY + ' 0.1200 0.3400 0.5500 0.2100 1.6180\n```');
  assert.equal(blockOf(ref({ key: '@photo', x: 0, y: 0, w: 1, h: 0.5, ratio: 2 })).split('\n')[1],
    '@photo 0.0000 0.0000 1.0000 0.5000 2.0000');
});

test('refOf 校验与服务端一致', () => {
  assert.ok(crop.refOf([KEY + ' 0.1200 0.3400 0.5500 0.2100 1.6180']));
  assert.ok(crop.refOf(['', '@photo 0 0 1 1 2.0000', '']));
  const bad = [
    KEY + ' 0.1 0.2 0.5',                              // 字段不足
    KEY + ' 0.1 0.2 0.5 0.2 1.6 9',                    // 字段多余
    'other/x.jpg 0.1 0.2 0.5 0.2 1.6',                 // 越界目录
    'exam-bank/a/b.md 0.1 0.2 0.5 0.2 1.6',            // 非图片
    'exam-bank/a.jpg"onload=x 0.1 0.2 0.5 0.2 1.6',    // 注入
    KEY + ' 0.9000 0.2 0.5000 0.2 1.6',                // 越界
    KEY + ' -0.1 0.2 0.5 0.2 1.6',                     // 负数
    KEY + ' 1e-1 0.2 0.5 0.2 1.6',                     // 科学计数
    KEY + ' 0.12345 0.2 0.5 0.2 1.6',                  // 小数位过多
    KEY + ' 0.1 0.2 0.001 0.2 1.6',                    // 框太小
    KEY + ' 0.1 0.2 0.5 0.2 99.0',                     // 宽高比过大
    KEY + ' 0.1 0.2 0.5 0.2 0.01'                      // 宽高比过小
  ];
  bad.forEach(line => assert.equal(crop.refOf([line]), null, line));
  assert.equal(crop.refOf(['']), null);
});

test('figures 只认解析成功的 figure 块', () => {
  const text = '开头\n```\n数轴字符画\n```\n' + blockText(ref()) +
    '\n结尾\n```figure\n' + KEY + ' 0.9 0.2 0.5 0.2 1.6\n```';
  const found = crop.figures(text);
  assert.equal(found.length, 1);
  assert.equal(found[0].start, 4);
  assert.equal(found[0].end, 6);
  assert.equal(found[0].ref.key, KEY);
  assert.equal(crop.figures('没有围栏的题干').length, 0);
});

test('upsert 有字符画就替换、没有就追加', () => {
  const stem = '**题干**：如图\n\n- A. 1\n- B. 2';
  const withAscii = stem + '\n\n```\n┌───┐\n│ ● │\n└───┘\n```';
  const block = blockOf(ref());

  const replaced = crop.upsert(withAscii, block);
  assert.ok(replaced.includes(block));
  assert.ok(!replaced.includes('┌───┐'), '模型画的字符画应被题图替换');
  assert.ok(replaced.startsWith(stem), '题干正文保持不动');
  assert.equal(crop.figures(replaced).length, 1);

  const appended = crop.upsert(stem, block);
  assert.ok(appended.startsWith(stem));
  assert.ok(appended.endsWith(block));
  assert.equal(crop.figures(appended).length, 1);

  assert.equal(crop.upsert('', block), block);
});

test('removeAt 只删指定的那张题图', () => {
  const one = ref({ x: 0.1 });
  const two = ref({ x: 0.4 });
  const text = '题干\n' + blockText(one) + '\n中间\n' + blockText(two) + '\n后文';
  const left = crop.removeAt(text, 0);
  assert.ok(!left.includes('0.1 0.34') && left.includes('0.4 0.34'));
  assert.ok(left.includes('题干') && left.includes('中间') && left.includes('后文'));
  assert.equal(crop.figures(left).length, 1);

  const right = crop.removeAt(text, 1);
  assert.ok(right.includes('0.1 0.34') && !right.includes('0.4 0.34'));
  assert.equal(crop.figures(right).length, 1);
  assert.equal(crop.removeAt(text, 5), text, '越界索引不应改动');
});

test('normalizeBox 夹到图内并保证最小边长', () => {
  // vm 里造出来的对象与测试进程的原型不同，逐字段比
  const same = (box, want) => ['x', 'y', 'w', 'h'].every(k => box[k] === want[k]);
  assert.ok(same(crop.normalizeBox({ x: 0.1, y: 0.2 }, { x: 0.6, y: 0.7 }), { x: 0.1, y: 0.2, w: 0.5, h: 0.5 }));
  const tiny = crop.normalizeBox({ x: 0.5, y: 0.5 }, { x: 0.5001, y: 0.5001 });
  assert.ok(tiny.w >= 0.01 && tiny.h >= 0.01, '太小的框要抬到最小边长');
  const edge = crop.normalizeBox({ x: 0.9, y: 0.5 }, { x: 1.2, y: 0.9 });
  assert.ok(edge.x + edge.w <= 1.0001, '右上角不能越出原图');
  const back = crop.normalizeBox({ x: 0.8, y: 0.6 }, { x: 0.2, y: 0.1 });
  assert.ok(same(back, { x: 0.2, y: 0.1, w: 0.6, h: 0.5 }), '反向拖拽也要能框出来');
});

test('ratioOf 用原图尺寸算裁剪区宽高比', () => {
  assert.equal(crop.ratioOf({ w: 0.5, h: 0.25 }, 1000, 1000), 2);
  assert.equal(crop.ratioOf({ w: 0.5, h: 0.5 }, 2000, 1000), 2);
  assert.equal(crop.ratioOf({ w: 0.5, h: 0.5 }, 0, 0), 1, '拿不到尺寸时退回 1');
  assert.ok(crop.ratioOf({ w: 1, h: 0.01 }, 1000, 1000) <= 20, '极端值被夹住');
  assert.ok(crop.ratioOf({ w: 0.01, h: 1 }, 1000, 1000) >= 0.05, '极端值被夹住');
});

test('renderFigure 用纯 CSS 把框里的那块裁出来', () => {
  const node = crop.renderFigure(ref({ x: 0.1, y: 0.2, w: 0.5, h: 0.25, ratio: 1.6 }), () => '/api/image?key=x');
  assert.equal(node.className, 'figcrop');
  assert.equal(node.style.aspectRatio, '1.6');
  const img = node.children[0];
  assert.equal(img.src, '/api/image?key=x');
  assert.equal(img.style.width, '200%');   // 100 / w
  assert.equal(img.style.left, '-20%');    // -100 * x / w
  assert.equal(img.style.top, '-80%');     // -100 * y / h
  assert.equal(crop.renderFigure(ref(), () => ''), null, '解不出地址时不渲染');
});

test('renderThumb 带删除按钮，点了回调', () => {
  let removed = 0;
  const row = crop.renderThumb(ref(), () => '/api/image?key=x', () => { removed++; });
  assert.ok(row.children.some(c => c.className === 'figcrop'));
  const del = row.children.find(c => c.tagName === 'button');
  assert.equal(del.textContent, '删除此图');
  del.onclick();
  assert.equal(removed, 1);

  const broken = crop.renderThumb(ref(), () => '', null);
  assert.ok(broken.children.some(c => c.className === 'figcrop figcrop-bad'));
});
