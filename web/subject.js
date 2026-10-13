// subject.js —— 某科目的错题列表：维度切换 + 筛选 + 详情 + 编辑/删除
views.subject = (function () {

  var state = {
    subject: '',
    items: [],
    facets: {},
    dim: 'date',        // date | tag | type | result
    filterResult: '',
    filterTags: [],
    kw: '',
    openId: ''
  };

  var DIM_LABEL = { date: '按日期', tag: '按知识点', type: '按题型', result: '按对错' };

  function render(main, subject) {
    state.subject = subject;
    state.items = []; state.facets = {}; state.filterTags = []; state.filterResult = '';
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

    var sel = document.createElement('select');
    sel.appendChild(opt('', '全部对错'));
    sel.appendChild(opt('wrong', '只看错的'));
    sel.appendChild(opt('correct', '只看对的'));
    sel.appendChild(opt('unknown', '未判定'));
    sel.onchange = function () { state.filterResult = sel.value; paintList(main); };
    bar.appendChild(sel);

    var search = document.createElement('input');
    search.type = 'text';
    search.className = 'search';
    search.placeholder = '搜索题干/备注/知识点';
    search.oninput = function () { state.kw = search.value.trim().toLowerCase(); paintList(main); };
    bar.appendChild(search);

    var tagBar = app.el('div', 'toolbar');
    main.appendChild(tagBar);
    var listWrap = app.el('div');
    main.appendChild(listWrap);
    var detailWrap = app.el('div');
    main.appendChild(detailWrap);

    listWrap.appendChild(app.el('div', 'empty', '加载中…'));

    app.api('/api/entries?subject=' + encodeURIComponent(subject)).then(function (d) {
      state.items = d.items || [];
      state.facets = d.facets || {};
      paintTags(tagBar, main, listWrap);
      paintList(main, listWrap, detailWrap);
    }).catch(function (e) {
      listWrap.textContent = '';
      listWrap.appendChild(app.el('div', 'empty', '加载失败：' + e.message));
    });

    // paintList 需要拿到这三个容器
    main._listWrap = listWrap;
    main._detailWrap = detailWrap;
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
    if (main && main._listWrap) paintList(main, main._listWrap, main._detailWrap);
  }

  function filtered() {
    return state.items.filter(function (it) {
      if (state.filterResult && it.result !== state.filterResult) return false;
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
      case 'result': return { wrong: '错了的', correct: '做对的', unknown: '未判定' }[it.result] || '未判定';
      case 'tag': return (it.tags && it.tags.length) ? it.tags.join('、') : '未打标签';
      default: return (it.date || '').slice(0, 7) || '无日期';
    }
  }

  function paintList(main, listWrap, detailWrap) {
    if (!listWrap) { listWrap = main._listWrap; detailWrap = main._detailWrap; }
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
      groups[k].forEach(function (it) { g.appendChild(itemRow(it, detailWrap)); });
      listWrap.appendChild(g);
    });
  }

  function itemRow(it, detailWrap) {
    var row = app.el('div', 'item');
    var head = app.el('div', 'head');
    var dot = app.el('span', 'result-dot ' + (it.result || 'unknown'));
    head.appendChild(dot);
    head.appendChild(app.el('span', 'qno', it.qno || '(无题号)'));
    if (it.qtype) head.appendChild(app.el('span', 'qtype', it.qtype));
    head.appendChild(app.el('span', 'qtype', it.date || ''));
    (it.tags || []).forEach(function (t) { head.appendChild(app.el('span', 'chip', t)); });
    row.appendChild(head);

    var excerpt = it.note || '';
    if (excerpt) row.appendChild(app.el('div', 'excerpt', excerpt));

    row.onclick = function () { openDetail(it, detailWrap); };
    return row;
  }

  function openDetail(it, detailWrap) {
    detailWrap.textContent = '';
    state.openId = it.id;
    var box = app.el('div', 'detail');
    box.appendChild(app.el('div', 'section-title', '加载中…'));
    detailWrap.appendChild(box);

    app.api('/api/entry?id=' + encodeURIComponent(it.id)).then(function (d) {
      box.textContent = '';
      var head = app.el('div', 'head');
      var dot = app.el('span', 'result-dot ' + (d.result || 'unknown'));
      dot.style.marginRight = '8px';
      head.appendChild(dot);
      head.appendChild(app.el('span', 'qno', (d.qno || '') + '　' + (d.qtype || '') + '　' + (d.exam || '') + '　' + (d.date || '')));
      box.appendChild(head);

      var grid = app.el('div', 'grid');
      box.appendChild(grid);

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
      if (pm && pm.parsed && pm.sections && pm.sections.length) {
        if (pm.heading) right.appendChild(app.el('div', 'excerpt', pm.heading));
        pm.sections.forEach(function (s) {
          var f = app.el('div', 'field');
          f.appendChild(app.el('div', 'k', s.key));
          var v = app.el('div', 'v');
          f.appendChild(v);
          right.appendChild(f);
          app.renderText(v, s.value);
        });
      } else {
        var f2 = app.el('div', 'field');
        f2.appendChild(app.el('div', 'k', '原文（未能按字段解析）'));
        var raw = app.el('div', 'raw');
        raw.textContent = (pm && pm.raw) || '';
        f2.appendChild(raw);
        right.appendChild(f2);
        if (raw.textContent) app.math(raw);
      }
      grid.appendChild(right);

      box.appendChild(editor(d));
      app.math(box);
    }).catch(function (e) {
      box.textContent = '';
      box.appendChild(app.el('div', 'empty', '详情加载失败：' + e.message));
    });
  }

  function editor(d) {
    var wrap = app.el('div', 'editrow');

    var sel = document.createElement('select');
    [['correct', '做对了'], ['wrong', '做错了'], ['unknown', '未判定']].forEach(function (p) {
      var o = document.createElement('option');
      o.value = p[0]; o.textContent = p[1];
      if (p[0] === (d.result || 'unknown')) o.selected = true;
      sel.appendChild(o);
    });
    wrap.appendChild(sel);

    var tags = document.createElement('input');
    tags.type = 'text';
    tags.value = (d.tags || []).join('、');
    tags.placeholder = '知识点，用、分隔';
    tags.style.minWidth = '220px';
    wrap.appendChild(tags);

    var save = app.el('button', 'primary', '保存');
    wrap.appendChild(save);

    var del = app.el('button', 'danger', '删除');
    wrap.appendChild(del);

    var note = document.createElement('textarea');
    note.value = d.note || '';
    note.placeholder = '备注 / 错因';
    wrap.appendChild(note);

    save.onclick = function () {
      var body = {
        id: d.id,
        result: sel.value,
        tags: tags.value.split(/[、,，\s]+/).filter(function (x) { return x; }),
        note: note.value
      };
      app.api('/api/entry', { method: 'PUT', body: body }).then(function () {
        app.toast('已保存');
        location.reload();
      }).catch(function (e) { app.toast('保存失败：' + e.message); });
    };

    del.onclick = function () {
      if (!confirm('确认删除「' + (d.qno || d.id) + '」？索引里的这条记录会被移除（md 和原图保留）。')) return;
      app.api('/api/entry?id=' + encodeURIComponent(d.id), { method: 'DELETE' }).then(function () {
        app.toast('已删除');
        location.reload();
      }).catch(function (e) { app.toast('删除失败：' + e.message); });
    };

    return wrap;
  }

  return { render: render };
})();
window.views = views;
