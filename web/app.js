'use strict';
/* gitreview – side-by-side review of the current branch. Vanilla JS, no build. */

const $ = (s, el = document) => el.querySelector(s);
const CTX = 4;        // unchanged lines shown around a change
const MIN_FOLD = 4;   // fold only runs of at least this many hidden lines

// ---- persistence (localStorage may be unavailable) ---------------------------
const LS = {
  get(k, d) {
    try { const v = localStorage.getItem('gitreview:' + k); return v === null ? d : JSON.parse(v); }
    catch { return d; }
  },
  set(k, v) { try { localStorage.setItem('gitreview:' + k, JSON.stringify(v)); } catch { /* ignore */ } },
};

const state = {
  info: null,
  opts: {
    base: LS.get('base', ''),
    left: LS.get('left', 'mergebase'),
    right: LS.get('right', 'worktree'),
    ws: LS.get('ws', false),
  },
  compare: null,
  files: [],
  order: [],            // file paths in tree order
  current: null,        // path the user selected
  diff: null,           // loaded diff; diff.file.path may lag behind `current` while loading
  forced: null,         // path for which "show anyway" was chosen
  comments: [],
  expanded: new Set(),  // equal-block indexes expanded by the user
  expandAll: LS.get('expandAll', false), // show whole files, no folds
  collapsedDirs: new Set(),
  seen: {},             // path -> sig when last opened
  editing: null,        // {side, line} for new comment or {id} for edit
  draft: '',
  seq: { diff: 0, files: 0, comments: 0 }, // latest request per kind; older responses are dropped
  inflight: 0,          // user-initiated diff loads in progress
  error: null,
  pending: { head: '', files: [] }, // uncommitted files (commit panel)
  cmUnchecked: new Set(), // files the user excluded from the commit
  cmHead: null,            // HEAD the open commit panel was reviewed against
  confirm: null,        // pending confirmation: {kind: 'revert', path} | {kind: 'stash', action}
};

// ---- helpers -------------------------------------------------------------------
const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
const esc = s => String(s).replace(/[&<>"']/g, c => ESC[c]);

async function api(url, opts = {}) {
  if (opts.method && opts.method !== 'GET') {
    // the server only accepts JSON for state-changing requests (CSRF protection)
    opts = { ...opts, body: JSON.stringify(opts.json ?? {}), headers: { 'Content-Type': 'application/json' } };
  }
  const res = await fetch(url, opts);
  let data = null;
  try { data = await res.json(); } catch { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

/* The base selector also lists stashes ("stash:<sha>"): browsing one compares
   it with the commit it was made on. */
const stashSha = () => state.opts.base.startsWith('stash:') ? state.opts.base.slice(6) : null;

function compareParams(extra = {}) {
  const o = state.opts;
  const sh = stashSha();
  if (sh) return new URLSearchParams({ stash: sh, ws: o.ws ? '1' : '0', ...extra }).toString();
  return new URLSearchParams({ base: o.base, left: o.left, right: o.right, ws: o.ws ? '1' : '0', ...extra }).toString();
}

let toastTimer;
function toast(text, isErr = false, ms = isErr ? 6000 : 3500) {
  const t = $('#toast');
  t.textContent = text;
  t.className = isErr ? 'err' : '';
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, ms);
}

function fmtSize(n) {
  return n > 1 << 20 ? (n / (1 << 20)).toFixed(1) + ' MB' : Math.round(n / 1024) + ' kB';
}

const statusName = st => t('st')[st] || st;

function ago(unix) {
  const s = Date.now() / 1000 - unix;
  const rtf = new Intl.RelativeTimeFormat(t('locale'), { numeric: 'auto' });
  for (const [u, n] of [['day', 86400], ['hour', 3600], ['minute', 60]]) {
    if (s >= n) return rtf.format(-Math.floor(s / n), u);
  }
  return rtf.format(0, 'minute');
}

const confirmHtml = (text, attr) =>
  `<span class="confirm">${esc(text)} <button ${attr}="yes" class="danger">${t('yes')}</button>` +
  `<button ${attr}="no">${t('no')}</button></span>`;

/* Label of one comparison side; the working tree gets a translated suffix. */
const sideLabel = sd => sd.rev ? sd.label : `${sd.label} – ${t('worktree')}`;

// ---- syntax highlighting (language comes from the server, internal/lang) --------
/* Split highlighted HTML into per-line HTML, closing and reopening spans that
   cross line breaks so every line is self-contained. */
function splitHtmlLines(html) {
  const lines = [];
  const open = [];
  let cur = '';
  const re = /(<span[^>]*>)|(<\/span>)|(\n)|([^<\n]+)/g;
  let m;
  while ((m = re.exec(html))) {
    if (m[1]) { open.push(m[1]); cur += m[1]; }
    else if (m[2]) { open.pop(); cur += m[2]; }
    else if (m[3]) { cur += '</span>'.repeat(open.length); lines.push(cur); cur = open.join(''); }
    else cur += m[4];
  }
  lines.push(cur);
  return lines;
}

function highlightLines(lines, lang) {
  const text = lines.join('\n');
  if (lang && window.hljs && hljs.getLanguage(lang) && lines.length && text.length < 800000) {
    try {
      const out = splitHtmlLines(hljs.highlight(text, { language: lang, ignoreIllegals: true }).value);
      if (out.length === lines.length) return out;
    } catch { /* fall through */ }
  }
  return lines.map(esc);
}

// ---- word-level diff -----------------------------------------------------------
const tokenize = s => s.match(/[\p{L}\p{N}_]+|\s+|[^\p{L}\p{N}_\s]/gu) || [];

function changedRanges(tokens, keep, text) {
  const res = [];
  let pos = 0;
  for (let k = 0; k < tokens.length; k++) {
    const len = tokens[k].length;
    if (!keep[k]) {
      const last = res[res.length - 1];
      // merge with previous range when only whitespace separates them
      if (last && /^\s*$/.test(text.slice(last[1], pos))) last[1] = pos + len;
      else res.push([pos, pos + len]);
    }
    pos += len;
  }
  return res;
}

/* Returns [leftRanges, rightRanges] of changed character ranges, or null when
   the lines have nothing meaningful in common (whole line is the change). */
function wordDiff(a, b) {
  const ta = tokenize(a), tb = tokenize(b);
  const n = ta.length, m = tb.length;
  if (!n || !m || n * m > 250000) return null;
  const W = m + 1;
  const dp = new Uint32Array((n + 1) * W);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i * W + j] = ta[i] === tb[j] ? dp[(i + 1) * W + j + 1] + 1 : Math.max(dp[(i + 1) * W + j], dp[i * W + j + 1]);
    }
  }
  const ka = new Array(n).fill(false), kb = new Array(m).fill(false);
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (ta[i] === tb[j]) { ka[i] = kb[j] = true; i++; j++; }
    else if (dp[(i + 1) * W + j] >= dp[i * W + j + 1]) i++;
    else j++;
  }
  if (!ta.some((t, k) => ka[k] && /\S/.test(t))) return null;
  return [changedRanges(ta, ka, a), changedRanges(tb, kb, b)];
}

/* Wrap character ranges of the underlying text in <span class="w">, walking
   highlighted HTML (tags and entities) while keeping nesting valid. */
function applyRanges(html, ranges) {
  if (!ranges || !ranges.length) return html;
  const open = '<span class="w">';
  let out = '', pos = 0, ri = 0, inR = false, i = 0;
  while (i < html.length) {
    const ch = html[i];
    if (ch === '<') {
      const j = html.indexOf('>', i);
      const tag = html.slice(i, j + 1);
      out += inR ? '</span>' + tag + open : tag;
      i = j + 1;
      continue;
    }
    while (ri < ranges.length && pos >= ranges[ri][1]) {
      if (inR) { out += '</span>'; inR = false; }
      ri++;
    }
    if (!inR && ri < ranges.length && pos >= ranges[ri][0]) { out += open; inR = true; }
    let len = 1;
    if (ch === '&') { const j = html.indexOf(';', i); if (j > i) len = j - i + 1; }
    out += html.substr(i, len);
    i += len;
    pos++;
  }
  if (inR) out += '</span>';
  return out;
}

/* Similarity of two lines (0..1): Dice coefficient over non-space tokens. */
function similarity(a, b) {
  const ta = tokenize(a).filter(t => /\S/.test(t)), tb = tokenize(b).filter(t => /\S/.test(t));
  if (!ta.length && !tb.length) return 1;
  if (!ta.length || !tb.length) return 0;
  const bag = new Map();
  for (const t of ta) bag.set(t, (bag.get(t) || 0) + 1);
  let common = 0;
  for (const t of tb) {
    const c = bag.get(t);
    if (c) { common++; bag.set(t, c - 1); }
  }
  return 2 * common / (ta.length + tb.length);
}

