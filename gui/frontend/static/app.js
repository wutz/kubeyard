'use strict';

// The page is a view of the state below. Reads and writes call the methods of
// App (gui/app.go, gui/api.go) through the Wails bindings, and those talk to the
// selected context's API server. A write is confirmed with a second click, then
// the page reloads the catalog.

const state = {
  contexts: [],
  contextError: '',
  selected: '',
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
  sidebarCollapsed: localStorage.getItem('sidebar') === 'collapsed',
};

// Sequence numbers drop responses that a newer request has overtaken.
let catalogSeq = 0;

// After an addon write, Flux reconciles the change in the background. The page
// re-reads the catalog every ADDON_FOLLOW_STEP_MS until the addon settles, and
// gives up after ADDON_FOLLOW_MS.
const ADDON_FOLLOW_STEP_MS = 3000;
const ADDON_FOLLOW_MS = 2 * 60 * 1000;
let addonFollowTimer = 0;

const STATUS_LABEL = {
  installed: ['已安装', 'ok'],
  existing: ['已存在（非 Helm）', ''],
  absent: ['未安装', ''],
  unknown: ['无法判断', ''],
};

// call runs a method of App through the Wails bindings. Wails rejects with the
// error text, so the rejection is turned into an Error the page can show.
async function call(method, ...args) {
  if (!window.go) throw new Error('Wails 绑定不可用，请在 Kubeyard 窗口中打开本页面');
  try {
    return await window.go.main.App[method](...args);
  } catch (err) {
    throw new Error(typeof err === 'string' ? err : err.message);
  }
}

// setTitlebar tells the CSS whether the window has a hidden title bar. Only macOS
// hides it (gui/main.go), so the page leaves room for the traffic lights there.
function setTitlebar() {
  if (!window.runtime) return;
  window.runtime
    .Environment()
    .then((env) => {
      document.documentElement.dataset.titlebar = env.platform === 'darwin' ? 'hidden' : 'native';
    })
    .catch(() => {});
}

// --- loading -----------------------------------------------------------------

async function init() {
  setTitlebar();
  try {
    const data = await call('Contexts');
    state.contexts = data.contexts;
    state.contextError = data.error || '';
    const current = data.contexts.find((c) => c.current) || data.contexts[0];
    state.selected = current ? current.name : '';
  } catch (err) {
    state.contextError = err.message;
  }
  render();
  if (state.selected) refreshCatalog();
}

async function refreshCatalog() {
  const name = state.selected;
  if (!name) return;
  const seq = ++catalogSeq;
  state.catalogLoading = true;
  renderContent();
  try {
    const cat = await call('Catalog', name);
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

// write calls a cluster write method of App for the selected context, with the
// arguments after the context. A successful write clears the install form, so
// the form starts again from what the cluster now runs. A failed one keeps what
// the user typed. An addon write passes settled, see followAddon.
async function write(method, scope, args = [], settled = null) {
  const context = state.selected;
  state.busy = true;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  state.actionScope = scope;
  renderContent();
  let ok = false;
  try {
    state.notice = await call(method, context, ...args);
    ok = true;
  } catch (err) {
    state.actionError = err.message;
  } finally {
    state.busy = false;
  }
  renderContent();
  await refreshCatalog();
  // Cleared only after the catalog is read again: the form is built from the
  // catalog the page holds, and until the read ends that one predates the write.
  if (ok) state.form = null;
  renderContent();
  if (ok && settled) followAddon(args[0], settled);
  return ok;
}

// followAddon re-reads the catalog until the addon's status settles, so the page
// shows what Flux did without a manual refresh. settled takes the status and says
// whether it is the one the write aims for. It stops early when the page moves
// to another context.
function followAddon(name, settled) {
  const context = state.selected;
  const deadline = Date.now() + ADDON_FOLLOW_MS;
  clearTimeout(addonFollowTimer);
  const step = async () => {
    await refreshCatalog();
    if (state.selected !== context) return;
    const ad = list(state.catalog?.addons).find((a) => a.name === name);
    if (!ad || settled(ad.status) || Date.now() >= deadline) return;
    addonFollowTimer = setTimeout(step, ADDON_FOLLOW_STEP_MS);
  };
  addonFollowTimer = setTimeout(step, ADDON_FOLLOW_STEP_MS);
}

// The status an addon write aims for. Uninstall is done once the addon is no
// longer installed; it may stop at existing while Flux removes the rest.
const settledInstalled = (status) => status === 'installed';
const settledRemoved = (status) => status !== 'installed';

// --- navigation and selection ------------------------------------------------

function selectContext(name) {
  clearTimeout(addonFollowTimer);
  state.selected = name;
  state.catalog = null;
  state.catalogError = '';
  // Drop a catalog response still in flight for the previous context.
  catalogSeq++;
  state.catalogLoading = false;
  state.selectedAddon = '';
  state.form = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  render();
  refreshCatalog();
}

function selectAddon(name) {
  state.selectedAddon = name;
  state.form = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  renderContent();
}

function setTheme(theme) {
  state.theme = theme;
  localStorage.setItem('theme', theme);
  render();
}

// setSidebarCollapsed only flips a class on the layout, so the page keeps its
// scroll position. The buttons that show in each state are styled from that class.
function setSidebarCollapsed(collapsed) {
  state.sidebarCollapsed = collapsed;
  localStorage.setItem('sidebar', collapsed ? 'collapsed' : 'open');
  applySidebar();
}

function applySidebar() {
  document.getElementById('app').classList.toggle('sidebar-collapsed', state.sidebarCollapsed);
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
  applySidebar();
}

// renderContent redraws only the page, so an open context menu in the top bar
// stays open.
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
    el(
      'div',
      { class: 'sidebar-head' },
      iconButton('收起侧边栏', () => setSidebarCollapsed(true)),
    ),
    clusterPicker(),
    el('div', { class: 'nav-group' }, '管理'),
    el('button', { class: 'nav-item', 'aria-current': 'page' }, '组件'),
    el('div', { class: 'sidebar-foot' }, el('label', {}, '主题 ', themeSelect)),
  );
}

