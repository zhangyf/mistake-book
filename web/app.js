// app.js —— 全局：token 管理 / API 封装 / hash 路由 / 通用工具
var app = (function () {

  var TOKEN_KEY = 'mistake_token';

  function token() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function setToken(t) {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
    renderTokenState();
  }

  function renderTokenState() {
    var el = document.getElementById('token-state');
    if (el) el.textContent = token() ? '已设置 token' : '未设置 token';
  }

  function askToken() {
    var cur = token();
    var t = prompt('输入访问 token（服务端 --auth-token，留空表示不鉴权）', cur);
    if (t === null) return;
    setToken(t.trim());
  }

  // api —— 统一带 Authorization 头；401 时提示重设 token
  function api(path, opts) {
    opts = opts || {};
    var headers = opts.headers || {};
    if (token()) headers['Authorization'] = 'Bearer ' + token();
    if (opts.body) headers['Content-Type'] = 'application/json';
    return fetch(path, {
      method: opts.method || 'GET',
      headers: headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined
    }).then(function (r) {
      if (r.status === 401) {
        toast('token 不对，请重设');
        askToken();
        throw new Error('unauthorized');
      }
      return r.json().then(function (d) {
        if (!r.ok) throw new Error(d && d.error ? d.error : ('HTTP ' + r.status));
        return d;
      });
    });
  }

  // ---------- DOM 工具 ----------
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  // renderText —— 把一段纯文本渲染成段落/列表，再跑 KaTeX。
  // 只用 textContent 塞内容，绝不 innerHTML（题干来自 md 原文，防注入）。
  function renderText(container, text) {
    container.textContent = '';
    var lines = (text || '').split('\n');
    var ul = null;
    lines.forEach(function (line) {
      var t = line.trim();
      if (/^[-*]\s+/.test(t)) {
        if (!ul) { ul = el('ul'); container.appendChild(ul); }
        ul.appendChild(el('li', null, t.replace(/^[-*]\s+/, '')));
      } else {
        ul = null;
        if (t) container.appendChild(el('p', null, line));
      }
    });
    math(container);
  }

  function math(node) {
    if (window.renderMathInElement) {
      try {
        window.renderMathInElement(node, {
          delimiters: [
            { left: '$$', right: '$$', display: true },
            { left: '$', right: '$', display: false },
            { left: '\\[', right: '\\]', display: true },
            { left: '\\(', right: '\\)', display: false }
          ],
          throwOnError: false
        });
      } catch (e) { /* 单个公式出错不阻断整页 */ }
    }
  }

  var toastTimer = null;
  function toast(msg) {
    var t = document.getElementById('toast');
    t.textContent = msg;
    t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove('show'); }, 2200);
  }

  // ---------- 路由 ----------
  function route() {
    var h = decodeURIComponent(location.hash || '#/');
    var main = document.getElementById('app');
    var crumb = document.getElementById('crumb');
    main.textContent = '';
    var m = h.match(/^#\/subject\/(.+)$/);
    if (m) {
      crumb.textContent = '› ' + m[1];
      views.subject.render(main, m[1]);
    } else {
      crumb.textContent = '';
      views.home.render(main);
    }
  }

  function boot() {
    renderTokenState();
    document.getElementById('btn-token').onclick = askToken;
    window.addEventListener('hashchange', route);
    if (!location.hash) location.hash = '#/';
    route();
  }

  return { boot: boot, api: api, el: el, renderText: renderText, toast: toast, setToken: setToken };
})();