/* Align the lines of a modified block like IntelliJ: similar lines end up on
   the same row, the rest get filler on the other side. Returns [[l|null, r|null]…]
   with block-relative indexes. */
const naivePairs = (n, m) => Array.from({ length: Math.max(n, m) }, (_, k) => [k < n ? k : null, k < m ? k : null]);

function alignBlock(left, right) {
  const n = left.length, m = right.length;
  if (n * m > 40000) return naivePairs(n, m);
  const W = m + 1;
  const sim = new Float32Array(n * m);
  for (let i = 0; i < n; i++) for (let j = 0; j < m; j++) sim[i * m + j] = similarity(left[i], right[j]);
  const dp = new Float64Array((n + 1) * W);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      const s = sim[i * m + j];
      const pair = s >= 0.4 ? s + dp[(i + 1) * W + j + 1] : -1;
      dp[i * W + j] = Math.max(pair, dp[(i + 1) * W + j], dp[i * W + j + 1]);
    }
  }
  const res = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    const s = sim[i * m + j];
    if (s >= 0.4 && dp[i * W + j] === s + dp[(i + 1) * W + j + 1]) res.push([i++, j++]);
    else if (dp[(i + 1) * W + j] >= dp[i * W + j + 1]) res.push([i++, null]);
    else res.push([null, j++]);
  }
  while (i < n) res.push([i++, null]);
  while (j < m) res.push([null, j++]);
  // nothing paired at all: fall back to side-by-side order (reads better)
  return res.some(p => p[0] != null && p[1] != null) ? res : naivePairs(n, m);
}

function prepareDiff(d) {
  d.blocks = d.blocks || [];
  if (!d.left || !d.right) return;
  d.htmlL = highlightLines(d.left.lines, d.lang);
  d.htmlR = highlightLines(d.right.lines, d.lang);
  let budget = 3000;
  for (const b of d.blocks) {
    if (b.type !== 'change' || !b.leftLen || !b.rightLen) continue;
    b.pairs = alignBlock(
      d.left.lines.slice(b.leftStart, b.leftStart + b.leftLen),
      d.right.lines.slice(b.rightStart, b.rightStart + b.rightLen));
    for (const [l, r] of b.pairs) {
      if (l == null || r == null || budget-- <= 0) continue;
      const li = b.leftStart + l, ri = b.rightStart + r;
      const wd = wordDiff(d.left.lines[li], d.right.lines[ri]);
      if (wd) {
        d.htmlL[li] = applyRanges(d.htmlL[li], wd[0]);
        d.htmlR[ri] = applyRanges(d.htmlR[ri], wd[1]);
      }
    }
  }
}

// ---- comments for the current file ----------------------------------------------
/* Maps side -> Map(line -> [comment+stale]) for the open file. Comments whose
   line no longer exists are shown on the last line; comments that cannot be
   placed at all (that side is empty, or the diff is not shown) are returned
   in `orphans` and rendered above the diff. */
function fileComments() {
  const res = { left: new Map(), right: new Map(), orphans: [] };
  const d = state.diff;
  const own = state.comments.filter(c => c.file === state.current);
  if (!d || !d.left || !d.blocks.some(b => b.type === 'change')) {
    res.orphans = own.map(c => ({ ...c, stale: true }));
    return res;
  }
  for (const c of own) {
    const lines = c.side === 'left' ? d.left.lines : d.right.lines;
    if (!lines.length) { res.orphans.push({ ...c, stale: true }); continue; }
    const stale = lines[c.line - 1] !== c.lineText;
    const line = Math.min(c.line, lines.length);
    const map = res[c.side];
    if (!map.has(line)) map.set(line, []);
    map.get(line).push({ ...c, stale });
  }
  return res;
}

// ---- rows ---------------------------------------------------------------------
function buildRows(d, cm) {
  const rows = [];
  const ed = state.editing && !state.editing.id ? state.editing : null;
  const pinned = (side, idx) => cm[side].has(idx + 1) || (ed && ed.side === side && ed.line === idx + 1);
  d.blocks.forEach((b, bi) => {
    if (b.type === 'equal') {
      const n = b.leftLen;
      const vis = new Array(n).fill(false);
      if (state.expandAll || state.expanded.has(bi) || n < MIN_FOLD + 2 * CTX) vis.fill(true);
      else {
        const atStart = bi === 0, atEnd = bi === d.blocks.length - 1;
        for (let k = 0; k < n; k++) {
          if ((!atStart && k < CTX) || (!atEnd && k >= n - CTX)) vis[k] = true;
          if (pinned('left', b.leftStart + k) || pinned('right', b.rightStart + k)) {
            for (let q = Math.max(0, k - 1); q <= Math.min(n - 1, k + 1); q++) vis[q] = true;
          }
        }
      }
      for (let k = 0; k < n;) {
        if (vis[k]) { rows.push({ kind: 'eq', l: b.leftStart + k, r: b.rightStart + k }); k++; continue; }
        let e = k;
        while (e < n && !vis[e]) e++;
        if (e - k < MIN_FOLD) {
          for (; k < e; k++) rows.push({ kind: 'eq', l: b.leftStart + k, r: b.rightStart + k });
        } else {
          rows.push({ kind: 'fold', block: bi, count: e - k });
          k = e;
        }
      }
    } else {
      const kind = b.leftLen === 0 ? 'add' : b.rightLen === 0 ? 'del' : 'mod';
      const pairs = b.pairs || naivePairs(b.leftLen, b.rightLen);
      const n = pairs.length;
      pairs.forEach(([l, r], k) => {
        rows.push({
          kind, block: bi, first: k === 0, last: k === n - 1, size: n,
          l: l == null ? null : b.leftStart + l,
          r: r == null ? null : b.rightStart + r,
        });
      });
    }
  });
  return rows;
}

// ---- rendering: diff --------------------------------------------------------------
function cellsHtml(side, idx) {
  const s = side === 'left' ? 'l' : 'r';
  if (idx == null) return `<td class="num filler"></td><td class="code filler" data-s="${s}"></td>`;
  const html = (side === 'left' ? state.diff.htmlL : state.diff.htmlR)[idx];
  return `<td class="num" data-side="${side}" data-line="${idx + 1}">${idx + 1}</td>` +
    `<td class="code" data-s="${s}"><span class="ln">${html || ' '}</span></td>`;
}

function rowHtml(row) {
  if (row.kind === 'fold') {
    return `<tr class="fold" data-block="${row.block}"><td class="num"></td>` +
      `<td class="code" colspan="4">${t('folded', row.count)}</td></tr>`;
  }
  let cls = 'k-' + row.kind;
  if (row.first) cls += ' first';
  if (row.last) cls += ' last';
  const cs = row.first ? ` data-cs="${row.kind}" data-n="${row.size}"` : '';
  return `<tr class="${cls}"${cs}>${cellsHtml('left', row.l)}<td class="mid"></td>${cellsHtml('right', row.r)}</tr>`;
}

function commentBoxHtml(c) {
  const ed = state.editing;
  if (ed && ed.id === c.id) return editorHtml(t('saveChanges'));
  const when = new Date(c.created).toLocaleString(t('locale'), { dateStyle: 'short', timeStyle: 'short' });
  const stale = c.stale
    ? `<span class="stale-badge" title="${esc(t('staleTitle', c.lineText))}">${t('stale')}</span>`
    : '';
  const side = c.side === 'left' ? t('sideOld') : t('sideNew');
  return `<div class="cbox${c.stale ? ' stale' : ''}" data-id="${c.id}">` +
    `<div class="ch"><span>${side} · ${t('line')} ${c.line} · ${esc(when)}</span>${stale}<span class="sp"></span>` +
    `<button data-act="edit">${t('edit')}</button><button data-act="delete" class="danger">${t('delete')}</button></div>` +
    `<div class="ct">${esc(c.text)}</div></div>`;
}

function editorHtml(label = t('addComment')) {
  return `<div class="ceditor"><textarea id="ctext" placeholder="${t('placeholder')}"></textarea>` +
    `<div class="cbtns"><button class="primary" data-act="save">${label}</button>` +
    `<button data-act="cancel">${t('cancel')}</button><span class="hint">${t('editorHint')}</span></div></div>`;
}

function commentRowHtml(row, cm) {
  if (row.kind === 'fold') return '';
  const ed = state.editing && !state.editing.id ? state.editing : null;
  const parts = ['left', 'right'].map(side => {
    const idx = side === 'left' ? row.l : row.r;
    if (idx == null) return '';
    let h = (cm[side].get(idx + 1) || []).map(commentBoxHtml).join('');
    if (ed && ed.side === side && ed.line === idx + 1) h += editorHtml();
    return h;
  });
  if (!parts[0] && !parts[1]) return '';
  return `<tr class="crow"><td colspan="2">${parts[0]}</td><td class="mid"></td><td colspan="2">${parts[1]}</td></tr>`;
}

