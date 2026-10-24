'use strict';

// The page is a view of the state below. Reads and writes go to the local API
// (see gui/server.go), which talks to the selected context's API server. A
// write is confirmed with a second click, then the page reloads both the
// overview and the catalog.

const token = document.querySelector('meta[name="kubeyard-token"]').content;
const POLL_MS = 8000;

const state = {
  contexts: [],
  contextError: '',
  selected: '',
  page: 'overview',
  overview: null,
  overviewError: '',
  overviewLoading: false,
  overviewAt: null,
  catalog: null,
  catalogError: '',
  catalogLoading: false,
  selectedAddon: '',
  // form holds the install form of one addon: its path-keyed fields.
  form: null,
  // confirm names the action waiting for its second click.
  confirm: '',
  busy: false,
  // notice and actionError show the outcome of the last write, in the area
  // the write was started from: "source" for Flux and the catalog source,
  // "addon" for an addon.
  notice: '',
  actionError: '',
  actionScope: '',
  theme: localStorage.getItem('theme') || 'auto',
};

// Sequence numbers drop responses that a newer request has overtaken.
let overviewSeq = 0;
let catalogSeq = 0;

const STATUS_LABEL = {
  installed: ['已安装', 'ok'],
  existing: ['已存在（非 Helm）', ''],
  absent: ['未安装', ''],
  unknown: ['无法判断', ''],
};