// clusterPicker selects the kubeconfig context the page reads. It sits at the
// top of the sidebar, where the other selections of the page are made.
function clusterPicker() {
  if (!state.contexts.length) return el('div', { class: 'status' }, '未找到 kubeconfig context');
  return el(
    'div',
    { class: 'cluster-picker' },
    el('span', { class: 'muted small' }, '集群'),
    el(
      'select',
      {
        class: 'select',
        'aria-label': '集群',
        title: state.selected,
        onchange: (e) => selectContext(e.target.value),
      },
      state.contexts.map((c) =>
        el('option', { value: c.name, selected: c.name === state.selected }, c.name === state.selected ? `${c.name}（当前）` : c.name),
      ),
    ),
  );
}

// iconButton is a 28px square button with the sidebar glyph. Its label is the
// accessible name; the buttons show or hide by the layout's collapsed class.
function iconButton(label, onclick, cls = '') {
  return el('button', { class: `icon-btn ${cls}`, 'aria-label': label, title: label, onclick }, sidebarGlyph());
}

const SVG_NS = 'http://www.w3.org/2000/svg';

// sidebarGlyph draws a panel with a divider, the usual sidebar toggle.
function sidebarGlyph() {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 16 16');
  svg.setAttribute('width', '16');
  svg.setAttribute('height', '16');
  svg.setAttribute('aria-hidden', 'true');
  for (const [tag, attrs] of [
    ['rect', { x: '1.5', y: '2.5', width: '13', height: '11', rx: '2' }],
    ['path', { d: 'M6 2.5v11' }],
  ]) {
    const node = document.createElementNS(SVG_NS, tag);
    for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value);
    svg.append(node);
  }
  return svg;
}

function mainColumn() {
  return el('div', { class: 'main' }, topbar(), el('main', { class: 'content' }, pageContent()));
}

// topbar holds the page actions. The expand button is there for the collapsed
// sidebar only; the CSS shows it when the layout has the sidebar collapsed.
function topbar() {
  const hasContexts = state.contexts.length > 0;
  return el(
    'header',
    { class: 'topbar' },
    iconButton('展开侧边栏', () => setSidebarCollapsed(false), 'sidebar-expand'),
    el('span', { class: 'spacer' }),
    hasContexts ? el('button', { class: 'btn', onclick: refreshCatalog }, '刷新') : null,
  );
}

function pageContent() {
  if (!state.selected) return emptyState();
  return catalogPage();
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
        run: () => write('InstallFlux', 'source'),
      }),
    );
  }
  if (!src && cat.fluxInstalled) {
    actions.push(
      el(
        'button',
        { class: 'btn', disabled: state.busy, onclick: () => write('AddCatalogSource', 'source') },
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
  const fieldsOf = () => (state.form ? state.form.fields : {});
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
        run: () => write('InstallAddon', 'addon', [ad.name, fieldsOf()], settledInstalled),
      }),
      ad.managed
        ? confirmButton('update', '更新参数', '确认更新', {
            run: () => write('UpdateAddon', 'addon', [ad.name, fieldsOf()], settledInstalled),
          })
        : null,
      confirmButton('uninstall', '卸载', '确认卸载', {
        confirmCls: 'btn btn-danger',
        disabled: Boolean(ad.uninstallBlocked),
        run: () => write('UninstallAddon', 'addon', [ad.name], settledRemoved),
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