/* Comments of the open file that cannot be anchored to a visible line. */
function orphansHtml(orphans) {
  if (!orphans.length) return '';
  return `<div class="orphans"><div class="orphans-head">${t('orphans')}</div>` +
    orphans.map(commentBoxHtml).join('') + '</div>';
}

function renderMessage(html, extra = '') {
  $('#diff-scroll').innerHTML = `${extra}<div class="msg">${html}</div>`;
  $('#ruler').innerHTML = '';
  setHScroll(0, 0);
  bindEditor();
}

function bindEditor() {
  const ta = $('#ctext');
  if (!ta) return;
  ta.value = state.draft;
  ta.focus({ preventScroll: true });
  ta.addEventListener('input', () => { state.draft = ta.value; });
}

function emptyDiffMessage(d) {
  if (d.file.status === 'R') return t('renamedOnly', esc(d.file.oldPath));
  if (!d.left.lines.length && !d.right.lines.length) return t('emptyFile');
  if (state.opts.ws) return t('wsOnly');
  return t('sameContent');
}

function renderHeader() {
  const h = $('#diff-header');
  const d = state.diff;
  const f = d ? d.file : state.files.find(x => x.path === state.current);
  if (!f) { h.innerHTML = ''; return; }
  const old = f.oldPath ? `<span class="old">← ${esc(f.oldPath)}</span>` : '';
  let sides = '';
  if (d && d.left && d.right) {
    const lbl = sd => sd.exists ? `<b>${esc(sideLabel(sd))}</b>` : `${esc(sideLabel(sd))} – ${t('fileMissing')}`;
    const l = lbl(d.left), r = lbl(d.right);
    sides = `<div class="dh-sides cols" id="dh-sides"><div class="pad-num"></div><div class="side">${l}</div>` +
      `<div class="pad-mid"></div><div class="side">${r}</div></div>`;
  }
  h.innerHTML = `<div class="dh-path"><span class="st st-${f.status}">${statusName(f.status)}</span>` +
    `<span>${esc(f.path)}</span>${old}<span class="hint">${t('hint')}</span>` +
    `<span class="dh-nav"><button data-nav="prev" title="${esc(t('prevChange'))}">↑</button>` +
    `<span id="nav-pos" class="nav-pos"></span>` +
    `<button data-nav="next" title="${esc(t('nextChange'))}">↓</button>` +
    revertHtml(f) +
    `<button data-nav="expand" class="${state.expandAll ? 'on' : ''}" title="${esc(t(state.expandAll ? 'collapseTitle' : 'expandTitle'))}">` +
    `${t(state.expandAll ? 'collapseAll' : 'expandAll')}</button></span></div>${sides}`;
}

function revertHtml(f) {
  if (stashSha() || !f.uncommitted) return '';
  const c = state.confirm;
  if (c && c.kind === 'revert' && c.path === f.path) return confirmHtml(t('revertConfirm', f.path), 'data-nav');
  return `<button data-nav="revert" title="${esc(t('revertTitle'))}">${t('revert')}</button>`;
}

function renderDiff({ keepScroll = true } = {}) {
  const sc = $('#diff-scroll');
  const top = sc.scrollTop;
  renderHeader();
  const d = state.diff;
  if (!d) return;
  const cm = fileComments();
  const extra = orphansHtml(cm.orphans);
  if (d.binary) { renderMessage(t('binary'), extra); return; }
  if (d.tooLarge) {
    renderMessage(`${t('tooLarge', fmtSize(d.size))}<br><button id="btn-force">${t('showAnyway')}</button>`, extra);
    $('#btn-force').onclick = () => { state.forced = state.current; openFile(state.current); };
    return;
  }
  if (!d.blocks.some(b => b.type === 'change')) { renderMessage(emptyDiffMessage(d), extra); return; }
  let h = extra + '<table class="diff"><colgroup><col class="cn"><col><col class="cm"><col class="cn"><col></colgroup><tbody>';
  for (const row of buildRows(d, cm)) h += rowHtml(row) + commentRowHtml(row, cm);
  h += '</tbody></table>';
  sc.innerHTML = h;
  if (keepScroll) sc.scrollTop = top;
  bindEditor();
  updateHScroll();
  renderRuler();
  updateNavPos();
}

// ---- horizontal scrolling (each side scrolls independently) -----------------------
let charW = 0;
function measureChar() {
  const m = $('#measure');
  charW = m.getBoundingClientRect().width / m.textContent.length || 7.5;
}

function textWidth(lines) {
  let max = 0;
  for (const l of lines) {
    let w = l.length;
    if (l.includes('\t')) w += (l.split('\t').length - 1) * 3;
    if (w > max) max = w;
  }
  return Math.ceil(max * charW) + 24;
}

function setHScroll(wl, wr) {
  const hl = $('#hs-left'), hr = $('#hs-right');
  hl.firstElementChild.style.width = wl + 'px';
  hr.firstElementChild.style.width = wr + 'px';
  hl.classList.toggle('none', wl <= hl.clientWidth);
  hr.classList.toggle('none', wr <= hr.clientWidth);
  syncHScroll();
}

function updateHScroll() {
  const d = state.diff;
  const sc = $('#diff-scroll');
  // keep the bars aligned with the table despite the vertical scrollbar and ruler
  const pad = (sc.offsetWidth - sc.clientWidth) + $('#ruler').offsetWidth;
  $('#hscroll').style.paddingRight = pad + 'px';
  const ds = $('#dh-sides');
  if (ds) ds.style.paddingRight = pad + 'px';
  if (!d || !d.left) { setHScroll(0, 0); return; }
  setHScroll(textWidth(d.left.lines), textWidth(d.right.lines));
}

function syncHScroll() {
  const sc = $('#diff-scroll');
  sc.style.setProperty('--sxl', -$('#hs-left').scrollLeft + 'px');
  sc.style.setProperty('--sxr', -$('#hs-right').scrollLeft + 'px');
}

// ---- overview ruler -----------------------------------------------------------------
function renderRuler() {
  const sc = $('#diff-scroll'), ru = $('#ruler');
  const H = sc.scrollHeight, rh = ru.clientHeight;
  if (!H || !rh) { ru.innerHTML = ''; return; }
  const rowH = parseFloat(getComputedStyle(document.body).getPropertyValue('--row-h')) || 19;
  let h = '';
  sc.querySelectorAll('tr[data-cs]').forEach(tr => {
    const top = tr.offsetTop / H * rh;
    const height = Math.max(3, (+tr.dataset.n * rowH) / H * rh);
    h += `<i class="${tr.dataset.cs}" style="top:${top}px;height:${height}px"></i>`;
  });
  sc.querySelectorAll('tr.crow').forEach(tr => {
    h += `<i class="cmt" style="top:${tr.offsetTop / H * rh}px;height:3px"></i>`;
  });
  ru.innerHTML = h;
}

// ---- navigation ------------------------------------------------------------------------
/* Navigation keeps an explicit index of the current change (navIdx) so F7
   works even when the whole file fits on screen and nothing can scroll.
   A manual scroll resets it and the position is derived from the viewport. */
const nav = { idx: null, expectTop: -1 };

function scrollToRow(tr) {
  const sc = $('#diff-scroll');
  sc.scrollTop = Math.max(0, tr.offsetTop - sc.clientHeight / 3);
  nav.expectTop = sc.scrollTop;
  tr.classList.remove('flash');
  void tr.offsetWidth;
  tr.classList.add('flash');
}

function changeMarks() { return [...$('#diff-scroll').querySelectorAll('tr[data-cs]')]; }

// index of the change at the reading position (a third of the viewport)
function currentChange(marks) {
  if (nav.idx != null && nav.idx < marks.length) return nav.idx;
  const sc = $('#diff-scroll');
  const anchor = sc.scrollTop + sc.clientHeight / 3;
  return marks.findLastIndex(tr => tr.offsetTop <= anchor + 2);
}

// "3 / 12" next to the ↑ ↓ buttons
function updateNavPos() {
  const el = $('#nav-pos');
  if (!el) return;
  const marks = changeMarks();
  if (!marks.length) { el.textContent = ''; return; }
  const i = currentChange(marks);
  el.textContent = `${i < 0 ? '–' : i + 1} / ${marks.length}`;
  el.title = t('changesCount', marks.length);
}

/* Toggles whole-file view. The change at the reading position stays put on
   screen even though rows are inserted (or removed) above it. */
