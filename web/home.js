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
    c.appendChild(app.el('div', 'name', s.subject));
    c.appendChild(app.el('div', 'count', s.count + ' 题' + (s.last_date ? ' · 最近 ' + s.last_date : '')));

    var chips = app.el('div', 'chips');
    var r = s.results || {};
    ['wrong', 'correct', 'unknown'].forEach(function (k) {
      if (!r[k]) return;
      var label = { wrong: '错', correct: '对', unknown: '未判定' }[k];
      chips.appendChild(app.el('span', 'chip ' + k, label + ' ' + r[k]));
    });
    c.appendChild(chips);

    if (s.exams && s.exams.length) {
      c.appendChild(app.el('div', 'exams', s.exams.join(' / ')));
    }
    c.onclick = function () { location.hash = '#/subject/' + encodeURIComponent(s.subject); };
    return c;
  }

  return { render: render };
})();
window.views = views;
