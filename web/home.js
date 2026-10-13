// home.js —— 集中页：各科错题本卡片
var views = window.views || {};
views.home = (function () {

  function render(main) {
    var title = app.el('h2', 'section-title', '我的错题本');
    main.appendChild(title);

    var wrap = app.el('div', 'cards');
    main.appendChild(wrap);
    wrap.appendChild(app.el('div', 'empty', '加载中…'));

    app.api('/api/subjects').then(function (d) {
      wrap.textContent = '';
      var list = d.subjects || [];
      if (!list.length) {
        wrap.appendChild(app.el('div', 'empty', '还没有录入任何错题'));
        return;
      }
      list.forEach(function (s) { wrap.appendChild(card(s)); });
    }).catch(function (e) {
      wrap.textContent = '';
      wrap.appendChild(app.el('div', 'empty', '加载失败：' + e.message));
    });
  }

  function card(s) {
    var c = app.el('div', 'card');

    // 标题 = 老师自己起的名字（没起就显示科目名）+ 右上角「改名」
    var head = app.el('div', 'cardhead');
    head.appendChild(app.el('div', 'name', s.name || s.subject));
    var pen = app.el('button', 'ghost pen', '改名');
    pen.title = '给这个错题本起个名字';
    pen.onclick = function (e) {
      e.stopPropagation();
      var v = prompt('给「' + s.subject + '」这个错题本起个名字（留空恢复默认显示科目名）', s.name || s.subject);
      if (v === null) return;
      app.api('/api/subject', { method: 'PUT', body: { subject: s.subject, name: v.trim() } })
        .then(function () {
          app.toast('已保存');
          render(document.getElementById('app'));
        })
        .catch(function (e2) { app.toast('保存失败：' + e2.message); });
    };
    head.appendChild(pen);
    c.appendChild(head);

    c.appendChild(app.el('div', 'count', s.count + ' 题 · 最近 ' + s.last_date));

    // 下面这行是这科错题本里出现过的章节（原来还带「对/错」计数，已去掉）
    if (s.exams && s.exams.length) {
      c.appendChild(app.el('div', 'exams', '章节：' + s.exams.join(' / ')));
    }
    c.onclick = function () { location.hash = '#/subject/' + encodeURIComponent(s.subject); };
    return c;
  }

  return { render: render };
})();
window.views = views;