function toggleExpandAll() {
  const sc = $('#diff-scroll');
  const marks = changeMarks();
  const i = Math.max(0, currentChange(marks));
  const offset = marks.length ? marks[i].offsetTop - sc.scrollTop : 0;
  state.expandAll = !state.expandAll;
  LS.set('expandAll', state.expandAll);
  if (!state.expandAll) state.expanded.clear();
  renderDiff();
  const after = changeMarks();
  if (after[i]) { sc.scrollTop = after[i].offsetTop - offset; nav.expectTop = sc.scrollTop; }
  updateNavPos();
}

function gotoChange(dir) {
  const sc = $('#diff-scroll');
  const marks = [...sc.querySelectorAll('tr[data-cs]')];
  if (!marks.length) { gotoFile(dir); return; }
  let next;
  if (nav.idx != null && nav.idx < marks.length) {
    next = nav.idx + dir;
  } else {
    const anchor = sc.scrollTop + sc.clientHeight / 3;
    next = dir > 0
      ? marks.findIndex(tr => tr.offsetTop > anchor + 2)
      : marks.findLastIndex(tr => tr.offsetTop < anchor - 2);
  }
  if (next < 0 || next >= marks.length) { gotoFile(dir, dir > 0 ? 'first' : 'last'); return; }
  nav.idx = next;
  scrollToRow(marks[next]);
  updateNavPos();
}

function gotoFile(dir, landOn) {
  const i = state.order.indexOf(state.current);
  const next = state.order[i + dir];
  if (!next) { toast(dir > 0 ? t('lastChange') : t('firstChange')); return; }
  openFile(next, { landOn: landOn || 'first' });
}

function scrollToChange(which = 'first') {
  const marks = $('#diff-scroll').querySelectorAll('tr[data-cs]');
  if (!marks.length) { nav.idx = null; return; }
  nav.idx = which === 'last' ? marks.length - 1 : 0;
  scrollToRow(marks[nav.idx]);
  updateNavPos();
}

function scrollToLine(side, line) {
  const td = $(`#diff-scroll td.num[data-side="${side}"][data-line="${line}"]`);
  if (td) scrollToRow(td.parentElement);
}

// ---- data loading ----------------------------------------------------------------------
/* Runs fetcher only if no newer request of the same kind started meanwhile;
   returns undefined for a superseded response. */
async function latest(kind, fetcher) {
  const seq = ++state.seq[kind];
  const res = await fetcher();
  return seq === state.seq[kind] ? res : undefined;
}

async function loadInfo() {
  const prev = state.info && state.info.version;
  state.info = await api('/api/info');
  if (prev && state.info.version && state.info.version !== prev) reloadWhenIdle(state.info.version);
  const valid = b => state.info.bases.some(x => x.exists && x.name === b) || state.info.branches.includes(b) ||
    (state.info.stashes || []).some(x => 'stash:' + x.sha === b);
  if (!valid(state.opts.base)) {
    const first = state.info.bases.find(b => b.exists);
    state.opts.base = first ? first.name : (state.info.branches[0] || 'develop');
  }
  renderToolbar();
  $('#app-version').textContent = 'gitreview ' + (state.info.version || '');
}

async function loadStatus() {
  try { state.pending = await api('/api/status'); } catch { return; }
  const b = $('#pending-count');
  b.textContent = state.pending.files.length;
  b.classList.toggle('has', state.pending.files.length > 0);
  if ($('#commit-panel').hidden) return;
  if (!state.cmHead) state.cmHead = state.pending.head;
  renderCommitFiles();
}

/* Someone (e.g. Claude Code) committed while the commit panel was open. */
const headMoved = () => !!state.cmHead && !!state.pending && state.cmHead !== state.pending.head;

async function loadFiles() {
  const r = await latest('files', () => api('/api/files?' + compareParams()).then(d => ({ d }), e => ({ e })));
  if (!r) return;
  if (r.e) {
    state.compare = null;
    state.files = [];
    state.error = r.e.message;
  } else {
    state.compare = r.d.compare;
    state.files = r.d.files;
    state.error = null;
  }
  renderSidebar();
}

async function loadComments() {
  const r = await latest('comments', () => api('/api/comments').then(d => ({ d }), e => ({ e })));
  if (!r) return;
  if (r.e) toast(r.e.message, true);
  else state.comments = r.d;
  const n = state.comments.length;
  const b = $('#comment-count');
  b.textContent = n;
  b.classList.toggle('has', n > 0);
  if (!$('#comments-panel').hidden) renderCommentsPanel();
}

/* Reload comments after a change and redraw everything that shows them. */
async function refreshComments() {
  await loadComments();
  renderSidebar();
  renderDiff();
}

/* Opens a file. keepScroll (used by auto-refresh) keeps position, folds and an
   open comment editor, but only when that same file is already rendered. */
async function openFile(path, { keepScroll = false, landOn = 'first', line = null } = {}) {
  const sc = $('#diff-scroll');
  keepScroll = keepScroll && state.diff != null && state.diff.file.path === path;
  if (!keepScroll) {
    state.expanded.clear();
    state.editing = null;
    nav.idx = null;
    if (path !== state.current) state.forced = null;
  }
  state.current = path;
  if (state.info) LS.set('file:' + state.info.root, path);
  const f = state.files.find(x => x.path === path);
  if (f) state.seen[path] = f.sig;
  renderSidebar();
  if (!keepScroll) state.inflight++;
  let r;
  try {
    r = await latest('diff', () => api('/api/diff?' + compareParams({ path, force: state.forced === path ? '1' : '0' }))
      .then(d => ({ d }), e => ({ e })));
  } finally {
    if (!keepScroll) state.inflight--;
  }
  if (!r) return;
  if (r.e) {
    state.diff = null;
    renderHeader();
    renderMessage(`<span class="err">${esc(r.e.message)}</span>`);
    return;
  }
  prepareDiff(r.d);
  state.diff = r.d;
  if (!keepScroll) {
    $('#hs-left').scrollLeft = 0;
    $('#hs-right').scrollLeft = 0;
    sc.scrollTop = 0;
  }
  renderDiff({ keepScroll });
  if (!keepScroll) {
    if (line) scrollToLine(line.side, line.line);
    else scrollToChange(landOn);
  }
}

let refreshTimer;
function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 250);
}

async function refresh() {
  // never override a file the user is just opening (F7, Alt+↓, click):
  // its scroll target would be lost – try again once it has loaded
  if (state.inflight) { scheduleRefresh(); return; }
  try { await loadInfo(); } catch { /* keep previous */ }
  await Promise.all([loadFiles(), loadComments(), loadStatus()]);
  if (state.inflight) { scheduleRefresh(); return; }
  if (state.current && state.files.some(f => f.path === state.current)) {
    await openFile(state.current, { keepScroll: true });
  } else if (state.current) {
    state.diff = null;
    renderHeader();
    renderMessage(t('notInChanges'));
  } else if (state.order.length) {
    openFile(state.order[0]);
  }
  const live = $('#live');
  live.classList.add('flash');
  setTimeout(() => live.classList.remove('flash'), 600);
}

/* Reload into an updated server, but never throw away a comment being typed. */
let pendingReload = null;
function reloadWhenIdle(version) {
  if (state.editing && state.draft.trim()) {
    if (!pendingReload) toast(t('updatedReload', version), false, 10000);
    pendingReload = version;
    return;
  }
  location.reload();
}

function connectEvents() {
  const live = $('#live');
  let first = true;
  const es = new EventSource('/api/events');
  es.addEventListener('hello', ev => {
    let v = '';
    try { v = JSON.parse(ev.data).version || ''; } catch { /* old server */ }
    if (state.info && v && state.info.version && v !== state.info.version) {
      es.close(); // the server was updated: load the new UI
      reloadWhenIdle(v);
      return;
    }
    live.classList.add('on');
    live.title = t('liveOn');
    if (!first) scheduleRefresh(); // reconnected – we may have missed changes
    first = false;
  });
  es.addEventListener('changed', scheduleRefresh);
  es.onerror = () => {
    live.classList.remove('on');
    live.title = t('liveOff');
  };
}

