// app.js —— 全局：token 管理 / API 封装 / hash 路由 / 通用工具
var app = (function () {

  var TOKEN_KEY = 'mistake_token';

  function token() { return localStorage.getItem(TOKEN_KEY) || ''; }

  // 带 token 的一次性链接：?token=xxx 或 #/...&token=xxx，点了就自动存好，
  // 不用手工粘贴。（存完立刻从地址栏抹掉，避免留在历史记录里）
  function captureTokenFromURL() {
    var t = new URLSearchParams(location.search).get('token');
    if (!t) {
      var m = location.hash.match(/[?&]token=([^&]+)/);
      if (m) t = decodeURIComponent(m[1]);
    }
    if (!t) return false;
    setToken(t);
    var url = location.pathname + (location.hash ? location.hash.split(/[?&]token=/)[0] : '');
    history.replaceState(null, '', url || '/');
    return true;
  }

  function setToken(t) {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
    renderTokenState();
  }

  function renderTokenState() {
    var el = document.getElementById('token-state');
    if (el) el.textContent = token() ? '已设置 token' : '未设置 token';
  }

  // 一次会话只问一次：401 每次都弹窗的话，点一道题弹一次，根本没法用。
  var asked = false;
  function askToken() {
    if (asked) return;
    asked = true;
    var t = prompt('输入访问 token（服务端 --auth-token 的值，不是网页登录密码）', token());
    if (t === null) return;
    setToken(t.trim());
  }

  // api —— 统一带 Authorization 头；401 时提示重设 token
  function api(path, opts) {
    opts = opts || {};
    var headers = opts.headers || {};
    // 必须用自定义头：线上前面还有 nginx Basic Auth，占用了 Authorization 头
    if (token()) headers['X-Mistake-Token'] = token();
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

  // apiRaw —— 导出用：要的是原始字节（md/html），不是 JSON
  function apiRaw(path, opts) {
    opts = opts || {};
    var headers = opts.headers || {};
    if (token()) headers['X-Mistake-Token'] = token();
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
      if (!r.ok) {
        return r.json().catch(function () { return {}; }).then(function (d) {
          throw new Error((d && d.error) || ('HTTP ' + r.status));
        });
      }
      return r;
    });
  }

  // download —— Blob → <a download> 触发保存
  function download(blob, filename) {
    var url = URL.createObjectURL(blob);
    var a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    setTimeout(function () { URL.revokeObjectURL(url); a.remove(); }, 3000);
  }

  // printHTML —— 把导出的 HTML 塞进隐藏 iframe，页面自己会调 window.print()。
  // 走 iframe 而不是 window.open，是为了不被弹窗拦截。
  function printHTML(blob) {
    var url = URL.createObjectURL(blob);
    var f = document.createElement('iframe');
    f.style.cssText = 'position:fixed;right:0;bottom:0;width:0;height:0;border:0;';
    f.src = url;
    document.body.appendChild(f);
    setTimeout(function () {
      URL.revokeObjectURL(url);
      f.remove();
    }, 120000);
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
    captureTokenFromURL();
    renderTokenState();
    document.getElementById('btn-token').onclick = askToken;
    window.addEventListener('hashchange', route);
    if (!location.hash) location.hash = '#/';
    route();
  }

  return {
    boot: boot, api: api, apiRaw: apiRaw, el: el, renderText: renderText, math: math,
    toast: toast, setToken: setToken, token: token,
    download: download, printHTML: printHTML,
    // <img> 没法带自定义头，只能把 token 拼进 URL（后端也接受 ?token=）
    withToken: function (url) {
      var t = token();
      if (!t) return url;
      return url + (url.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(t);
    }
  };
})();
