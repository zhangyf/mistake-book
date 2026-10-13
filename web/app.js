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

  function requestJSON(path, opts, multipart) {
    opts = opts || {};
    var headers = {};
    Object.keys(opts.headers || {}).forEach(function (key) {
      // multipart 的 boundary 只能由浏览器生成。
      if (!multipart || key.toLowerCase() !== 'content-type') headers[key] = opts.headers[key];
    });
    if (token()) headers['X-Mistake-Token'] = token();
    if (!multipart && opts.body) headers['Content-Type'] = 'application/json';
    return fetch(path, {
      method: opts.method || (multipart ? 'POST' : 'GET'),
      headers: headers,
      body: multipart ? opts.body : (opts.body ? JSON.stringify(opts.body) : undefined),
      signal: opts.signal
    }).then(function (r) {
      function responseError(message, serverRejected) {
        var error = new Error(message);
        error.status = r.status;
        // 非 JSON 代理响应不能当成后端明确的写入前校验失败。
        error.serverRejected = !!serverRejected;
        return error;
      }
      if (r.status === 401) {
        toast('token 不对，请重设');
        askToken();
        throw responseError('访问凭证无效，请重设 token 后重试');
      }
      return r.json().catch(function (e) {
        if (e.name === 'AbortError') throw e;
        throw responseError('服务返回了非 JSON 响应（HTTP ' + r.status + '），请检查服务或代理后重试');
      }).then(function (d) {
        if (!r.ok || (d && d.success === false)) {
          throw responseError(d && typeof d.error === 'string' ? d.error : ('请求失败（HTTP ' + r.status + '）'),
            d && d.success === false && typeof d.error === 'string');
        }
        return d;
      });
    }).catch(function (e) {
      if (e.name === 'TypeError') throw new Error('网络连接失败，请检查连接后重试');
      throw e;
    });
  }

  function api(path, opts) { return requestJSON(path, opts, false); }
  function apiForm(path, opts) { return requestJSON(path, opts, true); }

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

  // ---------- DOM 工具 ----------
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  // renderText —— 把一段纯文本渲染成段落/列表/字符画代码块，再跑 KaTeX。
  // 只用 textContent 塞内容，绝不 innerHTML（题干来自 md 原文，防注入）。
  // 三个反引号独占一行的围栏块按字符画处理：<pre> 等宽保留空格。
  // 声明为 figure 且能解析的块（老师在原图上框选的题图）渲染成按框裁好的图；
  // 解析不出、或 ctx.figureUrl 解不出地址，就退回字符画显示，绝不吞内容。
  function renderText(container, text, ctx) {
    container.textContent = '';
    var lines = (text || '').split('\n');
    var ul = null;
    var pre = null;
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];
      if (pre !== null) {
        if (/^\s*```/.test(line)) {
          container.appendChild(pre);
          pre = null;
        } else {
          pre.appendChild(document.createTextNode(line + '\n'));
        }
        continue;
      }
      var t = line.trim();
      if (/^```/.test(t)) {
        if (window.crop && crop.isFigureFence(t)) {
          var end = -1;
          for (var j = i + 1; j < lines.length; j++) {
            if (/^\s*```/.test(lines[j])) { end = j; break; }
          }
          var ref = crop.refOf(lines.slice(i + 1, end >= 0 ? end : lines.length));
          if (ref) {
            var figure = ctx && typeof ctx.figureUrl === 'function'
              ? crop.renderFigure(ref, ctx.figureUrl) : null;
            if (figure) {
              ul = null;
              container.appendChild(figure);
              i = end >= 0 ? end : lines.length - 1;
              continue;
            }
          }
        }
        ul = null;
        pre = document.createElement('pre');
        pre.className = 'fig';
        continue;
      }
      if (/^[-*]\s+/.test(t)) {
        if (!ul) { ul = el('ul'); container.appendChild(ul); }
        ul.appendChild(el('li', null, t.replace(/^[-*]\s+/, '')));
      } else {
        ul = null;
        if (t) container.appendChild(el('p', null, line));
      }
    }
    if (pre) container.appendChild(pre); // 未闭合围栏也照常显示，别吞内容
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
  var activeHash = null;
  var activeView = null;
  function route() {
    var nextHash = location.hash || '#/';
    if (nextHash === activeHash) return;
    // 必须先问再销毁；取消跳转只还原 URL，不重建导入 DOM。
    if (activeView && activeView.canLeave && !activeView.canLeave()) {
      history.replaceState(null, '', location.pathname + location.search + activeHash);
      return;
    }
    if (activeView && activeView.destroy) activeView.destroy();
    activeView = null;
    activeHash = nextHash;
    var h;
    try { h = decodeURIComponent(nextHash); } catch (e) { h = '#/'; }
    var main = document.getElementById('app');
    var crumb = document.getElementById('crumb');
    main.textContent = '';
    var imp = h.match(/^#\/import(?:\/(.+))?$/);
    var m = h.match(/^#\/subject\/(.+)$/);
    if (imp) {
      crumb.textContent = '› 导入照片';
      activeView = views.import.render(main, imp[1] || '');
    } else if (m) {
      crumb.textContent = '› ' + m[1];
      views.subject.render(main, m[1]);
    } else {
      crumb.textContent = '';
      views.home.render(main);
      var entry = el('div', 'toolbar home-import');
      var button = el('button', 'primary', '导入照片');
      button.onclick = function () { location.hash = '#/import'; };
      entry.appendChild(button);
      main.appendChild(entry);
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

  // withToken —— <img> 没法带自定义头，只能把 token 拼进 URL（后端也接受 ?token=）
  function withToken(url) {
    var t = token();
    if (!t) return url;
    return url + (url.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(t);
  }

  // figureResolver —— 题图块的 key → 可用的图片地址。
  // @photo 指本 md 对应的原图（详情页从 image_url 拿）；exam-bank/ 开头的是
  // 错题本里的对象，走后端代理 /api/image（凭证不出后端）。
  function figureResolver(photoUrl) {
    return function (key) {
      if (key === '@photo') return photoUrl || '';
      if (/^exam-bank\//i.test(key)) return withToken('/api/image?key=' + encodeURIComponent(key));
      return '';
    };
  }

  return {
    boot: boot, api: api, apiForm: apiForm, apiRaw: apiRaw, el: el, renderText: renderText, math: math,
    toast: toast, setToken: setToken, token: token,
    download: download, withToken: withToken, figureResolver: figureResolver
  };
})();