// ---- rendering: toolbar + sidebar --------------------------------------------------------
function renderToolbar() {
  const info = state.info;
  $('#repo-name').textContent = info.name;
  $('#repo-name').title = info.root;
  document.title = `${info.name} – gitreview`;
  const head = info.head;
  $('#branch').textContent = head.detached ? `detached @ ${(head.sha || '').slice(0, 8)}` : head.branch;

  const sel = $('#opt-base');
  const main = info.bases.map(b =>
    `<option value="${esc(b.name)}"${b.exists ? '' : ' disabled'}>${esc(b.name)}` +
    `${b.exists ? (b.ref !== b.name ? ` (${esc(b.ref)})` : '') : ` – ${t('notExists')}`}` +
    `${b.behind ? ` ↓${b.behind}` : ''}</option>`).join('');
  const others = info.branches.filter(b => !info.bases.some(x => x.name === b || x.ref === b));
  const stashes = info.stashes || [];
  sel.innerHTML = main + (others.length
    ? `<optgroup label="${t('otherBranches')}">${others.map(b => `<option value="${esc(b)}">${esc(b)}</option>`).join('')}</optgroup>`
    : '') + (stashes.length
    ? `<optgroup label="${t('stashes')}">${stashes.map(st => `<option value="stash:${esc(st.sha)}" title="${esc(st.message)}">` +
      `${esc(st.ref)}: ${esc(st.message.length > 50 ? st.message.slice(0, 49) + '…' : st.message)} (${esc(ago(st.time))})</option>`).join('')}</optgroup>`
    : '');
  sel.value = state.opts.base;
  applyMode();

  const tr = info.tracking || {}, te = $('#tracking');
  te.textContent = head.detached ? '' : !tr.upstream ? '⇡' : [tr.ahead ? `↑${tr.ahead}` : '', tr.behind ? `↓${tr.behind}` : ''].join(' ').trim();
  te.title = head.detached ? '' : tr.upstream ? t('trackingTitle', tr.upstream, tr.ahead, tr.behind) : t('noUpstream');
  $('#btn-push').disabled = head.detached || (!!tr.upstream && !tr.ahead);
  $('#btn-pull').disabled = head.detached || !tr.upstream;
  renderBehind();
  $('#opt-left').value = state.opts.left;
  $('#opt-right').value = state.opts.right;
  $('#opt-ws').checked = state.opts.ws;
  fitToolbar();
}

/* Drops the select labels (and then the whitespace label) while the toolbar would wrap;
   the controls keep their tooltips. */
function fitToolbar() {
  const bar = $('#toolbar');
  const wraps = () => {
    const items = [...bar.children].filter(e => e.offsetWidth);
    // items of one row are vertically centered, so compare whole boxes, not tops
    return items[items.length - 1].getBoundingClientRect().top >= items[0].getBoundingClientRect().bottom;
  };
  bar.classList.remove('compact', 'compact2');
  if (wraps()) bar.classList.add('compact');
  if (wraps()) bar.classList.add('compact2');
  placeMenu();
}

/* The settings menu opens to the left of the gear, or to the right when there is no room. */
function placeMenu() {
  const menu = $('#settings-menu');
  if (menu.hidden) return;
  menu.style.right = menu.style.left = '';
  const fitsLeft = $('#btn-settings').getBoundingClientRect().right >= menu.offsetWidth + 8;
  if (!fitsLeft) { menu.style.right = 'auto'; menu.style.left = '0'; }
}

/* Badge next to the base selector when the local base branch is behind its
   remote-tracking branch (as of the last fetch). */
function renderBehind() {
  const el = $('#base-behind');
  const b = state.info && state.info.bases.find(x => x.name === state.opts.base);
  el.hidden = !(b && b.behind);
  if (el.hidden) return;
  el.textContent = (b.ahead ? `↑${b.ahead} ` : '') + `↓${b.behind}`;
  el.title = t('behindTitle', b.name, b.upstream, b.behind, b.ahead);
}

/* Fetches all remotes and fast-forwards the local base branches. */
async function fetchRemote() {
  const btn = $('#btn-fetch');
  btn.disabled = true;
  btn.textContent = t('fetching');
  try {
    const r = await api('/api/fetch', { method: 'POST', json: { base: state.opts.base } });
    const msgs = [], warn = [];
    for (const u of r.branches) {
      if (u.status === 'updated') msgs.push(t('ffUpdated', u.branch, u.commits, u.upstream));
      else if (u.status === 'uptodate') msgs.push(t('ffUptodate', u.branch));
      else if (u.status === 'diverged') warn.push(t('ffDiverged', u.branch, u.ahead, u.upstream));
      else if (u.status === 'checkedout') warn.push(t('ffCheckedout', u.branch));
    }
    const all = [...msgs, ...warn];
    toast(`${t('fetched')} ${all.length ? all.join(' · ') : t('nothingToUpdate')}`, warn.length > 0, 9000);
    await refresh();
  } catch (e) {
    toast(e.message, true, 10000);
  } finally {
    btn.disabled = false;
    btn.textContent = t('fetch');
  }
}

function viewedMap() { return state.info ? LS.get('viewed:' + state.info.root, {}) : {}; }

function buildTree(files) {
  const root = { dirs: new Map(), files: [] };
  for (const f of files) {
    const parts = f.path.split('/');
    let n = root;
    for (const p of parts.slice(0, -1)) {
      if (!n.dirs.has(p)) n.dirs.set(p, { name: p, dirs: new Map(), files: [] });
      n = n.dirs.get(p);
    }
    n.files.push(f);
  }
  const sortNode = n => {
    n.dirs = new Map([...n.dirs].sort(([a], [b]) => a.localeCompare(b)));
    n.files.sort((a, b) => a.path.localeCompare(b.path));
    n.dirs.forEach(sortNode);
  };
  sortNode(root);
  return root;
}

function renderSidebar() {
  const sum = $('#summary');
  const tree = $('#tree');
  const c = state.compare;
  if (state.error) {
    sum.innerHTML = `<span class="err">${esc(state.error)}</span>`;
    tree.innerHTML = '';
    state.order = [];
    return;
  }
  if (!c) { sum.textContent = t('loading'); return; }
  const add = state.files.reduce((a, f) => a + f.added, 0);
  const del = state.files.reduce((a, f) => a + f.deleted, 0);
  sum.innerHTML = `<b>${state.files.length}</b> ${t('files', state.files.length)} · <span class="stat"><span class="a">+${add}</span> <span class="d">−${del}</span></span>` +
    `<span class="lbl" title="${esc(sideLabel(c.left))}">◀ ${esc(sideLabel(c.left))}</span>` +
    `<span class="lbl" title="${esc(sideLabel(c.right))}">▶ ${esc(sideLabel(c.right))}</span>` +
    (c.onBase ? `<span class="lbl err">${esc(t('onBase', c.base))}</span>` : '') + stashBoxHtml(c.stash);

  const viewed = viewedMap();
  const counts = {};
  for (const cm of state.comments) counts[cm.file] = (counts[cm.file] || 0) + 1;
  const order = []; // navigation order includes files inside collapsed dirs
  const fileHtml = (f, depth) => {
    const name = f.path.split('/').pop();
    const isViewed = viewed[f.path] === f.sig;
    const changed = (state.seen[f.path] && state.seen[f.path] !== f.sig) || (viewed[f.path] && !isViewed);
    const cls = `tnode tfile st-${f.status}${f.path === state.current ? ' active' : ''}${isViewed ? ' viewed' : ''}`;
    const stat = f.binary ? '<span class="stat">bin</span>'
      : `<span class="stat"><span class="a">+${f.added}</span> <span class="d">−${f.deleted}</span></span>`;
    return `<div class="${cls}" data-path="${esc(f.path)}" style="padding-left:${8 + depth * 14}px" title="${esc(f.path)} – ${statusName(f.status)}">` +
      `<input type="checkbox" class="viewed-chk" title="${t('reviewed')}"${isViewed ? ' checked' : ''}>` +
      `<span class="name">${esc(name)}</span>` +
      (f.oldPath ? `<span class="old">← ${esc(f.oldPath.split('/').pop())}</span>` : '') +
      (changed ? `<span class="dot" title="${t('changedSince')}">●</span>` : '') +
      (counts[f.path] ? `<span class="cc" title="${t('comments')}">✎${counts[f.path]}</span>` : '') +
      stat + '</div>';
  };
  // one sorted traversal: always records the order, emits HTML only when visible
  const walk = (node, prefix, depth, visible) => {
    let h = '';
    for (const d of node.dirs.values()) {
      let name = d.name, cur = d, path = prefix + d.name;
      while (!cur.files.length && cur.dirs.size === 1) { // compact single-child chains
        cur = cur.dirs.values().next().value;
        name += '/' + cur.name;
        path += '/' + cur.name;
      }
      const collapsed = state.collapsedDirs.has(path);
      if (visible) {
        h += `<div class="tnode tdir${collapsed ? ' collapsed' : ''}" data-dir="${esc(path)}" style="padding-left:${8 + depth * 14}px">` +
          `<span class="arrow">▾</span><span>${esc(name)}</span></div>`;
      }
      h += walk(cur, path + '/', depth + 1, visible && !collapsed);
    }
    for (const f of node.files) {
      order.push(f.path);
      if (visible) h += fileHtml(f, depth);
    }
    return h;
  };
  const html = walk(buildTree(state.files), '', 0, true);
  tree.innerHTML = html || `<div class="msg">${c.onBase ? t('noChangesOnBase') : t('noChanges')}</div>`;
  state.order = order;
}

