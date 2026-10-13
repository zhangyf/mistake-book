// subject.js —— 某科目的错题列表：维度切换 + 筛选 + 详情 + 编辑/删除
views.subject = (function () {

  var state = {
    subject: '',
    items: [],
    facets: {},
    dim: 'date',        // date | tag | type | source
    filterTags: [],
    kw: '',
    openId: ''
  };

  var DIM_LABEL = { date: '按日期', tag: '按知识点', type: '按题型', source: '按出处' };
  var SOURCES = ['校内作业', '课外班作业', '校内考试', '自我复习'];

  // md 里表示「错因要点」的几种写法（后端 analysisKeys 一致）
  var ANALYSIS_KEYS = ['错因/要点', '错因要点', '错因', '要点', '错因分析', '错误原因'];

  // 导出：格式与内容可选项（与后端 api_export.go 的 exportFields 对应）
  var FORMATS = [
    ['html', 'HTML 排版卷（单文件，可打印 / 另存为 PDF）'],
    ['md', 'Markdown（.md，纯文本）']
  ];
  var EXPORT_FIELDS = [
    ['meta', '元信息（科目/试卷/日期/题号/出处/知识点）'],
    ['stem', '题干'],
    ['answer', '我的作答'],
    ['analysis', '错因要点'],
    ['solution', '答案/正解'],
    ['note', '备注'],
    ['image', '原图'],
    ['other', 'md 里的其他字段']
  ];
  // 默认按「重做练习卷」配：只要元信息 + 题干 + 备注，作答/错因是答案，默认不带
  var DEFAULT_FIELDS = ['meta', 'stem', 'note'];

  function render(main, subject) {
    state.subject = subject;
    state.items = []; state.facets = {}; state.filterTags = [];
    state.kw = ''; state.openId = '';

    var bar = app.el('div', 'toolbar');
    main.appendChild(bar);

    var tabs = app.el('div', 'tabs');
    bar.appendChild(tabs);
    Object.keys(DIM_LABEL).forEach(function (k) {
      var b = app.el('button', k === state.dim ? 'on' : '', DIM_LABEL[k]);
      b.onclick = function () {
        state.dim = k;
        Array.prototype.forEach.call(tabs.children, function (c) { c.className = ''; });
        b.className = 'on';
        paintList(main);
      };
      tabs.appendChild(b);
    });

    var search = document.createElement('input');
    search.type = 'text';
    search.className = 'search';
    search.placeholder = '搜索题干/备注/知识点';
    search.oninput = function () { state.kw = search.value.trim().toLowerCase(); paintList(main); };
    bar.appendChild(search);

    var importButton = app.el('button', 'primary', '导入');
    importButton.onclick = function () { location.hash = '#/import/' + encodeURIComponent(subject); };
    bar.appendChild(importButton);

    var exp = app.el('button', 'primary', '导出');
    exp.onclick = function () { openExport(); };
    bar.appendChild(exp);

    var tagBar = app.el('div', 'toolbar');
    main.appendChild(tagBar);
    var listWrap = app.el('div');
    main.appendChild(listWrap);

    listWrap.appendChild(app.el('div', 'empty', '加载中…'));

    app.api('/api/entries?subject=' + encodeURIComponent(subject)).then(function (d) {
      if (!main.contains(listWrap)) return;
      // 面包屑用老师起的名字（没起就还是科目名）
      if (d.display) {
        var crumb = document.getElementById('crumb');
        if (crumb) crumb.textContent = '› ' + d.display;
      }
      state.items = d.items || [];
      state.facets = d.facets || {};
      paintTags(tagBar, main, listWrap);
      paintList(main, listWrap);
    }).catch(function (e) {
      listWrap.textContent = '';
      listWrap.appendChild(app.el('div', 'empty', '加载失败：' + e.message));
    });

    // paintList 需要拿到列表容器
    main._listWrap = listWrap;
  }

  function opt(v, t) { var o = document.createElement('option'); o.value = v; o.textContent = t; return o; }

  function paintTags(bar, main, listWrap) {
    bar.textContent = '';
    var tags = Object.keys((state.facets || {}).tags || {});
    if (!tags.length) return;
    bar.appendChild(app.el('span', 'crumb', '知识点：'));
    tags.sort(function (a, b) { return (state.facets.tags[b] - state.facets.tags[a]); });
    tags.forEach(function (t) {
      var on = state.filterTags.indexOf(t) >= 0;
      var c = app.el('span', 'chip' + (on ? ' correct' : ''), t + ' ' + state.facets.tags[t]);
      c.style.cursor = 'pointer';
      c.onclick = function () {
        var i = state.filterTags.indexOf(t);
        if (i >= 0) state.filterTags.splice(i, 1); else state.filterTags.push(t);
        paintTags(bar, main, listWrap);
        repaint();
      };
      bar.appendChild(c);
    });
  }

  function repaint() {
    var main = document.getElementById('app');
    if (main && main._listWrap) paintList(main, main._listWrap);
  }

  function filtered() {
    return state.items.filter(function (it) {
      if (state.filterTags.length) {
        var hit = state.filterTags.some(function (t) { return (it.tags || []).indexOf(t) >= 0; });
        if (!hit) return false;
      }
      if (state.kw) {
        var blob = JSON.stringify(it).toLowerCase();
        if (blob.indexOf(state.kw) < 0) return false;
      }
      return true;
    });
  }

  function groupKey(it) {
    switch (state.dim) {
      case 'type': return it.qtype || '未分类题型';
      case 'source': return it.source || '未标注出处';
      case 'tag': return (it.tags && it.tags.length) ? it.tags.join('、') : '未打标签';
      default: return (it.date || '').slice(0, 7) || '无日期';
    }
  }

  function paintList(main, listWrap) {
    if (!listWrap) { listWrap = main._listWrap; }
    listWrap.textContent = '';

    var items = filtered();
    if (!items.length) {
      listWrap.appendChild(app.el('div', 'empty', '没有符合条件的题目'));
      return;
    }

    var groups = {};
    var order = [];
    items.forEach(function (it) {
      var k = groupKey(it);
      if (!(k in groups)) { groups[k] = []; order.push(k); }
      groups[k].push(it);
    });
    if (state.dim !== 'date') order.sort(); else order.sort().reverse();

    order.forEach(function (k) {
      var g = app.el('div', 'group');
      g.appendChild(app.el('h3', null, k + '　(' + groups[k].length + ')'));
      var cards = app.el('div', 'qcards');
      groups[k].forEach(function (it) { cards.appendChild(itemCard(it)); });
      g.appendChild(cards);
      listWrap.appendChild(g);
    });
  }

  // itemCard —— 卡片罗列，至少能看到一部分题干（题干预览由 /api/entries 从 md 里取）
  function itemCard(it) {
    var c = app.el('div', 'qcard');

    var head = app.el('div', 'chead');
    head.appendChild(app.el('span', 'qno', it.qno || '(无题号)'));
    if (it.qtype) head.appendChild(app.el('span', 'qtype', it.qtype));
    if (it.source) head.appendChild(app.el('span', 'chip', it.source));
    c.appendChild(head);

    var sub = app.el('div', 'csub', (it.exam || '') + (it.date ? '　' + it.date : ''));
    c.appendChild(sub);

    var body = app.el('div', 'cbody');
    var pv = it.preview || it.note || '（无题干）';
    var t = app.el('div', 'ctext', pv);
    body.appendChild(t);
    c.appendChild(body);
    app.math(t); // 预览里可能有 $...$，渲染出来

    if ((it.tags || []).length) {
      var chips = app.el('div', 'chips');
      it.tags.forEach(function (tg) { chips.appendChild(app.el('span', 'chip', tg)); });
      c.appendChild(chips);
    }

    c.onclick = function () { openDetail(it); };
    return c;
  }

  // openDetail —— 详情用居中浮框弹出（2026-10-13 老师：铺在列表最底下要翻到底，
  // 太不方便），点遮罩或「关闭」收起。编辑 / 题图框选 / 删除都在浮框里照常进行。
  function openDetail(it) {
    // 同一时间只开一个详情浮框（保存后会重开刷新数据）
    var old = document.querySelector('.dmask');
    if (old) old.remove();

    state.openId = it.id;
    var mask = app.el('div', 'mask dmask');
    var box = app.el('div', 'modal dmodal');

    // 头部：题号信息 + 关闭
    var headRow = app.el('div', 'dhead');
    var headTitle = app.el('span', 'qno', (it.qno || '(无题号)') + (it.qtype ? '　' + it.qtype : ''));
    headRow.appendChild(headTitle);
    var closeBtn = app.el('button', 'ghost dclose', '关闭');
    headRow.appendChild(closeBtn);
    box.appendChild(headRow);

    var boxInner = app.el('div', 'detail');
    boxInner.appendChild(app.el('div', 'section-title', '加载中…'));
    box.appendChild(boxInner);
    mask.appendChild(box);
    document.body.appendChild(mask);

    function close() { mask.remove(); }
    closeBtn.onclick = close;
    mask.onclick = function (e) { if (e.target === mask) close(); };

    app.api('/api/entry?id=' + encodeURIComponent(it.id)).then(function (d) {
      boxInner.textContent = '';
      // 题图（figure 块）里 @photo 指向本条的原图；exam-bank/ 开头的走 /api/image 代理
      var photoUrl = d.image_url ? app.withToken(d.image_url) : '';
      var ctx = { figureUrl: app.figureResolver(photoUrl) };
      var head = app.el('div', 'head');
      head.appendChild(app.el('span', 'qno', (d.qno || '') + '　' + (d.qtype || '') + '　' + (d.exam || '') + '　' + (d.date || '')));
      if (d.source) {
        var sc = app.el('span', 'chip', d.source);
        sc.style.marginLeft = '8px';
        head.appendChild(sc);
      }
      boxInner.appendChild(head);

      var grid = app.el('div', 'grid');
      boxInner.appendChild(grid);

      // 左：原图
      var left = app.el('div');
      if (d.image_url) {
        var img = document.createElement('img');
        // <img> 请求带不上 X-Mistake-Token，只能拼在 URL 上
        img.src = app.withToken(d.image_url);
        img.alt = '原图';
        left.appendChild(img);
      } else {
        left.appendChild(app.el('div', 'excerpt', '（无原图）'));
      }
      grid.appendChild(left);

      // 右：字段
      var right = app.el('div');
      var pm = d.parsed_md;
      var parsedOK = !!(pm && pm.parsed && pm.sections && pm.sections.length);
      var secs = parsedOK ? pm.sections.slice() : [];

      // 索引里手改过的错因要点覆盖 md 里解析出来的那一份
      if (d.analysis) {
        var ai = -1;
        for (var i = 0; i < secs.length; i++) {
          if (ANALYSIS_KEYS.indexOf(secs[i].key) >= 0) { ai = i; break; }
        }
        if (ai >= 0) {
          secs[ai] = { key: secs[ai].key, value: d.analysis };
        } else {
          var at = secs.length;
          for (var j = 0; j < secs.length; j++) {
            if (secs[j].key === '我的作答') { at = j + 1; break; }
          }
          secs.splice(at, 0, { key: '错因要点', value: d.analysis });
        }
      }

      function addField(k, v) {
        var f = app.el('div', 'field');
        f.appendChild(app.el('div', 'k', k));
        var box2 = app.el('div', 'v');
        f.appendChild(box2);
        right.appendChild(f);
        app.renderText(box2, v, ctx);
      }

      if (parsedOK) {
        if (pm.heading) right.appendChild(app.el('div', 'excerpt', pm.heading));
        secs.forEach(function (s) { addField(s.key, s.value); });
      } else {
        var f2 = app.el('div', 'field');
        f2.appendChild(app.el('div', 'k', '原文（未能按字段解析）'));
        var raw = app.el('div', 'raw');
        raw.textContent = (pm && pm.raw) || '';
        f2.appendChild(raw);
        right.appendChild(f2);
        if (raw.textContent) app.math(raw);
        if (d.analysis) addField('错因要点', d.analysis);
      }

      // 知识点与备注：存在索引里（不是 md 段落），有内容才显示（2026-10-13 老师要求补上）
      if ((d.tags || []).length) {
        var tf = app.el('div', 'field');
        tf.appendChild(app.el('div', 'k', '知识点'));
        var tv = app.el('div', 'v tchips');
        d.tags.forEach(function (tg) { tv.appendChild(app.el('span', 'chip', tg)); });
        tf.appendChild(tv);
        right.appendChild(tf);
      }
      if (d.note) addField('备注', d.note);

      grid.appendChild(right);

      boxInner.appendChild(actions(d, ctx));
      app.math(boxInner);
    }).catch(function (e) {
      boxInner.textContent = '';
      boxInner.appendChild(app.el('div', 'empty', '详情加载失败：' + e.message));
    });
  }

  // actions —— 查看态只给「编辑 / 删除」两个按钮；点了编辑才换成表单。
  // 之前是详情一展开就铺满输入框，看着像误入编辑模式，老师明确说不对。
  function actions(d, ctx) {
    var wrap = app.el('div', 'actions');
    var edit = app.el('button', 'primary', '编辑');
    var del = app.el('button', 'danger', '删除');
    wrap.appendChild(edit);
    wrap.appendChild(del);

    edit.onclick = function () {
      wrap.replaceWith(editor(d, ctx));
    };

    del.onclick = function () {
      if (!confirm('确认删除「' + (d.qno || d.id) + '」？索引里的这条记录会被移除（md 和原图保留）。')) return;
      app.api('/api/entry?id=' + encodeURIComponent(d.id), { method: 'DELETE' }).then(function () {
        app.toast('已删除');
        closeDetail();
        return reload('');
      }).catch(function (e) { app.toast('删除失败：' + e.message); });
    };

    return wrap;
  }

  // closeDetail —— 关掉当前打开的详情浮框（删除题目后用）
  function closeDetail() {
    var m = document.querySelector('.dmask');
    if (m) m.remove();
  }

  // mdAnalysis —— md 里解析出的错因要点，用作编辑框的初值
  function mdAnalysis(d) {
    var secs = (d.parsed_md && d.parsed_md.parsed && d.parsed_md.sections) || [];
    for (var i = 0; i < secs.length; i++) {
      if (ANALYSIS_KEYS.indexOf(secs[i].key) >= 0) return secs[i].value;
    }
    return '';
  }

  // reload —— 改完不整页刷新（一刷新就回到列表顶、浮框也没了）：
  // 重新拉一次列表，重画；传了 id 就把那道题的详情浮框重新打开（回到查看态）。
  function reload(id) {
    return app.api('/api/entries?subject=' + encodeURIComponent(state.subject)).then(function (d2) {
      state.items = d2.items || [];
      state.facets = d2.facets || {};
      repaint();
      if (!id) return;
      var hit = null;
      state.items.forEach(function (x) { if (x.id === id) hit = x; });
      if (hit) openDetail(hit);
    });
  }

  // frow —— 编辑表单的一行：左侧固定标签 + 右侧控件（老师点名要标签）
  function frow(label, ctrl, wide) {
    var r = app.el('div', 'frow' + (wide ? ' wide' : ''));
    r.appendChild(app.el('div', 'flabel', label));
    r.appendChild(ctrl);
    return r;
  }

  // mdSection —— md 里解析出的某个字段原文，用作编辑框初值
  function mdSection(d, keys) {
    var secs = (d.parsed_md && d.parsed_md.parsed && d.parsed_md.sections) || [];
    for (var i = 0; i < secs.length; i++) {
      if (keys.indexOf(secs[i].key) >= 0) return secs[i].value;
    }
    return '';
  }

  // mdEditor —— 题干/作答编辑器：文本框 + 一个「公式预览」按钮（公式与字符画都能看）
  function mdEditor(label, value, placeholder, ctx) {
    var wrap = app.el('div', 'mdeditor');
    var ta = document.createElement('textarea');
    ta.className = 'stem-input';
    ta.value = value || '';
    ta.placeholder = placeholder || '';
    wrap.appendChild(ta);
    var preview = app.el('div', 'import-math-preview');
    var btn = app.el('button', 'ghost import-preview', '公式预览');
    btn.onclick = function () { app.renderText(preview, ta.value, ctx); };
    wrap.appendChild(btn);
    wrap.appendChild(preview);
    return { node: wrap, input: ta };
  }

  function editor(d, ctx) {
    var form = app.el('div', 'editform');

    // 题干与我的作答存在 md 里，改它们会重写题目原文
    var stemBox = mdEditor('题干', mdSection(d, ['题干', '题目']),
      '公式用 $…$；图形用三个反引号围栏的字符画', ctx);
    form.appendChild(frow('题干', stemBox.node, true));

    // ---- 题图：模型画的字符画不满意时，直接在原图上框一块 ----
    var figBox = app.el('div', 'figbox');
    var figList = app.el('div', 'figlist');
    var figCropHost = app.el('div', 'figcropper');
    figBox.appendChild(figList);
    figBox.appendChild(figCropHost);
    form.appendChild(frow('题图', figBox, true));

    function paintFigures() {
      figList.textContent = '';
      var figs = crop.figures(stemBox.input.value);
      figs.forEach(function (fig, index) {
        figList.appendChild(crop.renderThumb(fig.ref, ctx.figureUrl, function () {
          stemBox.input.value = crop.removeAt(stemBox.input.value, index);
          paintFigures();
          app.toast('已移除题图（点「保存」后写入）');
        }));
      });
      if (figs.length) {
        figList.appendChild(app.el('div', 'hint', '共 ' + figs.length + ' 张题图，随题干一起保存。'));
      }
    }

    if (d.image_url) {
      figCropHost.appendChild(app.el('div', 'hint',
        '模型画的图不满意时，用下面这张原图框一块当题图：点「框选原图」→ 在图上拖一个矩形 → 松手即插入题干。'));
      crop.attach(figCropHost, {
        imageUrl: app.withToken(d.image_url),
        key: function () { return d.image_key || crop.PHOTO_ALIAS; },
        hint: '在图上拖一个矩形，松手即插入题干',
        onPick: function (ref) {
          stemBox.input.value = crop.upsert(stemBox.input.value, crop.block(ref));
          paintFigures();
          app.toast('已插入题图（点「保存」后写入）');
        }
      });
    } else {
      figCropHost.appendChild(app.el('div', 'hint', '（这条没有原图，无法框选）'));
    }
    paintFigures();

    var ansBox = mdEditor('我的作答', mdSection(d, ['我的作答', '作答', '我的答案']),
      '手写作答，多行照抄', ctx);
    form.appendChild(frow('我的作答', ansBox.node, true));

    var stem0 = stemBox.input.value;
    var ans0 = ansBox.input.value;

    // 没有「对错」这一项：错题本里全是要重做的，标对错没意义
    var exam = document.createElement('input');
    exam.type = 'text';
    exam.value = d.exam || '';
    exam.placeholder = '例如：第三章 有理数';
    form.appendChild(frow('章节', exam));

    var src = document.createElement('select');
    src.appendChild(opt('', '未标注'));
    SOURCES.forEach(function (sname) {
      var o = document.createElement('option');
      o.value = sname; o.textContent = sname;
      if (sname === (d.source || '')) o.selected = true;
      src.appendChild(o);
    });
    form.appendChild(frow('出处', src));

    var tags = document.createElement('input');
    tags.type = 'text';
    tags.value = (d.tags || []).join('、');
    tags.placeholder = '多个用 、 分隔';
    form.appendChild(frow('知识点', tags));

    var ana = document.createElement('textarea');
    ana.value = d.analysis || mdAnalysis(d);
    ana.placeholder = '留空则显示 md 里解析出的「错因/要点」原文；填了就覆盖它';
    form.appendChild(frow('错因要点', ana, true));

    var note = document.createElement('textarea');
    note.value = d.note || '';
    note.placeholder = '备注';
    form.appendChild(frow('备注', note, true));

    var btns = app.el('div', 'fbtns');
    var save = app.el('button', 'primary', '保存');
    var cancel = app.el('button', 'ghost', '取消');
    btns.appendChild(save);
    btns.appendChild(cancel);
    form.appendChild(btns);

    save.onclick = function () {
      var body = {
        id: d.id,
        exam: exam.value.trim(),
        source: src.value,
        analysis: ana.value,
        tags: tags.value.split(/[、,，\s]+/).filter(function (x) { return x; }),
        note: note.value
      };
      // 题干/作答只在实际改动时才提交，避免每次保存都重写 md
      if (stemBox.input.value !== stem0) body.stem = stemBox.input.value;
      if (ansBox.input.value !== ans0) body.student_answer = ansBox.input.value;
      app.api('/api/entry', { method: 'PUT', body: body }).then(function () {
        app.toast('已保存');
        return reload(d.id);
      }).catch(function (e) { app.toast('保存失败：' + e.message); });
    };

    cancel.onclick = function () {
      var hit = null;
      state.items.forEach(function (x) { if (x.id === d.id) hit = x; });
      if (hit) openDetail(hit); // 回到查看态
    };

    return form;
  }

  // ==========================================================================
  // 导出
  // ==========================================================================

  function stamp() {
    var d = new Date();
    function p(x) { return (x < 10 ? '0' : '') + x; }
    return '' + d.getFullYear() + p(d.getMonth() + 1) + p(d.getDate());
  }

  function openExport() {
    var cur = filtered();
    var mask = app.el('div', 'mask');
    var box = app.el('div', 'modal');

    box.appendChild(app.el('h3', null, '导出「' + state.subject + '」错题本'));

    // ---- 范围 ----
    var scope = document.createElement('select');
    scope.appendChild(opt('filtered', '当前筛选结果（' + cur.length + ' 题）'));
    scope.appendChild(opt('all', '本科目全部（' + state.items.length + ' 题）'));
    scope.appendChild(opt('manual', '手动选题（自己勾）'));
    box.appendChild(mrow('范围', scope));

    // ---- 手动选题清单（选了「手动选题」才展开）----
    var pickWrap = app.el('div', 'pickwrap');
    pickWrap.style.display = 'none';

    var pickBar = app.el('div', 'pickbar');
    var pickCount = app.el('span', 'pickcount', '已选 0 题');
    var allBtn = app.el('button', 'ghost', '全选');
    var noneBtn = app.el('button', 'ghost', '清空');
    pickBar.appendChild(pickCount);
    pickBar.appendChild(allBtn);
    pickBar.appendChild(noneBtn);
    pickWrap.appendChild(pickBar);

    var checks2 = {};
    var pickList = app.el('div', 'picklist');
    cur.forEach(function (it2) {
      var lab = document.createElement('label');
      lab.className = 'pickitem';
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = it2.id;
      lab.appendChild(cb);
      var txt = app.el('span', null,
        (it2.qno || '(无题号)') + (it2.qtype ? '　' + it2.qtype : '') + '　' +
        ((it2.preview || '').replace(/\s+/g, ' ').slice(0, 46) || '（无题干）'));
      lab.appendChild(txt);
      pickList.appendChild(lab);
      checks2[it2.id] = cb;
      cb.onchange = syncCount;
    });
    pickWrap.appendChild(pickList);
    if (!cur.length) {
      pickWrap.appendChild(app.el('div', 'hint', '当前筛选结果为空，先调整筛选或换「本科目全部」。'));
    }

    function syncCount() {
      var n = Object.keys(checks2).filter(function (k) { return checks2[k].checked; }).length;
      pickCount.textContent = '已选 ' + n + ' 题';
    }
    allBtn.onclick = function () {
      Object.keys(checks2).forEach(function (k) { checks2[k].checked = true; });
      syncCount();
    };
    noneBtn.onclick = function () {
      Object.keys(checks2).forEach(function (k) { checks2[k].checked = false; });
      syncCount();
    };
    scope.onchange = function () {
      pickWrap.style.display = scope.value === 'manual' ? '' : 'none';
    };
    box.appendChild(mrow('题目', pickWrap, true));

    // ---- 格式 ----
    var fmt = document.createElement('select');
    FORMATS.forEach(function (f) { fmt.appendChild(opt(f[0], f[1])); });
    box.appendChild(mrow('格式', fmt));

    // ---- 每页题数（只对 HTML 排版卷生效）----
    var pp = document.createElement('select');
    [['1', '每页 1 道（重做留白最多）'], ['2', '每页 2 道'], ['3', '每页 3 道']].forEach(function (p) {
      pp.appendChild(opt(p[0], p[1]));
    });
    pp.disabled = fmt.value === 'md';
    fmt.onchange = function () { pp.disabled = fmt.value === 'md'; };
    box.appendChild(mrow('每页题数', pp));

    // ---- 内容 ----
    var checks = {};
    var grid = app.el('div', 'checks');
    EXPORT_FIELDS.forEach(function (f) {
      var lab = document.createElement('label');
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.checked = DEFAULT_FIELDS.indexOf(f[0]) >= 0;
      lab.appendChild(cb);
      lab.appendChild(document.createTextNode(f[1]));
      grid.appendChild(lab);
      checks[f[0]] = cb;
    });
    box.appendChild(mrow('导出内容', grid, true));

    var hint = app.el('div', 'hint',
      '排版：元数据做成表格，题干放在大块里、下方留白给你重做，备注在最下面。「我的作答 / 错因要点 / 答案正解 / 原图」属于参考内容，勾了才出现在题干块下面的参考区里。' +
      '打印：导出的 HTML 右下角有「打印」按钮，点它 → 在打印对话框里选「另存为 PDF」，不会自动弹打印。' +
      '原图：Markdown 放 7 天有效的 COS 链接，HTML 直接内嵌（文件会变大）。');
    box.appendChild(hint);

    var err = app.el('div', 'hint err', '');
    box.appendChild(err);

    // ---- 按钮 ----
    var foot = app.el('div', 'mfoot');
    var cancel = app.el('button', 'ghost', '取消');
    var go = app.el('button', 'primary', '导出');
    foot.appendChild(cancel);
    foot.appendChild(go);
    box.appendChild(foot);

    function close() { mask.remove(); }
    cancel.onclick = close;
    mask.onclick = function (e) { if (e.target === mask) close(); };

    go.onclick = function () {
      var fields = Object.keys(checks).filter(function (k) { return checks[k].checked; });
      if (!fields.length) { err.textContent = '至少勾一项导出内容'; return; }
      var ids = null;
      if (scope.value === 'filtered') {
        ids = cur.map(function (it) { return it.id; });
        if (!ids.length) { err.textContent = '当前筛选结果为空'; return; }
      } else if (scope.value === 'manual') {
        ids = Object.keys(checks2).filter(function (k) { return checks2[k].checked; });
        if (!ids.length) { err.textContent = '手动选题：至少勾一道题'; return; }
      } else if (!state.items.length) {
        err.textContent = '本科目没有题目'; return;
      }

      err.textContent = '';
      go.disabled = true;
      go.textContent = '生成中…';

      app.apiRaw('/api/export', {
        method: 'POST',
        body: {
          subject: state.subject,
          ids: ids,
          format: fmt.value,
          per_page: parseInt(pp.value, 10) || 1,
          fields: fields,
          with_image: fields.indexOf('image') >= 0
        }
      }).then(function (r) { return r.blob(); }).then(function (blob) {
        close();
        var n = ids ? ids.length : state.items.length;
        app.download(blob, '错题本-' + state.subject + '-' + stamp() + '.' + (fmt.value === 'md' ? 'md' : 'html'));
        app.toast('已导出 ' + n + ' 题' + (fmt.value === 'html' ? '，打开文件后点右下角「打印」可另存为 PDF' : ''));
      }).catch(function (e) {
        go.disabled = false;
        go.textContent = '导出';
        err.textContent = '导出失败：' + e.message;
      });
    };

    mask.appendChild(box);
    document.body.appendChild(mask);
  }

  // mrow —— 弹窗里的一行：左侧标签 + 右侧控件
  function mrow(label, ctrl, wide) {
    var r = app.el('div', 'mfield' + (wide ? ' wide' : ''));
    r.appendChild(app.el('div', 'mlabel', label));
    r.appendChild(ctrl);
    return r;
  }

  return { render: render };
})();
window.views = views;