async function api(method, path, body) {
  const opts = { method, headers: { 'X-Kubeyard-Token': token } };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

// --- loading -----------------------------------------------------------------

async function init() {
  try {
    const data = await api('GET', '/api/contexts');
    state.contexts = data.contexts;
    state.contextError = data.error || '';
    const current = data.contexts.find((c) => c.current) || data.contexts[0];
    state.selected = current ? current.name : '';
  } catch (err) {
    state.contextError = err.message;
  }
  render();
  if (state.selected) refreshOverview();
  setInterval(() => {
    if (state.page === 'overview' && state.selected && !state.overviewLoading) refreshOverview();
  }, POLL_MS);
}

async function refreshOverview() {
  const name = state.selected;
  if (!name) return;
  const seq = ++overviewSeq;
  state.overviewLoading = true;
  renderContent();
  try {
    const ov = await api('GET', `/api/overview?context=${encodeURIComponent(name)}`);
    if (seq !== overviewSeq) return;
    state.overview = ov;
    state.overviewError = '';
    state.overviewAt = new Date();
  } catch (err) {
    if (seq !== overviewSeq) return;
    state.overview = null;
    state.overviewError = err.message;
  } finally {
    if (seq === overviewSeq) {
      state.overviewLoading = false;
      renderContent();
    }
  }
}

async function refreshCatalog() {
  const name = state.selected;
  if (!name) return;
  const seq = ++catalogSeq;
  state.catalogLoading = true;
  renderContent();
  try {
    const cat = await api('GET', `/api/catalog?context=${encodeURIComponent(name)}`);
    if (seq !== catalogSeq) return;
    state.catalog = cat;
    state.catalogError = '';
  } catch (err) {
    if (seq !== catalogSeq) return;
    state.catalog = null;
    state.catalogError = err.message;
  } finally {
    if (seq === catalogSeq) {
      state.catalogLoading = false;
      renderContent();
    }
  }
}

// write sends a cluster write for the selected context. A successful write
// clears the install form, so the form starts again from what the cluster now
// runs. A failed one keeps what the user typed.
async function write(path, scope, extra) {
  const context = state.selected;
  state.busy = true;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  state.actionScope = scope;
  renderContent();
  let ok = false;
  try {
    const res = await api('POST', path, { context, ...extra });
    state.notice = res.message;
    ok = true;
  } catch (err) {
    state.actionError = err.message;
  } finally {
    state.busy = false;
  }
  if (ok) state.form = null;
  renderContent();
  await Promise.all([refreshOverview(), refreshCatalog()]);
}

// --- navigation and selection ------------------------------------------------

function selectContext(name) {
  state.selected = name;
  state.overview = null;
  state.overviewError = '';
  state.catalog = null;
  state.catalogError = '';
  state.selectedAddon = '';
  state.form = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  render();
  refreshOverview();
  if (state.page === 'catalog') refreshCatalog();
}

function setPage(page) {
  state.page = page;
  state.confirm = '';
  render();
  if (page === 'catalog' && !state.catalog && !state.catalogLoading) refreshCatalog();
}

function selectAddon(name) {
  state.selectedAddon = name;
  state.form = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  renderContent();
}

function refreshCurrent() {
  if (state.page === 'overview') refreshOverview();
  else refreshCatalog();
}

function setTheme(theme) {
  state.theme = theme;
  localStorage.setItem('theme', theme);
  render();
}

// --- rendering helpers -------------------------------------------------------

// el builds an element. Keys that start with "on" are event listeners, the
// listed keys are properties, and everything else is an attribute. Text is
// always set as text, never as HTML. Null, false and undefined children are
// skipped, and arrays are flattened.
const PROPERTIES = new Set(['value', 'checked', 'disabled', 'selected']);

function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else if (PROPERTIES.has(key)) node[key] = value;
    else node.setAttribute(key, value === true ? '' : String(value));
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

function list(v) {
  return Array.isArray(v) ? v : [];
}

function enc(s) {
  return encodeURIComponent(s);
}

function gib(bytes) {
  return `${Math.round(bytes / 2 ** 30)} GiB`;
}

function fmtTime(t) {
  return new Date(t).toLocaleTimeString('zh-CN', { hour12: false });
}

function healthText(health) {
  const items = list(health);
  return items.length ? items.map((h) => `${h.kind}/${h.name}`).join(', ') : '无';
}

function placeholderText(p) {
  return p.default === undefined || p.default === null ? p.title : `默认 ${p.default}`;
}

function alert(text, kind) {
  return el('div', { class: `alert ${kind}`, role: kind === 'error' ? 'alert' : 'status' }, text);
}

function badge(text, kind) {
  return el('span', { class: `badge ${kind || ''}` }, text);
}

// confirmButton is a button that needs a second click. The first click turns
// it into the confirm label, in the confirm class; the second runs the action.
function confirmButton(key, label, confirmLabel, { cls = 'btn', confirmCls = 'btn btn-primary', disabled = false, run }) {
  const confirming = state.confirm === key;
  return el(
    'button',
    {
      class: confirming ? confirmCls : cls,
      disabled: disabled || state.busy,
      onclick: () => {
        if (!confirming) {
          state.confirm = key;
          renderContent();
          return;
        }
        run();
      },
    },
    confirming ? confirmLabel : label,
  );
}

function cancelButton() {
  if (!state.confirm) return null;
  return el(
    'button',
    {
      class: 'btn',
      onclick: () => {
        state.confirm = '';
        renderContent();
      },
    },
    '取消',
  );
}

function messages(scope) {
  if (state.actionScope !== scope) return null;
  return [
    state.actionError ? alert(state.actionError, 'error') : null,
    state.notice ? alert(state.notice, 'success') : null,
  ];
}

// --- layout ------------------------------------------------------------------

function render() {
  document.documentElement.dataset.theme = state.theme === 'auto' ? '' : state.theme;
  document.getElementById('app').replaceChildren(sidebar(), mainColumn());
}

// renderContent redraws only the page. Polling uses it, so an open context
// menu in the top bar stays open.
function renderContent() {
  const content = document.querySelector('.content');
  if (!content) {
    render();
    return;
  }
  content.replaceChildren(pageContent());
}

function sidebar() {
  const themeSelect = el(
    'select',
    { class: 'select', 'aria-label': '主题', onchange: (e) => setTheme(e.target.value) },
    [
      ['auto', '跟随系统'],
      ['light', '浅色'],
      ['dark', '深色'],
    ].map(([value, label]) => el('option', { value, selected: state.theme === value }, label)),
  );
  return el(
    'aside',
    { class: 'sidebar' },
    el('div', { class: 'brand' }, el('span', { class: 'logo' }, 'K'), 'Kubeyard'),
    el('div', { class: 'nav-group' }, '工作区'),
    navItem('overview', '概览'),
    el('div', { class: 'nav-group' }, '管理'),
    navItem('catalog', '组件'),
    el('div', { class: 'sidebar-foot' }, el('label', {}, '主题 ', themeSelect)),
  );
}

function navItem(page, label) {
  return el(
    'button',
    {
      class: 'nav-item',
      'aria-current': state.page === page ? 'page' : null,
      onclick: () => setPage(page),
    },
    label,
  );
}

function mainColumn() {
  return el('div', { class: 'main' }, topbar(), el('main', { class: 'content' }, pageContent()));
}

function topbar() {
  const hasContexts = state.contexts.length > 0;
  return el(
    'header',
    { class: 'topbar' },
    el('span', { class: 'muted small' }, '集群'),
    hasContexts
      ? el(
          'select',
          {
            class: 'select',
            'aria-label': '集群',
            onchange: (e) => selectContext(e.target.value),
          },
          state.contexts.map((c) =>
            el('option', { value: c.name, selected: c.name === state.selected }, c.current ? `${c.name}（当前）` : c.name),
          ),
        )
      : null,
    hasContexts ? el('button', { class: 'btn', onclick: refreshCurrent }, '刷新') : null,
    el('span', { class: 'spacer' }),
    hasContexts ? null : el('span', { class: 'status' }, '未找到 kubeconfig context'),
  );
}

function pageContent() {
  if (!state.selected) return emptyState();
  return state.page === 'overview' ? overviewPage() : catalogPage();
}

function emptyState() {
  return el(
    'div',
    { class: 'empty' },
    el('strong', {}, '未找到 kubeconfig context'),
    el('span', { class: 'muted small' }, '请检查 KUBECONFIG 或 ~/.kube/config'),
    state.contextError ? el('span', { class: 'bad-text small' }, state.contextError) : null,
  );
}

function pageHead(title, subtitle, aside) {
  return el(
    'div',
    { class: 'page-head' },
    el('div', {}, el('h1', {}, title), el('p', {}, subtitle)),
    aside || null,
  );
}

// --- overview ----------------------------------------------------------------

function overviewPage() {
  const ov = state.overview;
  const live = state.overviewAt ? `实时 · 每 8 秒 · 更新于 ${fmtTime(state.overviewAt)}` : '实时 · 每 8 秒';
  const head = pageHead(
    '概览',
    ov ? `Pods 共 ${ov.podsRunning + ov.podsPending} · Pending 无节点 ${ov.podsUnscheduled}` : `集群 ${state.selected}`,
    el('span', { class: 'live' }, live),
  );
  if (state.overviewError) return el('div', { class: 'stack' }, head, alert(state.overviewError, 'error'));
  if (!ov) return el('div', { class: 'stack' }, head, el('p', { class: 'muted' }, '加载中…'));
  return el(
    'div',
    { class: 'stack' },
    head,
    el(
      'div',
      { class: 'cards' },
      metricCard('节点就绪', `${ov.nodesReady} / ${ov.nodesTotal}`, 'Ready 的节点数'),
      metricCard('GPU 分配', `${ov.gpuRequested} / ${ov.gpuTotal}`, 'requests / allocatable', gpuPercent(ov)),
      metricCard('Pod 运行中', ov.podsRunning, `Pending ${ov.podsPending} · 失败 ${ov.podsFailed}`),
    ),
    nodeTable(list(ov.nodes)),
  );
}

function gpuPercent(ov) {
  return ov.gpuTotal > 0 ? Math.min(100, (ov.gpuRequested / ov.gpuTotal) * 100) : 0;
}

function metricCard(label, value, sub, percent) {
  const bar = percent === undefined ? null : el('div', { class: 'meter' }, el('span', {}));
  if (bar) bar.firstChild.style.width = `${percent}%`;
  return el(
    'div',
    { class: 'card' },
    el('div', { class: 'card-label' }, label),
    el('div', { class: 'card-value' }, String(value)),
    el('div', { class: 'card-sub' }, sub),
    bar,
  );
}

function nodeTable(nodes) {
  const cols = [
    ['节点'],
    ['状态'],
    ['角色'],
    ['GPU 型号'],
    ['GPU 分配', 'num'],
    ['CPU', 'num'],
    ['内存', 'num'],
    ['Kubelet'],
  ];
  const rows = nodes.map((n) =>
    el(
      'tr',
      {},
      el('td', { class: 'mono' }, n.name),
      el('td', {}, n.ready ? badge('Ready', 'ok') : badge('NotReady', 'bad')),
      el('td', {}, list(n.roles).join(', ') || '—'),
      el('td', {}, n.gpuProduct || '—'),
      el('td', { class: 'num' }, n.gpuTotal ? `${n.gpuRequested} / ${n.gpuTotal}` : '—'),
      el('td', { class: 'num' }, `${(n.cpuRequestedMilli / 1000).toFixed(1)} / ${Math.floor(n.cpuMilli / 1000)}`),
      el('td', { class: 'num' }, `${gib(n.memRequestedBytes)} / ${gib(n.memBytes)}`),
      el('td', { class: 'mono' }, n.version),
    ),
  );
  return el(
    'div',
    { class: 'table-wrap' },
    el(
      'table',
      {},
      el('thead', {}, el('tr', {}, cols.map(([label, cls]) => el('th', { class: cls || '' }, label)))),
      el(
        'tbody',
        {},
        rows.length ? rows : el('tr', {}, el('td', { class: 'muted', colspan: cols.length }, '没有节点')),
      ),
    ),
  );
}

// --- catalog -----------------------------------------------------------------

function catalogPage() {
  const head = pageHead('组件', `目录与集群中的组件 · ${state.selected}`, null);
  if (state.catalogError) return el('div', { class: 'stack' }, head, alert(state.catalogError, 'error'));
  const cat = state.catalog;
  if (!cat) return el('div', { class: 'stack' }, head, el('p', { class: 'muted' }, state.catalogLoading ? '加载中…' : ''));
  return el(
    'div',
    { class: 'stack' },
    head,
    sourcePanel(cat),
    cat.addons ? el('p', { class: 'muted small' }, '安装与卸载只作用于 Kubeyard 管理的组件；已存在的组件不会被重复安装。') : null,
    cat.addons ? addonTable(cat) : null,
    cat.addons ? addonDetail(selectedAddon(cat)) : null,
  );
}

function sourcePanel(cat) {
  const src = cat.source;
  const line = src
    ? `源 ${src.namespace}/${src.name} · ${src.revision || '未解析版本'} · ${src.verified ? 'cosign 已校验' : '未校验'}`
    : 'catalog 源：未添加';
  const actions = [];
  if (cat.fluxInstallable) {
    actions.push(
      confirmButton('flux', '安装 Flux', '确认安装 Flux', {
        run: () => write('/api/flux', 'source', {}),
      }),
    );
  }
  if (!src && cat.fluxInstalled) {
    actions.push(
      el(
        'button',
        { class: 'btn', disabled: state.busy, onclick: () => write('/api/catalog-source', 'source', {}) },
        '添加 catalog 源',
      ),
    );
  }
  return el(
    'section',
    { class: 'card stack' },
    el('div', { class: 'mono small muted' }, line),
    cat.note ? el('p', { class: 'muted' }, cat.note) : null,
    cat.error ? alert(cat.error, 'error') : null,
    actions.length ? el('div', { class: 'actions' }, actions) : null,
    messages('source'),
  );
}

function selectedAddon(cat) {
  const addons = list(cat.addons);
  return addons.find((a) => a.name === state.selectedAddon) || addons[0];
}

function addonTable(cat) {
  const cols = [
    ['组件'],
    ['分类'],
    ['版本'],
    ['适用范围'],
    ['依赖'],
    ['状态'],
    ['说明'],
  ];
  const selected = selectedAddon(cat);
  const rows = list(cat.addons).map((ad) => {
    const [label, kind] = STATUS_LABEL[ad.status] || STATUS_LABEL.unknown;
    const isSelected = selected && ad.name === selected.name;
    return el(
      'tr',
      {
        class: `selectable ${isSelected ? 'selected' : ''}`,
        tabindex: 0,
        'aria-selected': isSelected ? 'true' : 'false',
        onclick: () => selectAddon(ad.name),
        onkeydown: (e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            selectAddon(ad.name);
          }
        },
      },
      el('td', { class: 'mono' }, ad.name),
      el('td', {}, ad.category),
      el('td', { class: 'mono' }, ad.version),
      el('td', {}, list(ad.scope).join(', ') || '—'),
      el('td', {}, list(ad.dependsOn).join(', ') || '—'),
      el('td', {}, badge(label, kind)),
      el('td', {}, ad.displayName),
    );
  });
  return el(
    'div',
    { class: 'table-wrap' },
    el(
      'table',
      {},
      el('thead', {}, el('tr', {}, cols.map(([label]) => el('th', {}, label)))),
      el('tbody', {}, rows),
    ),
  );
}