function stashBoxHtml(st) {
  if (!st) return '';
  const c = state.confirm;
  const label = { apply: t('stashApply'), pop: t('stashPop'), drop: t('stashDrop') };
  const acts = c && c.kind === 'stash'
    ? confirmHtml(t('stashConfirm', label[c.action], st.ref), 'data-stash')
    : ['apply', 'pop', 'drop'].map(a => `<button data-stash="${a}"${a === 'drop' ? ' class="danger"' : ''} ` +
      `title="${esc(t('stash' + a[0].toUpperCase() + a.slice(1) + 'Title'))}">${label[a]}</button>`).join('');
  return `<div class="stash-box"><div><b>${esc(st.ref)}</b> · ${esc(ago(st.time))}</div>` +
    `<div class="msg-line">${esc(st.message)}</div><div class="stash-acts">${acts}</div></div>`;
}

// ---- git operations ------------------------------------------------------------------------
function applyMode() {
  const stash = !!stashSha();
  document.body.classList.toggle('stash-mode', stash);
  for (const s of ['#opt-left', '#opt-right', '#btn-comments', '#btn-export', '#btn-copy']) $(s).disabled = stash;
}

async function gitOp(btn, url, json, done) {
  btn.disabled = true;
  try {
    const r = await api(url, { method: 'POST', json });
    toast(done(r), false, 6000);
    return true;
  } catch (e) {
    toast(e.message, true, 12000);
    return false;
  } finally {
    btn.disabled = false;
    await refresh();
  }
}

const pushDone = r => r.created ? t('pushedNew', r.upstream) : t('pushed', r.commits, r.upstream);
const pullDone = r => r.status === 'updated' ? t('pulled', r.commits) : t('pullUptodate');

async function revertFile(path) {
  state.confirm = null;
  try {
    await api('/api/revert', { method: 'POST', json: { path } });
    toast(t('reverted', path));
  } catch (e) { toast(e.message, true, 10000); }
  await refresh();
}

async function stashAction(action) {
  const st = state.compare && state.compare.stash;
  state.confirm = null;
  if (!st) return;
  const label = { apply: t('stashApply'), pop: t('stashPop'), drop: t('stashDrop') }[action];
  try {
    await api('/api/stash', { method: 'POST', json: { sha: st.sha, action } });
    toast(t('stashDone', label, st.ref));
  } catch (e) { toast(e.message, true, 12000); renderSidebar(); return; }
  if (action === 'apply') { await refresh(); return; }
  // the stash is gone: back to the branch comparison
  await loadInfo();
  await changeOpts();
  loadStatus();
}

function renderCommitFiles() {
  const el = $('#cm-files');
  const fs = state.pending.files;
  const moved = headMoved();
  $('#commit-panel [data-act="commit"]').disabled = moved;
  const warn = moved ? `<div class="cm-moved">${esc(t('headMovedWarn'))}<button data-act="ack">${t('headMovedAck')}</button></div>` : '';
  if (!fs.length) { el.innerHTML = warn + `<div class="empty">${t('noPending')}</div>`; return; }
  const c = state.confirm;
  el.innerHTML = warn + fs.map(f => {
    const conf = c && c.kind === 'revert' && c.path === f.path;
    return `<div class="cm-file" data-path="${esc(f.path)}"><label>` +
      `<input type="checkbox" class="cm-chk"${state.cmUnchecked.has(f.path) ? '' : ' checked'}>` +
      `<span class="cm-name">${esc(f.path)}</span>${f.untracked ? `<span class="cm-new">${t('untracked')}</span>` : ''}</label>` +
      (conf ? confirmHtml(t('revertConfirm', f.path), 'data-act')
        : `<button data-act="revert" title="${esc(t('revertTitle'))}">${t('revert')}</button>`) + '</div>';
  }).join('');
}

function toggleCommitPanel(open) {
  const p = $('#commit-panel');
  p.hidden = !open;
  if (!open) return;
  $('#comments-panel').hidden = true;
  state.cmHead = state.pending ? state.pending.head : null;
  renderCommitFiles();
  loadStatus();
  $('#cm-msg').focus();
}

async function doCommit() {
  const paths = state.pending.files.map(f => f.path).filter(p => !state.cmUnchecked.has(p));
  const message = $('#cm-msg').value.trim();
  if (!paths.length) { toast(t('noSelection'), true); return; }
  if (!message) { toast(t('emptyMessage'), true); $('#cm-msg').focus(); return; }
  if (headMoved()) { renderCommitFiles(); return; }
  // the HEAD the user reviewed, so a commit made by someone else meanwhile is refused by the server
  const ok = await gitOp($('#commit-panel [data-act="commit"]'), '/api/commit',
    { paths, message, head: state.cmHead || state.pending.head }, r => t('committed', r.sha.slice(0, 8), paths.length));
  if (ok) { $('#cm-msg').value = ''; state.cmUnchecked.clear(); state.cmHead = null; }
}

// ---- updates (settings menu) ---------------------------------------------------------------
async function checkUpdate() {
  const res = $('#update-result'), now = $('#btn-update-now');
  res.textContent = t('checking');
  now.hidden = true;
  try {
    const u = await api('/api/update');
    res.textContent = u.newer ? t('updateAvailable', u.latest) : t('upToDate', u.current || u.latest);
    now.hidden = !u.newer;
    $('#auto-update').textContent = t(u.auto ? 'autoOn' : 'autoOff');
  } catch (e) { res.textContent = e.message; }
}

async function updateNow() {
  const res = $('#update-result'), now = $('#btn-update-now');
  now.disabled = true;
  res.textContent = t('updating');
  try {
    const u = await api('/api/update', { method: 'POST' });
    res.textContent = t('updateInstalled', u.installed); // the page reloads once the new server is up
  } catch (e) {
    res.textContent = e.message;
    now.disabled = false;
  }
}

// ---- comments panel ------------------------------------------------------------------------
let confirmClear = false;
function renderCommentsPanel() {
  const p = $('#comments-panel');
  const cs = state.comments;
  let h = `<div class="cp-head"><b>${t('comments')} (${cs.length})</b>` +
    `<button data-act="export">Export REVIEW.md</button><button data-act="copy">${t('copy')}</button>` +
    `<button data-act="clear" class="danger">${confirmClear ? t('clearConfirm') : t('clearAll')}</button>` +
    `<button data-act="close">✕</button></div>`;
  if (!cs.length) h += `<div class="empty">${t('noComments')}</div>`;
  let file = null;
  for (const c of cs) {
    if (c.file !== file) { file = c.file; h += `<div class="cp-file">${esc(file)}</div>`; }
    h += `<div class="cp-item" data-file="${esc(c.file)}" data-side="${c.side}" data-line="${c.line}">` +
      `<div class="ln">${c.side === 'left' ? t('sideOld') : t('sideNew')} · ${t('line')} ${c.line}</div>` +
      `<div class="tx">${esc(c.text)}</div></div>`;
  }
  p.innerHTML = h;
}

// ---- actions -----------------------------------------------------------------------------------
function startComment(side, line) {
  if (stashSha()) { toast(t('stashNoComments')); return; }
  if (!state.diff || state.diff.file.path !== state.current) return; // a different file is loading
  state.editing = { side, line };
  state.draft = '';
  renderDiff();
}

async function saveComment() {
  const ed = state.editing;
  const text = state.draft.trim();
  if (!ed || !text) { toast(t('emptyComment'), true); return; }
  try {
    if (ed.id) {
      await api('/api/comments', { method: 'PUT', json: { id: ed.id, text } });
    } else {
      const lines = ed.side === 'left' ? state.diff.left.lines : state.diff.right.lines;
      const from = Math.max(1, ed.line - 2), to = Math.min(lines.length, ed.line + 2);
      await api('/api/comments', {
        method: 'POST',
        json: {
          file: state.current, side: ed.side, line: ed.line, lineText: lines[ed.line - 1] ?? '',
          context: lines.slice(from - 1, to), contextStart: from, text,
        },
      });
    }
  } catch (e) { toast(e.message, true); return; }
  state.editing = null;
  state.draft = '';
  if (pendingReload) { location.reload(); return; }
  await refreshComments();
}

function cancelEdit() {
  state.editing = null;
  state.draft = '';
  if (pendingReload) { location.reload(); return; }
  renderDiff();
}

async function deleteComment(id) {
  try { await api('/api/comments?id=' + encodeURIComponent(id), { method: 'DELETE' }); }
  catch (e) { toast(e.message, true); return; }
  await refreshComments();
}

function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  // plain http on a LAN address: Clipboard API is unavailable, use the legacy path
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  const ok = document.execCommand('copy');
  ta.remove();
  return ok ? Promise.resolve() : Promise.reject(new Error(t('copyFailed')));
}

