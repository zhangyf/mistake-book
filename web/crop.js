// crop.js —— 在原图上框选「题图」
//
// 老师反馈（2026-10-13）：题目里有图时，模型要么画不出来、要么画得不像；
// 应该允许直接在原图上框一块，把那块当作这道题的图。
//
// 做法：**不新增图片**，只记「哪张原图 + 框了哪一块」——题干里插一段 figure 块：
//
//	```figure
//	exam-bank/数学/2026-10-第三章有理数/images/2026-10-13-import-9f44.jpg 0.1200 0.3400 0.5500 0.2100 1.6180
//	```
//
// 六个字段：COS key / x / y / w / h / 裁剪区宽高比（相对原图归一化）。
// 草稿阶段还不知道原图最终 key，所以写 @photo，由后端在确认入库时换成真实 key。
//
// 网页里渲染用纯 CSS 裁（原图本来就在浏览器里，即时、无损、不占服务端）：
// 容器按 ratio 定高定宽，里面的图放大到 100/w % 再负偏移，就只剩框里那块。
// 导出 HTML 练习卷则走后端真裁（那里必须内联，不能内联整张原图）。
//
// 校验规则与服务端 figure.go 保持一致；解析不出就当普通字符画块降级显示，
// 绝不因为老师手改出一个非法块就让他存不了题。
var crop = (function () {
  var KEY_RE = /^exam-bank\/[^\s#\\]+\.(?:jpe?g|png|webp|gif|heic)$/i;
  var UNIT_RE = /^\d(?:\.\d{1,4})?$/;
  var RATIO_RE = /^\d{1,2}(?:\.\d{1,4})?$/;
  var PHOTO_ALIAS = '@photo';
  var MIN_SIDE = 0.01;
  var MIN_RATIO = 0.05;
  var MAX_RATIO = 20;
  var MIN_DRAG_PX = 12;

  function isFence(line) { return /^\s*`{3,}/.test(line); }
  function isFigureFence(line) { return /^\s*`{3,}\s*figure\s*$/i.test(line); }
  function validKey(key) { return key === PHOTO_ALIAS || KEY_RE.test(key); }
  function round4(n) { return Math.round(n * 10000) / 10000; }

  // parseRef —— 解析块体（取第一条非空行，须恰好六个合法字段）
  function parseRef(body) {
    var line = '';
    for (var i = 0; i < body.length; i++) {
      if (String(body[i]).trim()) { line = String(body[i]).trim(); break; }
    }
    if (!line) return null;
    var f = line.split(/[\s\u3000]+/);
    if (f.length !== 6 || !validKey(f[0])) return null;
    for (var j = 1; j <= 4; j++) { if (!UNIT_RE.test(f[j])) return null; }
    if (!RATIO_RE.test(f[5])) return null;
    var ref = {
      key: f[0], x: parseFloat(f[1]), y: parseFloat(f[2]),
      w: parseFloat(f[3]), h: parseFloat(f[4]), ratio: parseFloat(f[5])
    };
    if (ref.w < MIN_SIDE || ref.h < MIN_SIDE) return null;
    if (ref.x < 0 || ref.y < 0 || ref.x + ref.w > 1.0001 || ref.y + ref.h > 1.0001) return null;
    if (!(ref.ratio >= MIN_RATIO && ref.ratio <= MAX_RATIO)) return null;
    return ref;
  }

  // figures —— 扫出文本里所有解析成功的 figure 块（行号区间含首尾围栏）
  function figures(text) {
    var lines = String(text || '').split('\n');
    var out = [];
    for (var i = 0; i < lines.length; i++) {
      if (!isFigureFence(lines[i])) continue;
      var close = -1;
      for (var j = i + 1; j < lines.length; j++) { if (isFence(lines[j])) { close = j; break; } }
      var end = close >= 0 ? close : lines.length - 1;
      var ref = parseRef(lines.slice(i + 1, close >= 0 ? close : lines.length));
      if (ref) out.push({ ref: ref, start: i, end: end });
      i = end;
    }
    return out;
  }

  function hasFence(text) { return /^\s*`{3,}/m.test(String(text || '')); }

  // block —— 生成 figure 块文本
  function block(ref) {
    return '```figure\n' + [ref.key, round4(ref.x).toFixed(4), round4(ref.y).toFixed(4),
      round4(ref.w).toFixed(4), round4(ref.h).toFixed(4), round4(ref.ratio).toFixed(4)].join(' ') + '\n```';
  }

  // upsert —— 把题图放进题干：已经有围栏块（通常是模型画的字符画）就替换掉它，
  // 这正是「画得不好」的场景；没有就追加到末尾。
  function upsert(text, blockText) {
    var lines = String(text || '').split('\n');
    for (var i = 0; i < lines.length; i++) {
      if (!isFence(lines[i])) continue;
      var close = -1;
      for (var j = i + 1; j < lines.length; j++) { if (isFence(lines[j])) { close = j; break; } }
      var end = close >= 0 ? close : lines.length - 1;
      return lines.slice(0, i).concat(blockText.split('\n'), lines.slice(end + 1)).join('\n');
    }
    var base = String(text || '').replace(/\s+$/, '');
    return base ? base + '\n\n' + blockText : blockText;
  }

  // removeAt —— 删掉第 index 个题图块（按 figures() 的顺序）
  function removeAt(text, index) {
    var list = figures(text);
    if (index < 0 || index >= list.length) return text;
    var lines = String(text || '').split('\n');
    var b = list[index];
    var out = lines.slice(0, b.start).concat(lines.slice(b.end + 1));
    // 顺手收掉留下的连续空行，别让题干里堆一片空白
    for (var i = out.length - 1; i > 0; i--) {
      if (!out[i].trim() && !out[i - 1].trim()) out.splice(i, 1);
    }
    return out.join('\n');
  }

  // renderFigure —— 把题图渲染成按框裁好的图（纯 CSS，不切像素）
  function renderFigure(ref, resolve) {
    var url = resolve ? resolve(ref.key) : '';
    if (!url) return null;
    var box = document.createElement('div');
    box.className = 'figcrop';
    box.style.aspectRatio = String(ref.ratio);
    box.appendChild(imgNode(url, ref));
    return box;
  }

  function imgNode(url, ref) {
    var img = document.createElement('img');
    img.alt = '题图';
    img.src = url;
    img.style.position = 'absolute';
    img.style.height = 'auto';
    img.style.width = (100 / ref.w) + '%';
    img.style.left = (-100 * ref.x / ref.w) + '%';
    img.style.top = (-100 * ref.y / ref.h) + '%';
    return img;
  }

  // renderThumb —— 小方框里的题图缩略图，给「已框选的图」列表用
  function renderThumb(ref, resolve, onRemove) {
    var row = document.createElement('div');
    row.className = 'figrow';
    var box = document.createElement('div');
    box.className = 'figcrop';
    box.style.aspectRatio = String(ref.ratio);
    var url = resolve ? resolve(ref.key) : '';
    if (url) {
      box.appendChild(imgNode(url, ref));
    } else {
      box.className = 'figcrop figcrop-bad';
      box.textContent = '原图不可读';
    }
    row.appendChild(box);
    var meta = document.createElement('div');
    meta.className = 'figmeta';
    meta.textContent = 'x ' + ref.x.toFixed(2) + '　y ' + ref.y.toFixed(2) +
      '　w ' + ref.w.toFixed(2) + '　h ' + ref.h.toFixed(2);
    row.appendChild(meta);
    if (onRemove) {
      var del = document.createElement('button');
      del.type = 'button';
      del.className = 'danger';
      del.textContent = '删除此图';
      del.onclick = function () { onRemove(); };
      row.appendChild(del);
    }
    return row;
  }

  // normalizeBox —— 拖出来的两点换算成归一化框（夹到图内、不小于最小边长）
  function normalizeBox(a, b) {
    var x = Math.min(a.x, b.x), y = Math.min(a.y, b.y);
    var w = Math.abs(a.x - b.x), h = Math.abs(a.y - b.y);
    x = round4(x); y = round4(y); w = round4(w); h = round4(h);
    if (w < MIN_SIDE) w = MIN_SIDE;
    if (h < MIN_SIDE) h = MIN_SIDE;
    if (x + w > 1) x = Math.max(0, round4(1 - w));
    if (y + h > 1) y = Math.max(0, round4(1 - h));
    return { x: x, y: y, w: w, h: h };
  }

  // ratioOf —— 裁剪区宽高比（用来定渲染高度，避免等图加载完才定尺寸）
  function ratioOf(box, naturalWidth, naturalHeight) {
    if (!naturalWidth || !naturalHeight) return 1;
    var r = (box.w * naturalWidth) / (box.h * naturalHeight);
    r = Math.round(r * 10000) / 10000;
    if (!(r >= MIN_RATIO)) r = MIN_RATIO;
    if (r > MAX_RATIO) r = MAX_RATIO;
    return r;
  }

  // attach —— 在原图上框选。host 是挂载点，
  // opts: { imageUrl, key(), hint, onPick(ref) }
  function attach(host, opts) {
    var on = false, dragging = false, start = null;
    var wrap = document.createElement('div');
    wrap.className = 'croppanel';
    var bar = document.createElement('div');
    bar.className = 'cropbar';
    var toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'ghost';
    toggle.textContent = '框选原图';
    var tip = document.createElement('span');
    tip.className = 'hint';
    bar.appendChild(toggle);
    bar.appendChild(tip);
    wrap.appendChild(bar);

    var stage = document.createElement('div');
    stage.className = 'cropstage';
    var img = document.createElement('img');
    img.src = opts.imageUrl;
    img.alt = '原图';
    img.draggable = false;
    stage.appendChild(img);
    var rubber = document.createElement('div');
    rubber.className = 'croprubber';
    stage.appendChild(rubber);
    wrap.appendChild(stage);
    host.appendChild(wrap);

    function setMode(next) {
      on = next;
      stage.className = 'cropstage' + (on ? ' on' : '');
      toggle.textContent = on ? '退出框选' : '框选原图';
      tip.textContent = on ? '在原图上按住拖一个矩形，松手即插入' : (opts.hint || '题目里的图模型画不好时，直接在这里框一块');
      if (!on) { dragging = false; rubber.style.display = 'none'; }
    }
    setMode(false);

    toggle.onclick = function () { setMode(!on); };

    function point(event) {
      var rect = img.getBoundingClientRect();
      if (!rect.width || !rect.height) return null;
      var x = (event.clientX - rect.left) / rect.width;
      var y = (event.clientY - rect.top) / rect.height;
      return { x: Math.min(1, Math.max(0, x)), y: Math.min(1, Math.max(0, y)) };
    }

    function draw(a, b) {
      var rect = img.getBoundingClientRect();
      var box = normalizeBox(a, b);
      rubber.style.display = 'block';
      rubber.style.left = (box.x * rect.width) + 'px';
      rubber.style.top = (box.y * rect.height) + 'px';
      rubber.style.width = (box.w * rect.width) + 'px';
      rubber.style.height = (box.h * rect.height) + 'px';
      return box;
    }

    stage.addEventListener('pointerdown', function (event) {
      if (!on || event.button !== 0) return;
      var p = point(event);
      if (!p) return;
      event.preventDefault();
      dragging = true;
      start = p;
      if (stage.setPointerCapture) { try { stage.setPointerCapture(event.pointerId); } catch (e) {} }
      draw(start, start);
    });
    stage.addEventListener('pointermove', function (event) {
      if (!dragging) return;
      var p = point(event);
      if (p) draw(start, p);
    });
    function finish(event) {
      if (!dragging) return;
      dragging = false;
      rubber.style.display = 'none';
      var p = point(event);
      if (!p) return;
      var rect = img.getBoundingClientRect();
      if (Math.abs(p.x - start.x) * rect.width < MIN_DRAG_PX ||
          Math.abs(p.y - start.y) * rect.height < MIN_DRAG_PX) {
        tip.textContent = '框太小了，重新拖一个大一点的框';
        return;
      }
      var box = normalizeBox(start, p);
      var ref = {
        key: (opts.key && opts.key()) || PHOTO_ALIAS,
        x: box.x, y: box.y, w: box.w, h: box.h,
        ratio: ratioOf(box, img.naturalWidth, img.naturalHeight)
      };
      setMode(false);
      opts.onPick(ref);
    }
    stage.addEventListener('pointerup', finish);
    stage.addEventListener('pointercancel', function () {
      dragging = false;
      rubber.style.display = 'none';
    });

    return { node: wrap, setMode: setMode, image: img };
  }

  // choose —— 多题共用一张照片时，问一下这个框插给哪道题
  function choose(title, options, done) {
    var mask = document.createElement('div');
    mask.className = 'mask';
    var box = document.createElement('div');
    box.className = 'modal';
    var h = document.createElement('h3');
    h.textContent = title;
    box.appendChild(h);
    var sel = document.createElement('select');
    options.forEach(function (o) {
      var op = document.createElement('option');
      op.value = o.value;
      op.textContent = o.label;
      sel.appendChild(op);
    });
    var row = document.createElement('div');
    row.className = 'mfield';
    row.appendChild(sel);
    box.appendChild(row);
    var foot = document.createElement('div');
    foot.className = 'mfoot';
    var cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'ghost';
    cancel.textContent = '取消';
    var ok = document.createElement('button');
    ok.type = 'button';
    ok.className = 'primary';
    ok.textContent = '插入此图';
    foot.appendChild(cancel);
    foot.appendChild(ok);
    box.appendChild(foot);
    mask.appendChild(box);
    document.body.appendChild(mask);
    function close() { mask.remove(); }
    cancel.onclick = close;
    mask.onclick = function (e) { if (e.target === mask) close(); };
    ok.onclick = function () { var v = sel.value; close(); done(v); };
  }

  return {
    refOf: parseRef, figures: figures, hasFence: hasFence, block: block, upsert: upsert, removeAt: removeAt,
    isFigureFence: isFigureFence, isFence: isFence, normalizeBox: normalizeBox, ratioOf: ratioOf,
    renderFigure: renderFigure, renderThumb: renderThumb, attach: attach, choose: choose,
    validKey: validKey, PHOTO_ALIAS: PHOTO_ALIAS
  };
})();