function addonDetail(ad) {
  if (!ad) return null;
  const form = ensureForm(ad);
  return el(
    'section',
    { class: 'card detail' },
    el('div', {}, el('h2', {}, ad.displayName), el('p', { class: 'lede' }, ad.description)),
    el('div', { class: 'mono small muted' }, `命名空间 ${ad.namespace} · 健康检查 ${healthText(ad.health)}`),
    fluxSection(ad),
    paramForm(ad, form),
    actionRow(ad),
    messages('addon'),
  );
}

// ensureForm returns the install form of an addon, starting it from the values
// the addon is installed with, or from the schema defaults.
function ensureForm(ad) {
  if (state.form && state.form.addon === ad.name) return state.form;
  const current = ad.current || {};
  const fields = {};
  for (const p of list(ad.params)) {
    if (p.type === 'boolean') {
      fields[p.path] = typeof current[p.path] === 'boolean' ? current[p.path] : p.default === true;
    } else {
      fields[p.path] = current[p.path] === undefined || current[p.path] === null ? '' : String(current[p.path]);
    }
  }
  state.form = { addon: ad.name, fields };
  return state.form;
}

// fluxSection shows the Flux state of an addon Kubeyard manages.
function fluxSection(ad) {
  if (!ad.managed) {
    if (ad.status !== 'installed') return null;
    return el('p', { class: 'muted small' }, '由其他方式安装，Kubeyard 不管理它，也不显示 Flux 状态');
  }
  if (ad.fluxError) return el('p', { class: 'muted small' }, ad.fluxError);
  if (!ad.flux) return null;
  const conditions = list(ad.flux.conditions);
  const events = list(ad.flux.events);
  return el(
    'div',
    {},
    el('h3', { class: 'section-title' }, 'Flux 状态'),
    el(
      'div',
      { class: 'flux-lines' },
      conditions.map((c) =>
        el(
          'div',
          { class: `line ${c.ready ? 'ok-text' : 'bad-text'}` },
          `${c.kind}/${c.name}  ${c.ready ? 'Ready' : '未就绪'}  ${c.message}`,
        ),
      ),
    ),
    events.length
      ? el(
          'div',
          {},
          el('h3', { class: 'section-title' }, '最近事件'),
          el(
            'div',
            { class: 'flux-lines muted' },
            events.map((ev) => el('div', { class: 'line' }, `${fmtTime(ev.time)}  ${ev.reason}  ${ev.object}  ${ev.message}`)),
          ),
        )
      : null,
  );
}