async function exportReview(write) {
  if (!state.comments.length) { toast(t('nothingToExport')); return; }
  const c = state.compare;
  let r;
  try {
    r = await api('/api/export', {
      method: 'POST',
      json: { write, lang: uiLang, left: c ? sideLabel(c.left) : '', right: c ? sideLabel(c.right) : '' },
    });
  } catch (e) { toast(e.message, true); return; }
  if (write) {
    toast(t('written', r.count, r.path));
  } else {
    try { await copyText(r.markdown); toast(t('copied', r.count)); }
    catch (e) { toast(e.message, true); }
  }
}

const EXPORT_ACTIONS = { export: () => exportReview(true), copy: () => exportReview(false) };

function applyTheme(theme) {
  if (![...$('#set-theme').options].some(o => o.value === theme)) theme = 'dark';
  document.documentElement.dataset.theme = theme;
  $('#set-theme').value = theme;
  LS.set('theme', theme);
}

/* Code font and size: the row height and line-number column scale with the size. */
const FONT_SIZE = { min: 9, max: 24, def: 12.5 };
const FONTS = ['JetBrains Mono', 'Fira Code', 'Fira Mono', 'Source Code Pro', 'Cascadia Code', 'Cascadia Mono', 'IBM Plex Mono',
  'Roboto Mono', 'Iosevka', 'Hack', 'Inconsolata', 'Victor Mono', 'Ubuntu Mono', 'DejaVu Sans Mono', 'Liberation Mono',
  'Noto Sans Mono', 'Droid Sans Mono', 'DM Mono', 'Space Mono', 'SF Mono', 'Menlo', 'Monaco', 'Consolas', 'Courier New', 'FreeMono'];

/* A font is installed when text set in it differs in width from both fallbacks. */
function fontInstalled(name) {
  const ctx = document.createElement('canvas').getContext('2d');
  const w = f => { ctx.font = `32px ${f}`; return ctx.measureText('mmmwwwiiil1|0Oo@').width; };
  return ['serif', 'sans-serif'].some(fb => w(`${JSON.stringify(name)}, ${fb}`) !== w(fb));
}

/* Offers only the fonts this browser actually has, before the "other…" entry. */
function fillFonts() {
  const sel = $('#set-font'), other = sel.querySelector('option[value="__custom"]');
  for (const f of FONTS) {
    if (!fontInstalled(f)) continue;
    const o = document.createElement('option');
    o.value = o.textContent = f;
    sel.insertBefore(o, other);
  }
}

/* UI text size: everything outside the code is sized in rem. */
const UI_SIZE = { min: 10, max: 20, def: 13 };
function applyUiSize(size) {
  size = Math.min(UI_SIZE.max, Math.max(UI_SIZE.min, +size || UI_SIZE.def));
  document.documentElement.style.fontSize = size + 'px';
  $('#set-ui-size').value = size;
  LS.set('uiSize', size);
  fitToolbar();
  updateHScroll();
  renderRuler();
}

function chosenFont() {
  return $('#set-font').value === '__custom' ? $('#set-font-custom').value : $('#set-font').value;
}
function applyFont(family, size) {
  family = (family || '').trim();
  size = Math.min(FONT_SIZE.max, Math.max(FONT_SIZE.min, +size || FONT_SIZE.def));
  const st = document.body.style;
  // a generic family (monospace) must stay unquoted, a font name is quoted
  if (family) st.setProperty('--code-font', `${family === 'monospace' ? family : JSON.stringify(family)}, monospace`);
  else st.removeProperty('--code-font');
  st.setProperty('--code-size', size + 'px');
  st.setProperty('--row-h', Math.round(size * 1.52) + 'px');
  st.setProperty('--num-w', Math.round(size * 4.2) + 'px');
  const sel = $('#set-font'), custom = $('#set-font-custom');
  const listed = [...sel.options].some(o => o.value === family);
  sel.value = listed ? family : '__custom';
  custom.hidden = listed;
  if (!listed) custom.value = family;
  $('#set-size').value = size;
  LS.set('font', family);
  LS.set('fontSize', size);
  measureChar();
  if (state.diff) renderDiff();
}

/* Switches the UI language and redraws everything that holds text. */
function applyLang(l) {
  uiLang = I18N[l] ? l : 'en';
  $('#set-lang').value = uiLang;
  LS.set('lang', uiLang);
  applyStaticTexts();
  const live = $('#live');
  live.title = t(live.classList.contains('on') ? 'liveOn' : 'liveOff');
  if (state.info) renderToolbar();
  renderSidebar();
  if (state.diff) renderDiff();
  if (!$('#comments-panel').hidden) renderCommentsPanel();
}

async function changeOpts() {
  const o = state.opts;
  o.base = $('#opt-base').value;
  state.confirm = null;
  applyMode();
  renderBehind();
  o.left = $('#opt-left').value;
  o.right = $('#opt-right').value;
  o.ws = $('#opt-ws').checked;
  for (const k of Object.keys(o)) LS.set(k, o[k]);
  await loadFiles();
  const target = state.files.some(f => f.path === state.current) ? state.current : state.order[0];
  if (target) openFile(target);
  else { state.current = null; state.diff = null; renderHeader(); renderMessage(state.error ? `<span class="err">${esc(state.error)}</span>` : t('noChanges')); }
}

