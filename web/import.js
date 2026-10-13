// import.js —— 当前页照片草稿；识别与确认入库严格分离。
views.import = (function () {
  var SOURCES = ['校内作业', '课外班作业', '校内考试', '自我复习'];
  var STATUS = {
    pending: '待识别', queued: '待识别（排队中）', recognizing: '识别中',
    review: '待核对', empty: '无保留题目（需重新识别或移除照片）', stale: '待识别（题号已变更）', confirming: '入库中',
    success: '成功（已入库）', failed: '失败（可重试识别）',
    uncertain: '失败（入库结果不确定）', dry: '演练完成（未入库）'
  };

  function localDate() {
    var d = new Date();
    function pad(n) { return n < 10 ? '0' + n : '' + n; }
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  }

  function validDate(value) {
    if (value.length !== 10 || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
    var parts = value.split('-').map(Number);
    var year = parts[0], month = parts[1], day = parts[2];
    var leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
    var days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    return year >= 1 && month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1];
  }

  function textError(value, label, max, required, singleLine) {
    if (required && !value.trim()) return label + '不能为空';
    // 按 Unicode 码点计数，与服务端 Go rune 一致，不使用 UTF-16 的 length。
    if (Array.from(value).length > max) return label + '最多 ' + max + ' 个字符';
    if (/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/.test(value)) return label + '含不支持的控制字符';
    if (singleLine && /[\r\n\u2028\u2029]/.test(value)) return label + '不能包含换行';
    return '';
  }

  function normalizedQno(value) {
    // 与服务端保持一致：去 Unicode 空白、全角 ASCII 转半角、统一简单数字题号。
    var qno = value.replace(/[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/g, '')
      .replace(/[\uff01-\uff5e]/g, function (c) { return String.fromCharCode(c.charCodeAt(0) - 0xfee0); });
    var sub = qno.match(/^(?:第)?([0-9]+)(?:题)?(?:第)?\(([0-9]+)\)(?:小问)?$/);
    if (sub) return '第' + (sub[1].replace(/^0+/, '') || '0') + '题(' + (sub[2].replace(/^0+/, '') || '0') + ')';
    var match = qno.match(/^(?:第)?([0-9]+)(?:题)?[.、]?$/);
    return match ? '第' + (match[1].replace(/^0+/, '') || '0') + '题' : qno;
  }

  function tagsFromText(value) { return value.split(/[、,，\s]+/).filter(function (t) { return t; }); }

  function importID() {
    var bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    return Array.prototype.map.call(bytes, function (b) { return ('0' + b.toString(16)).slice(-2); }).join('');
  }

  function render(main, initialSubject) {
    var alive = true, photos = [], queue = [], active = 0, confirming = false;
    var batchLocked = false, dirty = false, subjects = [];
    var config = null, configLoading = false;
    var maxPhotos = 20, maxBytes = 10485760, maxQuestions = 30;
    var pageController = new AbortController();
    var batch = { subject: initialSubject || '', date: localDate(), chapter: '', source: '' };
    var root = app.el('section', 'import-page');
    main.appendChild(root);
    root.appendChild(app.el('h2', null, '批量导入照片'));
    root.appendChild(app.el('p', 'hint', '草稿仅保存在当前页，不会自动入库。选择照片 → 填写每图题号 → 识别 → 对照原图编辑 → 勾选核对 → 逐图确认入库。'));
    var configInfo = app.el('p', 'hint import-config');
    configInfo.setAttribute('role', 'status');
    root.appendChild(configInfo);
    var configRetry = button('重新读取配置', 'ghost', loadConfig);
    root.appendChild(configRetry);
    var metadata = app.el('fieldset', 'import-metadata');
    metadata.appendChild(app.el('legend', null, '本批信息'));
    root.appendChild(metadata);
    var batchInputs = {};
    ['subject', 'date', 'chapter', 'source'].forEach(function (key) {
      var labels = { subject: '科目（可输入新科目）', date: '日期', chapter: '章节（可空）', source: '出处（可空，四选一）' };
      var control = key === 'source' ? sourceSelect() : app.el('input');
      if (key !== 'source') control.type = key === 'date' ? 'date' : 'text';
      control.name = key;
      control.value = batch[key];
      if (key === 'subject') {
        control.setAttribute('list', 'import-subjects');
        control.autocomplete = 'off';
        control.placeholder = '例如：数学';
      }
      control[key === 'source' ? 'onchange' : 'oninput'] = function () {
        if (batchLocked || confirming || !alive) return;
        batch[key] = control.value;
        dirty = true;
        photos.forEach(function (p) { p.reviewed = false; });
        updateSubjectName();
        refresh();
      };
      batchInputs[key] = control;
      metadata.appendChild(field(labels[key], control));
    });
    var subjectList = app.el('datalist');
    subjectList.id = 'import-subjects';
    root.appendChild(subjectList);
    var subjectName = app.el('p', 'hint import-subject-name');
    root.appendChild(subjectName);
    var metadataHint = app.el('p', 'hint', '识别时将批次章节/出处填入每题，可逐题修改；之后调整批次章节/出处不会覆盖已有题目。首次确认后本批信息锁定。');
    root.appendChild(metadataHint);

    var picker = app.el('input', 'import-picker');
    picker.type = 'file';
    picker.name = 'photos';
    picker.multiple = true;
    picker.accept = 'image/jpeg,image/png';
    picker.onchange = function () {
      addPhotos(picker.files || []);
      picker.value = '';
    };
    root.appendChild(field('照片（JPEG / PNG，每批最多 20 张、每张最多 10 MiB）', picker));
    var error = app.el('p', 'hint err import-error');
    error.setAttribute('role', 'alert');
    root.appendChild(error);
    var toolbar = app.el('div', 'toolbar');
    var start = button('开始识别', 'primary import-start', function () {
      if (confirming || !enabled()) return;
      // 全批操作只处理未识别/失败项，不覆盖任何已编辑的草稿。
      photos.slice().forEach(function (p) {
        if ((p.status === 'pending' || p.status === 'failed' || p.status === 'stale') && !(p.questions.length && p.edited)) enqueue(p, false);
      });
    });
    toolbar.appendChild(start);
    var back = button('返回列表', 'ghost import-back', function () {
      if (confirming) { app.toast('正在确认入库，请等待结果；勿重复新建批次。'); return; }
      location.hash = batch.subject.trim() ? '#/subject/' + encodeURIComponent(batch.subject.trim()) : '#/';
    });
    toolbar.appendChild(back);
    root.appendChild(toolbar);
    root.appendChild(app.el('p', 'hint', '“开始识别”最多同时处理 2 张，仅处理待识别/失败照片；已有编辑的草稿请逐图重识别。失败不会自动重试，手动识别可能产生费用。确认入库范围仅为当前照片，不会提交其他照片。'));
    var summary = app.el('p', 'import-summary');
    summary.setAttribute('aria-live', 'polite');
    root.appendChild(summary);
    var navigationWarning = app.el('p', 'hint import-navigation-warning');
    root.appendChild(navigationWarning);
    // 该提醒常驻，而不是仅在模型报告疑点时显示。
    root.appendChild(app.el('p', 'hint import-caution', '无疑点不代表无错误，注意负号/分数/指数。请对照原图核对每题题号、选项与作答。'));
    var cards = app.el('div', 'import-cards');
    root.appendChild(cards);

    function button(text, cls, fn) {
      var b = app.el('button', cls, text);
      b.type = 'button';
      b.onclick = fn;
      return b;
    }

    function field(label, control) {
      var row = app.el('label', 'import-field');
      row.appendChild(app.el('span', 'import-label', label));
      row.appendChild(control);
      return row;
    }

    function sourceSelect() {
      var select = app.el('select');
      [''].concat(SOURCES).forEach(function (s) {
        var o = app.el('option', null, s || '未标注');
        o.value = s;
        select.appendChild(o);
      });
      return select;
    }

    function enabled() { return !!(config && config.enabled === true); }
    function exists(p) { return alive && photos.indexOf(p) !== -1; }
    function locked(p) {
      return !exists(p) || confirming || !!p.frozen || p.status === 'queued' || p.status === 'recognizing';
    }
    function ready(p) {
      return p.status === 'review' && p.recognizedNumbers === p.numbers && p.questions.length > 0;
    }
    function canRecognize(p) {
      return enabled() && !locked(p) && photos.length <= maxPhotos && p.file.size <= maxBytes;
    }
    function updateSubjectName() {
      var key = batch.subject.trim();
      var found = subjects.filter(function (s) { return s.subject === key; })[0];
      subjectName.textContent = found ? '错题本名称：' + (found.name || found.subject) + '；科目：' + found.subject : (key ? '新科目：' + key : '请选择或输入科目；已有错题本可从输入建议中选择。');
    }

    function refresh() {
      if (!alive) return;
      var succeeded = photos.filter(function (p) { return p.status === 'success'; });
      var dryCount = photos.filter(function (p) { return p.status === 'dry'; }).length;
      var imported = succeeded.reduce(function (n, p) { return n + p.imported; }, 0);
      summary.textContent = '已入库成功 ' + succeeded.length + ' 张（' + imported + ' 题） / 共 ' + photos.length + ' 张；剩余未入库 ' + (photos.length - succeeded.length) + ' 张' + (dryCount ? '，其中演练完成但未入库 ' + dryCount + ' 张' : '') + '。';
      back.textContent = '返回列表（成功 ' + succeeded.length + ' / 剩余未入库 ' + (photos.length - succeeded.length) + '）';
      back.disabled = confirming;
      picker.disabled = confirming || configLoading;
      configRetry.disabled = confirming || configLoading || active > 0 || queue.length > 0;
      metadata.disabled = batchLocked || confirming;
      Object.keys(batchInputs).forEach(function (key) { batchInputs[key].disabled = metadata.disabled; });
      metadataHint.textContent = batchLocked ? '本批信息已锁定；入库结果不确定的照片只能原样重试。未提交或已恢复编辑的照片仍可逐题修改。' : '识别时将批次章节/出处填入每题，可逐题修改；之后调整批次章节/出处不会覆盖已有题目。首次确认后本批信息锁定。';
      start.disabled = !enabled() || confirming || !photos.some(function (p) {
        return canRecognize(p) && (p.status === 'pending' || p.status === 'failed' || p.status === 'stale') && !(p.questions.length && p.edited);
      });
      navigationWarning.textContent = confirming ? '正在确认入库，暂时不能离开应用内页面。关闭/刷新浏览器后后台可能仍完成写入，勿重复新建批次；请等待结果。' :
        (photos.some(function (p) { return p.status === 'uncertain'; }) ? '有照片的入库结果不确定，可能已经写入。请留在本页“重试确认（相同内容）”，不要为同一照片新建批次。' : '离开或刷新将丢弃本页未入库草稿；返回列表会重新读取已入库结果。');
      photos.forEach(function (p) {
        var disabled = locked(p);
        p.statusEl.textContent = STATUS[p.status];
        p.statusEl.className = 'import-status import-status-' + p.status;
        p.errorEl.textContent = p.error;
        p.numbersInput.disabled = disabled;
        p.remove.disabled = disabled;
        p.recognize.disabled = !canRecognize(p);
        p.recognize.textContent = p.questions.length || p.status === 'empty' ? '重新识别此图' : (p.status === 'failed' ? '重试识别此图' : '识别此图');
        p.controls.forEach(function (c) { c.disabled = disabled; });
        p.check.checked = p.reviewed;
        p.check.disabled = disabled || !ready(p);
        p.confirm.disabled = !alive || confirming || active > 0 || queue.length > 0 || (p.frozen ? p.status !== 'uncertain' : (!ready(p) || !p.reviewed));
        p.confirm.textContent = p.status === 'uncertain' ? '重试确认（相同内容）' : (p.status === 'dry' ? '演练完成，未入库' : (p.status === 'success' ? '已入库，已锁定' : '确认入库（仅此图）'));
        p.resultHint.textContent = p.frozen ? (p.status === 'success' ? p.receipt : (p.status === 'dry' ? '演练未入库，不能计入成功数；本次请求已锁定。' : '请求内容已冻结，不可编辑或移除；重试将使用相同导入编号、原图与题目信息。')) :
          (p.status === 'empty' ? '本图所有草稿题目已移除，无法确认入库。请重新识别此图，或移除照片。' :
            (p.questions.length && !ready(p) ? '旧草稿仅供参考：题号已变更、重识别中或失败，必须成功重新识别后才能核对入库。' : (p.model ? '本图识别模型：' + p.model : '')));
      });
    }

    function loadConfig() {
      if (!alive || confirming || active || queue.length || configLoading) return;
      configLoading = true;
      config = null;
      configInfo.textContent = '正在读取识别配置…';
      refresh();
      app.api('/api/import/config', { signal: pageController.signal }).then(function (d) {
        if (!alive) return;
        if (!d || d.success !== true || typeof d.enabled !== 'boolean') throw new Error('配置响应格式不正确');
        config = d;
        function limit(value, cap) { return Number.isInteger(value) && value > 0 ? Math.min(value, cap) : cap; }
        maxPhotos = limit(d.max_photos, 20);
        maxBytes = limit(d.max_photo_bytes, 10485760);
        maxQuestions = limit(d.max_questions, 30);
        configInfo.textContent = d.enabled ? '识别已启用；模型：' + (typeof d.model === 'string' ? d.model : '未提供') + '。' : '照片识别未启用：服务端尚未配置，不能开始识别。';
        configInfo.textContent += ' 限制：' + maxPhotos + ' 张 / 每张 ' + (maxBytes / 1048576) + ' MiB / 每图最多 ' + maxQuestions + ' 题。';
        if (d.dry_run) configInfo.textContent += ' 当前为演练模式：确认成功也不会实际入库。';
      }).catch(function (e) {
        if (alive) configInfo.textContent = '无法读取识别配置，识别已禁用：' + e.message;
      }).finally(function () { configLoading = false; refresh(); });
    }

    function addPhotos(files) {
      if (!alive || confirming || configLoading) return;
      var errors = [];
      Array.prototype.forEach.call(files, function (file) {
        var duplicate = photos.some(function (p) {
          return p.file === file || (p.file.name === file.name && p.file.size === file.size && p.file.type === file.type && p.file.lastModified === file.lastModified);
        });
        if (duplicate) { errors.push(file.name + '：已在本批，已跳过重复照片'); return; }
        if (photos.length >= maxPhotos) { errors.push('本批最多 ' + maxPhotos + ' 张照片'); return; }
        if (file.type !== 'image/jpeg' && file.type !== 'image/png') { errors.push(file.name + '：只支持 JPEG / PNG'); return; }
        if (!file.size || file.size > maxBytes) { errors.push(file.name + '：照片不能为空且不得超过 ' + (maxBytes / 1048576) + ' MiB'); return; }
        var id;
        try { id = importID(); } catch (e) { errors.push('无法生成安全的导入编号，请使用支持安全随机数的浏览器打开'); return; }
        var p = {
          id: id, file: file, url: URL.createObjectURL(file), numbers: '', recognizedNumbers: null,
          questions: [], reviewed: false, edited: false, status: 'pending', error: '',
          frozen: null, uncertainAttempt: false, imported: 0, model: '', controls: [], controller: null
        };
        photos.push(p);
        dirty = true;
        makeCard(p);
      });
      error.textContent = errors.join('；');
      refresh();
    }

    function makeCard(p) {
      var card = app.el('article', 'import-photo');
      p.card = card;
      cards.appendChild(card);
      var header = app.el('div', 'import-photo-head');
      header.appendChild(app.el('h3', null, p.file.name));
      p.statusEl = app.el('span', 'import-status');
      p.statusEl.setAttribute('role', 'status');
      header.appendChild(p.statusEl);
      card.appendChild(header);
      var layout = app.el('div', 'import-photo-layout');
      var reference = app.el('aside', 'import-photo-reference');
      var editor = app.el('div', 'import-photo-editor');
      layout.appendChild(reference);
      layout.appendChild(editor);
      card.appendChild(layout);
      var original = app.el('a', 'import-original');
      original.href = p.url;
      original.target = '_blank';
      original.rel = 'noopener noreferrer';
      var image = app.el('img');
      image.src = p.url;
      image.alt = '原图：' + p.file.name;
      original.appendChild(image);
      original.appendChild(app.el('span', null, '查看原图 / 放大（新窗口）'));
      reference.appendChild(original);
      p.numbersInput = app.el('input');
      p.numbersInput.type = 'text';
      p.numbersInput.name = 'question_numbers';
      p.numbersInput.placeholder = '第4题、第10题(1)';
      p.numbersInput.oninput = function () {
        if (locked(p)) return;
        p.numbers = p.numbersInput.value;
        p.reviewed = false;
        p.edited = true;
        // 即使后来改回原题号，也不能悄悄恢复旧核对结果。
        p.recognizedNumbers = null;
        if (p.questions.length) p.status = 'stale';
        refresh();
      };
      reference.appendChild(field('此图要识别的题号（必填，最多500字符，例如：第4题、第10题(1)）', p.numbersInput));
      var actions = app.el('div', 'toolbar');
      p.recognize = button('识别此图', 'primary import-recognize', function () { enqueue(p, true); });
      p.remove = button('移除照片', 'danger import-remove-photo', function () {
        if (locked(p)) return;
        if (p.questions.length && !confirm('移除这张照片会丢弃其草稿，确定移除？')) return;
        photos.splice(photos.indexOf(p), 1);
        URL.revokeObjectURL(p.url);
        card.remove();
        refresh();
      });
      actions.appendChild(p.recognize);
      actions.appendChild(p.remove);
      reference.appendChild(actions);
      p.errorEl = app.el('p', 'hint err import-photo-error');
      p.errorEl.setAttribute('role', 'alert');
      editor.appendChild(p.errorEl);
      p.resultHint = app.el('p', 'hint import-result-hint');
      editor.appendChild(p.resultHint);
      p.questionWrap = app.el('div', 'import-questions');
      editor.appendChild(p.questionWrap);
      p.check = app.el('input', 'import-reviewed');
      p.check.type = 'checkbox';
      p.check.checked = false;
      p.check.onchange = function () {
        if (locked(p) || !ready(p)) { p.check.checked = p.reviewed; return; }
        p.reviewed = p.check.checked;
        refresh();
      };
      editor.appendChild(field('我已对照原图核对本图所有保留题目，确认题号、题干与作答正确', p.check));
      p.confirm = button('确认入库（仅此图）', 'primary import-confirm', function () { confirmPhoto(p); });
      editor.appendChild(p.confirm);
    }

    function paintQuestions(p) {
      p.questionWrap.textContent = '';
      p.controls = [];
      p.questions.forEach(function (q, index) {
        var box = app.el('fieldset', 'import-question');
        box.appendChild(app.el('legend', null, '识别题目 ' + (index + 1)));
        var doubts = app.el('div', 'hint import-uncertainties');
        doubts.appendChild(app.el('strong', null, '模型疑点'));
        var list = app.el('ul');
        (q.uncertainties.length ? q.uncertainties : ['模型未报告疑点；仍须人工核对。']).forEach(function (u) { list.appendChild(app.el('li', null, u)); });
        doubts.appendChild(list);
        box.appendChild(doubts);
        var fields = [
          ['qno', '题号'], ['qtype', '题型'], ['stem', '题干（含选项）', true],
          ['student_answer', '我的作答', true], ['chapter', '章节'], ['source', '出处（可空，四选一）'],
          ['tags', '知识点（用顿号或逗号分隔）'], ['analysis', '错因要点', true], ['note', '备注', true]
        ];
        fields.forEach(function (f) {
          var key = f[0];
          var input = key === 'source' ? sourceSelect() : app.el(f[2] ? 'textarea' : 'input');
          if (!f[2] && key !== 'source') input.type = 'text';
          input.name = key;
          input.value = q[key];
          input[key === 'source' ? 'onchange' : 'oninput'] = function () {
            if (locked(p)) return;
            q[key] = input.value;
            p.reviewed = false;
            p.edited = true;
            if (preview) preview.textContent = '内容已修改，请重新点击预览。';
            refresh();
          };
          p.controls.push(input);
          box.appendChild(field(f[1], input));
          var preview;
          if (key === 'stem' || key === 'student_answer') {
            preview = app.el('div', 'import-math-preview');
            var previewButton = button(f[1] + '公式预览', 'ghost import-preview', function () { app.renderText(preview, q[key]); });
            box.appendChild(previewButton);
            box.appendChild(preview);
          }
        });
        var remove = button('移除此题（多识别时使用）', 'danger import-remove-question', function () {
          if (locked(p)) return;
          p.questions.splice(p.questions.indexOf(q), 1);
          p.reviewed = false;
          p.edited = true;
          if (!p.questions.length) { p.status = 'empty'; p.error = ''; }
          paintQuestions(p);
          refresh();
        });
        p.controls.push(remove);
        box.appendChild(remove);
        p.questionWrap.appendChild(box);
      });
    }

    function enqueue(p, manual) {
      if (!canRecognize(p)) return;
      var validation = textError(p.numbers, '此图要识别的题号', 500, true, true);
      if (validation) { p.error = validation + '。例如：第4题、第10题(1)'; refresh(); return; }
      if (p.questions.length && p.edited) {
        if (!manual || !confirm('重新识别会覆盖这张照片已经编辑的草稿，并撤销核对。确定继续？')) return;
      }
      p.reviewed = false;
      p.recognizedNumbers = null;
      p.status = 'queued';
      p.error = '';
      queue.push(p);
      refresh();
      pump();
    }

    function pump() {
      if (!alive) return;
      while (active < 2 && queue.length) recognize(queue.shift());
      refresh();
    }

    function recognize(p) {
      active++;
      p.status = 'recognizing';
      p.controller = new AbortController();
      var numbers = p.numbers;
      var defaults = { chapter: batch.chapter, source: batch.source };
      var form = new FormData();
      form.append('photo', p.file);
      form.append('question_numbers', numbers);
      app.apiForm('/api/import/recognize', { body: form, signal: p.controller.signal }).then(function (d) {
        if (!exists(p)) return;
        if (!d || d.success !== true || !Array.isArray(d.questions) || !d.questions.length || d.questions.length > maxQuestions) throw new Error('识别没有返回有效题目或超出题数限制，请重试');
        var questions = d.questions.map(function (q) {
          if (!q || ['qno', 'qtype', 'stem', 'student_answer'].some(function (key) { return typeof q[key] !== 'string'; }) || !Array.isArray(q.uncertainties) || q.uncertainties.some(function (u) { return typeof u !== 'string'; })) throw new Error('识别题目格式不正确，请重试');
          return {
            qno: q.qno, qtype: q.qtype, stem: q.stem, student_answer: q.student_answer,
            uncertainties: q.uncertainties.slice(), chapter: defaults.chapter, source: defaults.source,
            tags: '', analysis: '', note: ''
          };
        });
        p.questions = questions;
        p.recognizedNumbers = numbers;
        p.status = 'review';
        p.edited = false;
        p.reviewed = false;
        p.model = typeof d.model === 'string' ? d.model : '';
        paintQuestions(p);
      }).catch(function (e) {
        if (!exists(p)) return;
        p.status = 'failed';
        p.error = '识别失败：' + e.message + '。草稿保留，可手动重试；不会自动发起收费请求。';
      }).finally(function () {
        active--;
        p.controller = null;
        pump();
      });
    }

    function confirmationError(p) {
      if (!p.file.size || p.file.size > maxBytes) return '照片不能为空且不得超过 ' + (maxBytes / 1048576) + ' MiB，请移除并重新选择照片';
      if (p.file.type !== 'image/jpeg' && p.file.type !== 'image/png') return '照片只支持 JPEG / PNG';
      var error = textError(batch.subject, '科目', 64, true, true) ||
        textError(batch.chapter, '批次章节', 128, false, true) || textError(batch.source, '批次出处', 16, false, true);
      if (error) return error;
      if (!validDate(batch.date)) return '日期必须是真实有效的 YYYY-MM-DD 日期';
      if (batch.source && SOURCES.indexOf(batch.source) < 0) return '批次出处请从四个选项中选择，也可留空';
      if (!p.questions.length || p.questions.length > maxQuestions) return '每张照片须保留 1 至 ' + maxQuestions + ' 题';
      var seen = new Set();
      var fields = [
        ['qno', '题号', 64, true, true], ['qtype', '题型', 64, false, true],
        ['stem', '题干', 12000, true, false], ['student_answer', '我的作答', 8000, false, false],
        ['chapter', '章节', 128, false, true], ['source', '出处', 16, false, true],
        ['analysis', '错因要点', 4000, false, false], ['note', '备注', 2000, false, false]
      ];
      for (var i = 0; i < p.questions.length; i++) {
        var q = p.questions[i], prefix = '第 ' + (i + 1) + ' 项题目：';
        for (var j = 0; j < fields.length; j++) {
          var f = fields[j];
          error = textError(q[f[0]], f[1], f[2], f[3], f[4]);
          if (error) return prefix + error;
        }
        if (q.source && SOURCES.indexOf(q.source) < 0) return prefix + '出处请从四个选项中选择，也可留空';
        var tags = tagsFromText(q.tags);
        if (tags.length > 20) return prefix + '知识点最多 20 个';
        for (var t = 0; t < tags.length; t++) {
          error = textError(tags[t], '第 ' + (t + 1) + ' 个知识点', 32, true, true);
          if (error) return prefix + error;
        }
        var qno = normalizedQno(q.qno);
        if (seen.has(qno)) return prefix + '与本图其他题目的题号重复（已忽略空白、全半角及简单数字题号格式），请修改题号或移除多识别的题目';
        seen.add(qno);
      }
      return '';
    }

    function confirmPhoto(p) {
      if (!exists(p) || confirming || active || queue.length) return;
      if (p.frozen && p.status !== 'uncertain') return;
      if (!p.frozen) {
        if (p.status === 'empty') { p.error = '本图没有保留题目，请重新识别或移除照片。'; refresh(); return; }
        if (!ready(p) || !p.reviewed) { p.error = '请先成功识别并勾选已核对，再确认入库。'; refresh(); return; }
        var validation = confirmationError(p);
        if (validation) { p.error = validation + '。请修正后重新核对。'; p.reviewed = false; refresh(); return; }
        var data = {
          import_id: p.id, confirmed: true, subject: batch.subject.trim(), date: batch.date,
          chapter: batch.chapter, source: batch.source,
          questions: p.questions.map(function (q) {
            return {
              qno: q.qno, qtype: q.qtype, stem: q.stem, student_answer: q.student_answer,
              chapter: q.chapter, source: q.source, analysis: q.analysis, note: q.note,
              tags: tagsFromText(q.tags)
            };
          })
        };
        // 不确定结果只能原样重试；仅明确写入前 400 且无不确定历史时允许解冻。
        p.frozen = Object.freeze({ metadata: JSON.stringify(data), file: p.file, count: data.questions.length });
        batchLocked = true;
      }
      confirming = true;
      p.status = 'confirming';
      p.error = '';
      refresh();
      var form = new FormData();
      form.append('photo', p.frozen.file);
      form.append('metadata', p.frozen.metadata);
      // 不给确认请求挂离页 AbortSignal：中止客户端不能保证撤销已发生的写入。
      app.apiForm('/api/import/confirm', { body: form }).then(function (d) {
        if (!alive) return;
        if (!d || d.success !== true || typeof d.dry_run !== 'boolean') throw new Error('确认响应格式异常');
        if (d.dry_run) {
          p.status = 'dry';
          p.imported = 0;
        } else {
          if (d.imported !== p.frozen.count || !Array.isArray(d.ids) || d.ids.length !== p.frozen.count || d.ids.some(function (id) { return typeof id !== 'string' || !id; })) throw new Error('入库回执不完整，请用相同内容重试确认');
          p.status = 'success';
          p.imported = d.imported;
          p.receipt = (d.already_imported ? '服务端确认此前已入库（未重复写入）' : '已入库') + '，共 ' + d.imported + ' 题。此图已锁定。';
        }
      }).catch(function (e) {
        if (!alive) return;
        if (e.status === 400 && e.serverRejected && !p.uncertainAttempt) {
          p.frozen = null;
          p.status = 'review';
          p.reviewed = false;
          batchLocked = photos.some(function (photo) { return !!photo.frozen; });
          p.error = '校验未通过：' + e.message + '。本次尚未写入，已恢复编辑，请修改后重新核对并确认。';
        } else {
          // 即便之后重试收到 400，也不能撤销更早请求可能已经发生的写入。
          p.uncertainAttempt = true;
          p.status = 'uncertain';
          p.error = '确认失败：' + e.message + '。服务端可能已写入，请点击“重试确认（相同内容）”；不要新建批次重复导入。';
        }
      }).finally(function () { confirming = false; refresh(); });
    }

    function hasDraft() { return photos.some(function (p) { return p.status !== 'success'; }) || (dirty && !photos.length); }
    function beforeUnload(event) {
      if (!confirming && !hasDraft()) return;
      event.preventDefault();
      event.returnValue = '草稿仅在当前页；正在入库的请求可能后台完成，请勿重复新建批次。';
    }
    function canLeave() {
      if (confirming) { app.toast('正在确认入库，暂时不能离开；后台可能完成写入，勿重复新建批次。'); return false; }
      if (!hasDraft()) return true;
      var uncertain = photos.some(function (p) { return p.status === 'uncertain'; });
      return confirm((uncertain ? '有入库结果不确定的照片，可能已写入，建议留在本页原样重试，勿重复新建批次。\n' : '') + summary.textContent + '\n离开将丢弃未入库草稿并中止识别（已发出的识别可能仍收费），确定离开？');
    }
    function destroy() {
      alive = false;
      queue = [];
      pageController.abort();
      photos.forEach(function (p) {
        if (p.controller) p.controller.abort();
        URL.revokeObjectURL(p.url);
      });
      window.removeEventListener('beforeunload', beforeUnload);
      photos = [];
    }

    window.addEventListener('beforeunload', beforeUnload);
    updateSubjectName();
    loadConfig();
    app.api('/api/subjects', { signal: pageController.signal }).then(function (d) {
      if (!alive) return;
      subjects = (d.subjects || []).filter(function (s) { return s && typeof s.subject === 'string'; });
      subjects.forEach(function (s) {
        var option = app.el('option');
        option.value = s.subject;
        option.label = (s.name || s.subject) + '（' + s.subject + '）';
        subjectList.appendChild(option);
      });
      updateSubjectName();
    }).catch(function (e) {
      if (alive) subjectName.textContent = '错题本名称读取失败：' + e.message + '；仍可手动输入科目。';
    });
    refresh();
    return { canLeave: canLeave, destroy: destroy };
  }

  return { render: render };
})();