// paramForm shows one input per parameter, in the order the schema lists them.
function paramForm(ad, form) {
  const params = list(ad.params);
  if (!params.length) return el('p', { class: 'muted small' }, '无参数');
  return el(
    'div',
    {},
    el('h3', { class: 'section-title' }, '参数'),
    el(
      'div',
      { class: 'form' },
      params.flatMap((p) => [
        el('span', { class: `label ${p.required ? 'required' : ''}` }, p.path),
        p.type === 'boolean'
          ? el(
              'label',
              { class: 'actions' },
              el('input', {
                type: 'checkbox',
                class: 'checkbox',
                checked: form.fields[p.path] === true,
                onchange: (e) => {
                  form.fields[p.path] = e.target.checked;
                },
              }),
              el('span', { class: 'muted small' }, p.title),
            )
          : el('input', {
              class: 'input',
              type: 'text',
              value: form.fields[p.path] ?? '',
              placeholder: placeholderText(p),
              'aria-label': p.path,
              oninput: (e) => {
                form.fields[p.path] = e.target.value;
              },
            }),
      ]),
    ),
  );
}

// actionRow shows install, update and uninstall. A button is disabled when the
// catalog or the cluster refuses the action, and the reason is shown below.
function actionRow(ad) {
  const addonPath = `/api/addons/${enc(ad.name)}`;
  const fieldsOf = () => ({ fields: state.form ? state.form.fields : {} });
  const uninstallWarning =
    state.confirm === 'uninstall' && !ad.uninstallBlocked && ad.uninstallWarning ? ad.uninstallWarning : '';
  return el(
    'div',
    { class: 'stack' },
    el(
      'div',
      { class: 'actions' },
      confirmButton('install', '安装', '确认安装', {
        cls: 'btn btn-primary',
        disabled: Boolean(ad.installBlocked),
        run: () => write(`${addonPath}/install`, 'addon', fieldsOf()),
      }),
      ad.managed
        ? confirmButton('update', '更新参数', '确认更新', {
            run: () => write(`${addonPath}/update`, 'addon', fieldsOf()),
          })
        : null,
      confirmButton('uninstall', '卸载', '确认卸载', {
        confirmCls: 'btn btn-danger',
        disabled: Boolean(ad.uninstallBlocked),
        run: () => write(`${addonPath}/uninstall`, 'addon', {}),
      }),
      cancelButton(),
      state.busy ? el('span', { class: 'muted small' }, '正在提交到集群…') : null,
    ),
    ad.installBlocked ? el('p', { class: 'muted small' }, `安装：${ad.installBlocked}`) : null,
    ad.uninstallBlocked ? el('p', { class: 'muted small' }, `卸载：${ad.uninstallBlocked}`) : null,
    uninstallWarning ? el('p', { class: 'danger-text small' }, uninstallWarning) : null,
  );
}

init();