// ---- events ----------------------------------------------------------------------------------------
function bindUI() {
  ['#opt-base', '#opt-left', '#opt-right', '#opt-ws'].forEach(s => $(s).addEventListener('change', changeOpts));
  const menu = $('#settings-menu'), gear = $('#btn-settings');
  const showMenu = open => {
    menu.hidden = !open;
    gear.setAttribute('aria-expanded', String(open));
    placeMenu();
  };
  gear.onclick = e => { e.stopPropagation(); showMenu(menu.hidden); };
  document.addEventListener('click', e => { if (!menu.hidden && !e.target.closest('.settings')) showMenu(false); });
  $('#set-theme').addEventListener('change', e => applyTheme(e.target.value));
  $('#set-lang').addEventListener('change', e => applyLang(e.target.value));
  $('#set-font').addEventListener('change', e => {
    if (e.target.value !== '__custom') return applyFont(e.target.value, $('#set-size').value);
    const custom = $('#set-font-custom');
    custom.hidden = false;
    custom.focus();
    custom.select();
  });
  // hiding the focused field (another font picked) also fires change: ignore that one
  const applyCustom = () => { if ($('#set-font').value === '__custom') applyFont($('#set-font-custom').value, $('#set-size').value); };
  $('#set-font-custom').addEventListener('change', applyCustom);
  $('#set-font-custom').addEventListener('keydown', e => { if (e.key === 'Enter') applyCustom(); });
  $('#set-size').addEventListener('change', e => applyFont(chosenFont(), e.target.value));
  const stepFont = d => applyFont(chosenFont(), (+$('#set-size').value || FONT_SIZE.def) + d);
  $('#font-dec').onclick = () => stepFont(-0.5);
  $('#font-inc').onclick = () => stepFont(0.5);
  $('#set-ui-size').addEventListener('change', e => applyUiSize(e.target.value));
  const stepUi = d => applyUiSize((+$('#set-ui-size').value || UI_SIZE.def) + d);
  $('#ui-dec').onclick = () => stepUi(-0.5);
  $('#ui-inc').onclick = () => stepUi(0.5);
  $('#btn-export').onclick = EXPORT_ACTIONS.export;
  $('#btn-fetch').onclick = fetchRemote;
  $('#btn-push').onclick = e => gitOp(e.currentTarget, '/api/push', {}, pushDone);
  $('#btn-pull').onclick = e => gitOp(e.currentTarget, '/api/pull', {}, pullDone);
  $('#btn-commit').onclick = () => toggleCommitPanel($('#commit-panel').hidden);
  $('#btn-check-update').onclick = checkUpdate;
  $('#btn-update-now').onclick = updateNow;
  const cp = $('#commit-panel');
  cp.addEventListener('click', e => {
    const btn = e.target.closest('button[data-act]');
    const row = e.target.closest('.cm-file');
    if (!btn) {
      if (e.target.classList.contains('cm-name') && row) {
        e.preventDefault(); // don't toggle the checkbox
        if (state.files.some(f => f.path === row.dataset.path)) openFile(row.dataset.path);
        else toast(t('fileNotCompared'), true);
      }
      return;
    }
    const act = btn.dataset.act;
    if (act === 'close') toggleCommitPanel(false);
    else if (act === 'commit') doCommit();
    else if (act === 'ack') { state.cmHead = state.pending.head; renderCommitFiles(); }
    else if (act === 'revert') { state.confirm = { kind: 'revert', path: row.dataset.path }; renderCommitFiles(); }
    else if (act === 'yes') revertFile(row.dataset.path);
    else if (act === 'no') { state.confirm = null; renderCommitFiles(); }
  });
  cp.addEventListener('change', e => {
    if (!e.target.classList.contains('cm-chk')) return;
    const p = e.target.closest('.cm-file').dataset.path;
    e.target.checked ? state.cmUnchecked.delete(p) : state.cmUnchecked.add(p);
  });
  $('#cm-msg').addEventListener('keydown', e => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); doCommit(); }
  });
  $('#summary').addEventListener('click', e => {
    const b = e.target.closest('button[data-stash]');
    if (!b) return;
    const a = b.dataset.stash;
    if (a === 'yes') stashAction(state.confirm.action);
    else if (a === 'no') { state.confirm = null; renderSidebar(); }
    else if (a === 'apply') stashAction('apply');
    else { state.confirm = { kind: 'stash', action: a }; renderSidebar(); }
  });
  $('#btn-copy').onclick = EXPORT_ACTIONS.copy;
  $('#btn-comments').onclick = () => {
    const p = $('#comments-panel');
    p.hidden = !p.hidden;
    confirmClear = false;
    if (!p.hidden) { $('#commit-panel').hidden = true; renderCommentsPanel(); }
  };

  $('#comments-panel').addEventListener('click', async e => {
    const btn = e.target.closest('button[data-act]');
    if (btn) {
      const act = btn.dataset.act;
      if (act === 'close') $('#comments-panel').hidden = true;
      else if (EXPORT_ACTIONS[act]) EXPORT_ACTIONS[act]();
      else if (act === 'clear') {
        if (!confirmClear) { confirmClear = true; renderCommentsPanel(); return; }
        confirmClear = false;
        try { await api('/api/comments?all=1', { method: 'DELETE' }); } catch (err) { toast(err.message, true); }
        await refreshComments();
      }
      return;
    }
    const item = e.target.closest('.cp-item');
    if (item) {
      const { file, side, line } = item.dataset;
      if (!state.files.some(f => f.path === file)) { toast(t('fileNotCompared'), true); return; }
      if (file === state.current) scrollToLine(side, +line);
      else openFile(file, { line: { side, line: +line } });
    }
  });

  $('#tree').addEventListener('click', e => {
    const dir = e.target.closest('.tdir');
    if (dir) {
      const p = dir.dataset.dir;
      state.collapsedDirs.has(p) ? state.collapsedDirs.delete(p) : state.collapsedDirs.add(p);
      renderSidebar();
      return;
    }
    const node = e.target.closest('.tfile');
    if (!node) return;
    const path = node.dataset.path;
    if (e.target.classList.contains('viewed-chk')) {
      const v = viewedMap();
      const f = state.files.find(x => x.path === path);
      if (e.target.checked && f) v[path] = f.sig; else delete v[path];
      LS.set('viewed:' + state.info.root, v);
      if (f) state.seen[path] = f.sig;
      renderSidebar();
      return;
    }
    openFile(path);
  });

  const sc = $('#diff-scroll');
  let navPosQueued = false;
  sc.addEventListener('click', e => {
    const num = e.target.closest('td.num[data-line]');
    if (num) { startComment(num.dataset.side, +num.dataset.line); return; }
    const fold = e.target.closest('tr.fold');
    if (fold) { state.expanded.add(+fold.dataset.block); renderDiff(); return; }
    const btn = e.target.closest('button[data-act]');
    if (!btn) return;
    const act = btn.dataset.act;
    const box = btn.closest('.cbox');
    if (act === 'save') saveComment();
    else if (act === 'cancel') cancelEdit();
    else if (act === 'edit' && box) {
      const c = state.comments.find(x => x.id === box.dataset.id);
      state.editing = { id: c.id };
      state.draft = c.text;
      renderDiff();
    } else if (act === 'delete' && box) deleteComment(box.dataset.id);
  });
  sc.addEventListener('keydown', e => {
    if (e.target.id !== 'ctext') return;
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); saveComment(); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cancelEdit(); }
  });
  sc.addEventListener('scroll', () => {
    if (Math.abs(sc.scrollTop - nav.expectTop) > 2) nav.idx = null; // user scrolled manually
    if (!navPosQueued) {
      navPosQueued = true;
      requestAnimationFrame(() => { navPosQueued = false; updateNavPos(); });
    }
  });
  $('#diff-header').addEventListener('click', e => {
    const b = e.target.closest('button[data-nav]');
    if (!b) return;
    const nv = b.dataset.nav;
    if (nv === 'expand') toggleExpandAll();
    else if (nv === 'revert') { state.confirm = { kind: 'revert', path: state.current }; renderHeader(); updateNavPos(); }
    else if (nv === 'yes') revertFile(state.confirm.path);
    else if (nv === 'no') { state.confirm = null; renderHeader(); updateNavPos(); }
    else gotoChange(nv === 'next' ? 1 : -1);
  });
  sc.addEventListener('wheel', e => {
    const dx = e.deltaX || (e.shiftKey ? e.deltaY : 0);
    if (!dx) return;
    const td = e.target.closest('td.code[data-s]');
    if (!td) return;
    $(td.dataset.s === 'l' ? '#hs-left' : '#hs-right').scrollLeft += dx;
    e.preventDefault();
  }, { passive: false });
  $('#hs-left').addEventListener('scroll', syncHScroll);
  $('#hs-right').addEventListener('scroll', syncHScroll);
  $('#ruler').addEventListener('click', e => {
    const ru = $('#ruler');
    const y = e.clientY - ru.getBoundingClientRect().top;
    sc.scrollTop = y / ru.clientHeight * sc.scrollHeight - sc.clientHeight / 2;
  });

  window.addEventListener('resize', () => { fitToolbar(); updateHScroll(); renderRuler(); });

  document.addEventListener('keydown', e => {
    const typing = /^(TEXTAREA|INPUT|SELECT)$/.test(e.target.tagName) && e.target.type !== 'checkbox';
    if (e.key === 'Escape') {
      if (!$('#settings-menu').hidden) $('#settings-menu').hidden = true;
      else if (!$('#comments-panel').hidden) $('#comments-panel').hidden = true;
      else if (!$('#commit-panel').hidden) toggleCommitPanel(false);
      else if (state.confirm) { state.confirm = null; renderHeader(); updateNavPos(); renderSidebar(); renderCommitFiles(); }
      else if (state.editing) cancelEdit();
      return;
    }
    if (typing) return;
    if (e.key === 'F7') { e.preventDefault(); gotoChange(e.shiftKey ? -1 : 1); }
    else if (e.altKey && e.key === 'ArrowDown') { e.preventDefault(); gotoFile(1); }
    else if (e.altKey && e.key === 'ArrowUp') { e.preventDefault(); gotoFile(-1); }
  });

  // sidebar resizing
  const rz = $('#resizer');
  rz.addEventListener('mousedown', e => {
    e.preventDefault();
    rz.classList.add('drag');
    const move = ev => {
      const w = Math.min(Math.max(160, ev.clientX), window.innerWidth - 300);
      document.body.style.setProperty('--sidebar-w', w + 'px');
    };
    const up = () => {
      rz.classList.remove('drag');
      document.removeEventListener('mousemove', move);
      document.removeEventListener('mouseup', up);
      LS.set('sidebarW', getComputedStyle(document.body).getPropertyValue('--sidebar-w'));
      updateHScroll();
      renderRuler();
    };
    document.addEventListener('mousemove', move);
    document.addEventListener('mouseup', up);
  });
}

// ---- start ---------------------------------------------------------------------------------------------
async function init() {
  applyTheme(LS.get('theme', 'dark'));
  uiLang = I18N[LS.get('lang', '')] ? LS.get('lang', '') : defaultLang();
  $('#set-lang').value = uiLang;
  applyStaticTexts();
  const sw = LS.get('sidebarW', null);
  if (sw) document.body.style.setProperty('--sidebar-w', sw);
  fillFonts();
  applyUiSize(LS.get('uiSize', UI_SIZE.def));
  applyFont(LS.get('font', ''), LS.get('fontSize', FONT_SIZE.def));
  bindUI();
  try {
    await loadInfo();
  } catch (e) {
    renderMessage(`<span class="err">${esc(e.message)}</span>`);
    return;
  }
  await Promise.all([loadComments(), loadFiles(), loadStatus()]);
  const last = LS.get('file:' + state.info.root, null);
  const first = state.files.some(f => f.path === last) ? last : state.order[0];
  if (first) openFile(first);
  else renderMessage(state.error ? `<span class="err">${esc(state.error)}</span>` : t('noChangesVsBase'));
  connectEvents();
}

init();
