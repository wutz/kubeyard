'use strict';

// The page is a view of the state below. Reads and writes call the methods of
// App (gui/app.go, gui/api.go) through the Wails bindings, and those talk to the
// selected context's API server. A write is confirmed with a second click, then
// the page reloads both the overview and the catalog.

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
  // pools is the resource pool page; poolsAt is when it last loaded.
  pools: null,
  poolsError: '',
  poolsLoading: false,
  poolsAt: null,
  // clusters is the virtual cluster page.
  clusters: null,
  clustersError: '',
  clustersLoading: false,
  clustersAt: null,
  // selectedCluster names the virtual cluster whose detail is open.
  selectedCluster: '',
  // adminView is the overview read through a cluster's admin credential, shown
  // until the page, the context or the cluster changes.
  adminView: null,
  selectedAddon: '',
  // form holds the install form of one addon: its path-keyed fields.
  form: null,
  // confirm names the action waiting for its second click.
  confirm: '',
  busy: false,
  // desired is the open desired-size editor of one claim, see desiredEditor.
  desired: null,
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
let overviewSeq = 0;
let catalogSeq = 0;
let poolsSeq = 0;
let clustersSeq = 0;

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
    const ov = await call('Overview', name);
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

async function refreshPools() {
  const name = state.selected;
  if (!name) return;
  const seq = ++poolsSeq;
  state.poolsLoading = true;
  renderContent();
  try {
    const view = await call('Pools', name);
    if (seq !== poolsSeq) return;
    state.pools = view;
    state.poolsError = '';
    state.poolsAt = new Date();
  } catch (err) {
    if (seq !== poolsSeq) return;
    state.pools = null;
    state.poolsError = err.message;
  } finally {
    if (seq === poolsSeq) {
      state.poolsLoading = false;
      renderContent();
    }
  }
}

async function refreshClusters() {
  const name = state.selected;
  if (!name) return;
  const seq = ++clustersSeq;
  state.clustersLoading = true;
  renderContent();
  try {
    const view = await call('VirtualClusters', name);
    if (seq !== clustersSeq) return;
    state.clusters = view;
    state.clustersError = '';
    state.clustersAt = new Date();
  } catch (err) {
    if (seq !== clustersSeq) return;
    state.clusters = null;
    state.clustersError = err.message;
  } finally {
    if (seq === clustersSeq) {
      state.clustersLoading = false;
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
  // A pin changes the pool and the cluster pages, so both are read again too.
  await Promise.all([refreshOverview(), refreshCatalog(), refreshPools(), refreshClusters()]);
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
  state.overview = null;
  state.overviewError = '';
  state.catalog = null;
  state.catalogError = '';
  // Drop a pools response still in flight for the previous context.
  poolsSeq++;
  state.pools = null;
  state.poolsError = '';
  state.poolsLoading = false;
  state.poolsAt = null;
  // The same for the virtual cluster page.
  clustersSeq++;
  state.clusters = null;
  state.clustersError = '';
  state.clustersLoading = false;
  state.clustersAt = null;
  state.selectedCluster = '';
  state.adminView = null;
  state.selectedAddon = '';
  state.form = null;
  state.desired = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  render();
  refreshOverview();
  if (state.page === 'catalog') refreshCatalog();
  if (state.page === 'pools') refreshPools();
  if (state.page === 'clusters') refreshClusters();
}

function setPage(page) {
  state.page = page;
  state.adminView = null;
  state.desired = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  render();
  if (page === 'catalog' && !state.catalog && !state.catalogLoading) refreshCatalog();
  if (page === 'pools') refreshPools();
  if (page === 'clusters') refreshClusters();
}

// selectCluster opens the detail of one virtual cluster, or closes it when the
// same one is clicked again.
function selectCluster(name) {
  state.selectedCluster = state.selectedCluster === name ? '' : name;
  state.adminView = null;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  renderContent();
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
  else if (state.page === 'pools') refreshPools();
  else if (state.page === 'clusters') refreshClusters();
  else refreshCatalog();
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
  applySidebar();
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
    el(
      'div',
      { class: 'sidebar-head' },
      iconButton('收起侧边栏', () => setSidebarCollapsed(true)),
    ),
    clusterPicker(),
    el('div', { class: 'nav-group' }, '工作区'),
    navItem('overview', '概览'),
    el('div', { class: 'nav-group' }, '资源'),
    navItem('clusters', '虚拟集群'),
    navItem('pools', '资源池'),
    el('div', { class: 'nav-group' }, '管理'),
    navItem('catalog', '组件'),
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

// topbar holds the page actions. The expand button is there for the collapsed
// sidebar only; the CSS shows it when the layout has the sidebar collapsed.
function topbar() {
  const hasContexts = state.contexts.length > 0;
  return el(
    'header',
    { class: 'topbar' },
    iconButton('展开侧边栏', () => setSidebarCollapsed(false), 'sidebar-expand'),
    el('span', { class: 'spacer' }),
    hasContexts ? el('button', { class: 'btn', onclick: refreshCurrent }, '刷新') : null,
  );
}

function pageContent() {
  if (!state.selected) return emptyState();
  if (state.page === 'overview') return overviewPage();
  if (state.page === 'clusters') return clustersPage();
  if (state.page === 'pools') return poolsPage();
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

function meterBar(percent) {
  const fill = el('span', {});
  fill.style.width = `${percent}%`;
  return el('div', { class: 'meter' }, fill);
}

function metricCard(label, value, sub, percent) {
  const bar = percent === undefined ? null : meterBar(percent);
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

// --- virtual clusters --------------------------------------------------------

// PHASE_LABEL names the VirtualCluster phases and the badge colour of each.
const PHASE_LABEL = {
  Pending: ['等待', ''],
  Provisioning: ['创建中', ''],
  Ready: ['就绪', 'ok'],
  Degraded: ['降级', 'warn'],
  Draining: ['回收中', 'warn'],
  Deleting: ['删除中', 'bad'],
};

// phaseBadge shows the phase the operator reported. The host operator does not
// set a phase yet (docs/roadmap.md M2), so an empty phase shows no badge: a
// "未报告" badge on every cluster reads as a fault, and the card already shows
// the claims and the nodes held.
function phaseBadge(phase) {
  if (!phase) return null;
  const [label, kind] = PHASE_LABEL[phase] || [phase, ''];
  return badge(label, kind);
}

// clustersPage is read-only except for the export of an admin kubeconfig: the
// operator owns the claims, so the page does not edit them.
function clustersPage() {
  const view = state.clusters;
  const count = view && view.installed ? `VirtualCluster · ${list(view.clusters).length}` : `集群 ${state.selected}`;
  const head = pageHead(
    '虚拟集群',
    count,
    state.clustersAt ? el('span', { class: 'live' }, `更新于 ${fmtTime(state.clustersAt)}`) : null,
  );
  if (state.clustersError) return el('div', { class: 'stack' }, head, alert(state.clustersError, 'error'));
  if (!view) return el('div', { class: 'stack' }, head, el('p', { class: 'muted' }, '加载中…'));
  if (!view.installed) {
    return el(
      'div',
      { class: 'stack' },
      head,
      el(
        'div',
        { class: 'empty' },
        el('strong', {}, '未安装 Kubeyard operator'),
        el('span', { class: 'muted small' }, '集群中没有 VirtualCluster 资源（kubeyard.wutz.dev/v1alpha1），安装 kubeyard-operator 后虚拟集群会出现'),
      ),
    );
  }
  const clusters = list(view.clusters);
  if (!clusters.length) {
    return el(
      'div',
      { class: 'stack' },
      head,
      el(
        'div',
        { class: 'empty' },
        el('strong', {}, '没有虚拟集群'),
        el('span', { class: 'muted small' }, '创建 VirtualCluster 后会出现'),
      ),
    );
  }
  const open = clusters.find((c) => c.name === state.selectedCluster);
  return el(
    'div',
    { class: 'stack' },
    head,
    el('div', { class: 'pool-grid' }, clusters.map(clusterCard)),
    open ? clusterDetail(open) : null,
  );
}

function clusterCard(c) {
  const claims = list(c.claims);
  const nodes = list(c.allocations).reduce((n, a) => n + list(a.nodes).length, 0);
  return el(
    'section',
    {
      class: `card pool selectable ${state.selectedCluster === c.name ? 'selected' : ''}`,
      onclick: () => selectCluster(c.name),
      role: 'button',
      tabindex: 0,
      onkeydown: (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          selectCluster(c.name);
        }
      },
    },
    el(
      'div',
      {},
      el('h2', {}, c.displayName || c.name),
      el('div', { class: 'mono small muted' }, c.name),
    ),
    el('div', {}, el('div', { class: 'card-label' }, '持有节点'), el('div', { class: 'card-value' }, String(nodes))),
    el(
      'div',
      { class: 'card-sub' },
      phaseBadge(c.phase),
      ` 优先级 ${c.priority} · ${c.preemptible ? '可被抢占' : '不可抢占'}`,
    ),
    el(
      'div',
      { class: 'claims' },
      el('h3', { class: 'section-title' }, '声明'),
      claims.length
        ? claims.map((cl) =>
            el(
              'div',
              { class: 'claim' },
              el('span', { class: 'claim-name' }, el('span', { class: 'mono' }, cl.poolDisplayName || cl.pool)),
              el(
                'span',
                { class: 'mono small muted' },
                cl.autoscale ? `min ${cl.min} · max ${cl.max} · 自动伸缩` : `min ${cl.min} · max ${cl.max} · desired ${cl.desired}`,
              ),
            ),
          )
        : el('span', { class: 'muted small' }, '没有资源池声明'),
    ),
  );
}

function clusterDetail(c) {
  const exposure = c.exposure || {};
  const address =
    exposure.type === 'Ingress'
      ? `${exposure.host || '—'}:${exposure.port || '—'}`
      : exposure.loadBalancerIP || c.endpoint || '—';
  const facts = [
    ['Provider', `${c.provider || '—'}${c.mode ? ' · ' + c.mode : ''}`],
    ['版本', c.version || '—'],
    ['对外地址', `${exposure.type || '—'} · ${address}`],
  ];
  const conditions = list(c.conditions);
  // The operator sets Preempting while nodes are being reclaimed for another
  // cluster (docs/operator.md §3.4 step 2). The admin needs to see it first.
  const reclaiming = conditions.find((cond) => cond.type === 'Preempting' && cond.status === 'True');
  return el(
    'section',
    { class: 'card stack' },
    el(
      'div',
      { class: 'page-head' },
      el('div', {}, el('h2', {}, c.displayName || c.name), el('div', { class: 'mono small muted' }, c.name)),
      el('button', { class: 'btn', onclick: () => selectCluster(c.name) }, '收起'),
    ),
    reclaiming ? el('div', { class: 'alert warning', role: 'status' }, `即将回收：${reclaiming.message || '有节点将被回收'}`) : null,
    c.description ? el('p', { class: 'muted' }, c.description) : null,
    el(
      'div',
      { class: 'facts' },
      facts.map(([k, v]) => el('div', { class: 'fact' }, el('span', { class: 'card-label' }, k), el('span', { class: 'mono small' }, v))),
    ),
    allocationTable(list(c.allocations)),
    conditions.length
      ? el(
          'div',
          { class: 'claims' },
          el('h3', { class: 'section-title' }, '条件'),
          conditions.map((cond) =>
            el(
              'div',
              { class: 'claim' },
              el('span', { class: 'claim-name' }, el('span', { class: 'mono' }, cond.type), ' ', badge(cond.status, cond.status === 'True' ? 'ok' : '')),
              el('span', { class: 'muted small' }, `${cond.reason || ''} ${cond.message || ''}`.trim()),
            ),
          ),
        )
      : null,
    el('h3', { class: 'section-title' }, '管理员 kubeconfig'),
    c.adminKubeconfig
      ? el('span', { class: 'muted small' }, '导出的是该虚拟集群内的 cluster-admin 凭证，导出会写入审计 Event，请只交给该集群的管理员。')
      : el('span', { class: 'muted small' }, '集群尚未生成管理员 kubeconfig（status.adminKubeconfigSecretRef 为空）。'),
    c.adminKubeconfig
      ? el(
          'div',
          { class: 'actions' },
          confirmButton(`export:${c.name}`, '导出管理员 kubeconfig', `确认导出 ${c.name} 的管理员 kubeconfig`, {
            confirmCls: 'btn btn-danger',
            run: () => exportAdminKubeconfig(c.name),
          }),
          cancelButton(),
        )
      : null,
    el('h3', { class: 'section-title' }, '以管理员身份打开'),
    el(
      'span',
      { class: 'muted small' },
      '以该集群的 cluster-admin 身份读取它的节点与 Pod。凭证只用于这一次读取，不返回页面；每次打开都会写入审计 Event。',
    ),
    c.adminKubeconfig
      ? el(
          'div',
          { class: 'actions' },
          confirmButton(`open:${c.name}`, '以管理员身份打开', `确认以 ${c.name} 管理员身份打开`, {
            confirmCls: 'btn btn-danger',
            run: () => openAsAdmin(c.name),
          }),
          state.adminView && state.adminView.vc === c.name ? el('button', { class: 'btn', onclick: closeAdminView }, '关闭') : null,
        )
      : null,
    state.adminView && state.adminView.vc === c.name ? adminOverview(c.name, state.adminView.overview) : null,
    messages('cluster'),
  );
}

function allocationTable(allocs) {
  return el(
    'div',
    { class: 'table-wrap' },
    el(
      'table',
      {},
      el('thead', {}, el('tr', {}, el('th', {}, '资源池'), el('th', {}, '节点'), el('th', { class: 'num' }, '待分配需求'))),
      el(
        'tbody',
        {},
        allocs.length
          ? allocs.map((a) =>
              el(
                'tr',
                {},
                el('td', { class: 'mono' }, a.pool),
                el('td', { class: 'mono small' }, list(a.nodes).join(', ') || '—'),
                el('td', { class: 'num' }, String(a.pendingDemand || 0)),
              ),
            )
          : el('tr', {}, el('td', { class: 'muted', colspan: 3 }, '还没有分配节点')),
      ),
    ),
  );
}

// exportAdminKubeconfig has the app save the credential to a file the user picks
// in a save dialog, so the credential never reaches the page. The export is
// audited before the file is written, so a failed export shows the error here.
// A cancelled dialog exports nothing and shows nothing.
async function exportAdminKubeconfig(name) {
  state.busy = true;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  state.actionScope = 'cluster';
  renderContent();
  try {
    const path = await call('ExportAdminKubeconfig', state.selected, name, name);
    if (path) state.notice = `已导出到 ${path}，导出已写入审计 Event。`;
  } catch (err) {
    state.actionError = err.message;
  } finally {
    state.busy = false;
    renderContent();
  }
}

// openAsAdmin reads the nodes and pods of a virtual cluster through its admin
// credential. The app does the read and the audit, so the page gets the overview
// only.
async function openAsAdmin(name) {
  state.busy = true;
  state.confirm = '';
  state.notice = '';
  state.actionError = '';
  state.actionScope = 'cluster';
  state.adminView = null;
  renderContent();
  try {
    const overview = await call('OpenVirtualClusterAdmin', state.selected, name, name);
    state.adminView = { vc: name, overview };
  } catch (err) {
    state.actionError = err.message;
  } finally {
    state.busy = false;
    renderContent();
  }
}

function closeAdminView() {
  state.adminView = null;
  renderContent();
}

// adminOverview shows what the admin read says, with a warning that it was read
// as the cluster's admin.
function adminOverview(name, ov) {
  return el(
    'div',
    { class: 'stack' },
    el('div', { class: 'alert warning', role: 'status' }, `正在以 ${name} 的 cluster-admin 身份查看。关闭或切换后收起。`),
    el(
      'div',
      { class: 'cards' },
      metricCard('节点就绪', `${ov.nodesReady} / ${ov.nodesTotal}`, 'Ready 的节点数'),
      metricCard('Pod 运行中', ov.podsRunning, `Pending ${ov.podsPending} · 失败 ${ov.podsFailed}`),
    ),
    nodeTable(list(ov.nodes)),
  );
}

// --- resource pools ----------------------------------------------------------

// poolsPage is read-only: it shows what the NodePools and their claims say.
// The operator does the allocation, so the page has no write actions.
function poolsPage() {
  const view = state.pools;
  const head = pageHead(
    '资源池',
    view && view.installed ? `NodePool · ${state.selected}` : `集群 ${state.selected}`,
    state.poolsAt ? el('span', { class: 'live' }, `更新于 ${fmtTime(state.poolsAt)}`) : null,
  );
  if (state.poolsError) return el('div', { class: 'stack' }, head, alert(state.poolsError, 'error'));
  if (!view) return el('div', { class: 'stack' }, head, el('p', { class: 'muted' }, '加载中…'));
  if (!view.installed) {
    return el(
      'div',
      { class: 'stack' },
      head,
      el(
        'div',
        { class: 'empty' },
        el('strong', {}, '未安装 Kubeyard operator'),
        el('span', { class: 'muted small' }, '集群中没有 NodePool 资源（kubeyard.wutz.dev/v1alpha1），安装 kubeyard-operator 后资源池会出现'),
      ),
    );
  }
  const pools = list(view.pools);
  if (!pools.length) {
    return el(
      'div',
      { class: 'stack' },
      head,
      el(
        'div',
        { class: 'empty' },
        el('strong', {}, '没有资源池'),
        el('span', { class: 'muted small' }, '在 host 集群中创建 NodePool 后会出现'),
      ),
    );
  }
  return el(
    'div',
    { class: 'stack' },
    head,
    messages('pools'),
    el('div', { class: 'pool-grid' }, pools.map(poolCard)),
  );
}

// dragging is the node being dragged on the resource pool page: its name, its
// pool, and the virtual cluster it is pinned to, if any. dragover cannot read the
// drag data, so the page keeps it here. It is cleared when a drop or drag ends.
let dragging = null;

function poolCard(p) {
  const reservedPercent = p.total > 0 ? Math.min(100, (p.reserved / p.total) * 100) : 0;
  const claims = list(p.claims);
  const nodes = list(p.nodes);
  return el(
    'section',
    { class: 'card pool' },
    el('div', {}, el('h2', {}, p.displayName || p.name), el('div', { class: 'mono small muted' }, p.name)),
    el('div', {}, el('div', { class: 'card-label' }, '空闲 / 总数'), el('div', { class: 'card-value' }, `${p.free} / ${p.total}`)),
    meterBar(reservedPercent),
    el('div', { class: 'card-sub' }, `保底占用 ${p.reserved} · 不健康 ${p.unhealthy}`),
    el(
      'div',
      { class: 'claims' },
      el('h3', { class: 'section-title' }, '虚拟集群声明'),
      claims.length ? claims.map((c) => claimRow(c, p)) : el('span', { class: 'muted small' }, '没有虚拟集群声明此池'),
    ),
    el(
      'div',
      { class: 'nodes' },
      el('h3', { class: 'section-title' }, '节点'),
      el('p', { class: 'muted small' }, '拖动节点到虚拟集群的声明即固定；拖到下方区域解除固定'),
      el(
        'div',
        { class: 'node-list' },
        nodes.length ? nodes.map((n) => nodeChip(n, p)) : el('span', { class: 'muted small' }, '选择器没有匹配的节点'),
      ),
      dropTarget(
        el('div', { class: 'drop-zone' }, '解除固定：拖到此处'),
        (d) => d.pool === p.name && d.pinnedTo !== '',
        (d) => write('UnpinNode', 'pools', [d.pinnedTo, p.name, d.node]),
      ),
    ),
  );
}

function claimRow(c, p) {
  const pinned = list(p.nodes).filter((n) => n.pinnedTo === c.virtualCluster).length;
  const size = c.autoscale ? '自动伸缩' : `desired ${c.desired}`;
  const row = el(
    'div',
    { class: 'claim' },
    el(
      'span',
      { class: 'claim-name' },
      el('span', { class: 'mono' }, c.virtualCluster),
      c.displayName ? el('span', { class: 'muted small' }, c.displayName) : null,
    ),
    el('span', { class: 'mono small muted claim-meta' }, `min ${c.min} · max ${c.max} · ${size} · 固定 ${pinned}`),
    c.autoscale
      ? null
      : el(
          'button',
          {
            class: 'btn',
            disabled: state.busy,
            onclick: () => {
              state.desired = { vc: c.virtualCluster, pool: p.name, value: String(c.desired), preview: null, typed: '', error: '', loading: false };
              renderContent();
            },
          },
          '调整 desired',
        ),
  );
  return el(
    'div',
    { class: 'claim-block' },
    dropTarget(
      row,
      (d) => d.pool === p.name && d.pinnedTo !== c.virtualCluster,
      (d) => write('PinNode', 'pools', [c.virtualCluster, p.name, d.node]),
    ),
    desiredEditor(c, p),
  );
}

// desiredEditor is the open desired-size editor of a claim. It previews the
// change before anything is written. A change that would take or release bound
// nodes needs the claim's name typed first, so the admin confirms what goes.
// The write sends the nodes the preview showed, and the server refuses when the
// cluster would now take a different set (docs/gui.md §3).
function desiredEditor(c, p) {
  const d = state.desired;
  if (!d || d.vc !== c.virtualCluster || d.pool !== p.name) return null;
  const inRange = (v) => Number.isInteger(v) && v >= c.min && v <= c.max;
  // Typing does not re-render, so the field keeps its focus. It updates the
  // preview button and drops a plan that no longer matches the value.
  const slot = el('div', { class: 'desired-slot' }, d.preview ? desiredPlan(c, p, d) : null);
  const previewButton = el(
    'button',
    {
      class: 'btn',
      disabled: !inRange(Number(d.value)) || state.busy || d.loading,
      onclick: () => previewDesired(c, p, Number(d.value)),
    },
    d.loading ? '预览中…' : '预览',
  );
  return el(
    'div',
    { class: 'desired-editor' },
    el(
      'div',
      { class: 'desired-row' },
      el('span', { class: 'small muted' }, `min ${c.min} – max ${c.max}`),
      el('input', {
        class: 'input desired-input',
        type: 'number',
        min: c.min,
        max: c.max,
        step: 1,
        value: d.value,
        disabled: state.busy,
        oninput: (e) => {
          d.value = e.target.value;
          previewButton.disabled = !inRange(Number(d.value)) || state.busy || d.loading;
          if (d.preview) {
            d.preview = null;
            slot.replaceChildren();
          }
        },
      }),
      previewButton,
      el(
        'button',
        {
          class: 'btn',
          onclick: () => {
            state.desired = null;
            renderContent();
          },
        },
        '取消',
      ),
    ),
    d.error ? alert(d.error, 'error') : null,
    slot,
  );
}

// typedConfirm is the name field and apply button of a change that takes nodes.
// Typing updates the button in place, so the field keeps its focus.
function typedConfirm(c, d, apply) {
  const button = el('button', { class: 'btn btn-primary', onclick: apply }, '确认应用');
  const input = el('input', {
    class: 'input desired-input desired-name',
    placeholder: `输入 ${c.virtualCluster} 确认`,
    value: d.typed,
    disabled: state.busy,
    oninput: (e) => {
      d.typed = e.target.value;
      button.disabled = d.typed !== c.virtualCluster || state.busy;
    },
  });
  button.disabled = d.typed !== c.virtualCluster || state.busy;
  return el('div', { class: 'desired-row' }, input, button);
}

// previewDesired reads what setting the claim to n would do, into the open editor.
async function previewDesired(c, p, n) {
  const d = state.desired;
  d.loading = true;
  d.error = '';
  renderContent();
  try {
    const view = await call('PreviewDesired', state.selected, c.virtualCluster, p.name, n);
    d.preview = { desired: n, view };
    d.typed = '';
  } catch (err) {
    d.error = err.message;
  } finally {
    d.loading = false;
    renderContent();
  }
}

// desiredPlan lists what the previewed change does and the button that applies
// it. The nodes it takes or releases are sent with the write as the set the
// admin confirmed.
function desiredPlan(c, p, d) {
  const { desired: n, view: v } = d.preview;
  const affected = [...v.preempt.map((x) => x.node), ...v.release.map((x) => x.node)].sort();
  const lines = [
    ...v.preempt.map((x) => `抢占 ${x.node}：来自 ${x.from}，宽限 ${v.grace} 后驱逐，${v.force} 内强制回收（自确认起算）`),
    ...v.release.map((x) => `释放 ${x.node}：来自 ${x.virtualCluster}`),
  ];
  const apply = () =>
    write('SetDesired', 'pools', [c.virtualCluster, p.name, n, affected]).then((ok) => {
      if (!ok) return;
      state.desired = null;
      renderContent();
    });
  return el(
    'div',
    { class: 'desired-plan' },
    n === c.desired
      ? el('span', { class: 'small muted' }, `desired 已是 ${n}，无需改动`)
      : el('span', { class: 'small' }, `desired ${c.desired} → ${n}`),
    lines.length ? lines.map((line) => el('span', { class: 'small' }, line)) : null,
    v.granted.length ? el('span', { class: 'small muted' }, `将直接授予：${v.granted.join(', ')}`) : null,
    v.shortfall > 0 ? el('span', { class: 'small muted' }, `还差 ${v.shortfall} 个节点，即使抢占也不够，将保持等待`) : null,
    v.approximate ? el('span', { class: 'small muted' }, '自动伸缩声明的待调度需求未计入，结果为近似') : null,
    n === c.desired
      ? null
      : affected.length
        ? typedConfirm(c, d, apply)
        : el('div', { class: 'desired-row' }, el('button', { class: 'btn btn-primary', disabled: state.busy, onclick: apply }, '应用')),
  );
}

// nodeChip is one node of a pool. It can be dragged while it is ready and no
// write is running. Its label says whether it is free, bound, pinned or draining.
function nodeChip(n, p) {
  const kind = !n.ready ? 'unready' : n.pinnedTo ? 'pinned' : n.holder ? 'busy' : 'free';
  const label = !n.ready
    ? '未就绪'
    : n.pinnedTo
      ? `固定 · ${n.pinnedTo}`
      : n.holder
        ? `${n.draining ? '排空中 · ' : ''}${n.holder}`
        : '空闲';
  return el(
    'span',
    {
      class: `node-chip ${kind}`,
      draggable: n.ready && !state.busy ? 'true' : false,
      title: `${n.name} · ${label}`,
      ondragstart: (e) => {
        dragging = { node: n.name, pool: p.name, pinnedTo: n.pinnedTo };
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', n.name);
      },
      ondragend: endDrag,
    },
    el('span', { class: 'mono' }, n.name),
    el('span', { class: 'node-state' }, label),
  );
}

// dropTarget makes node a place a dragged node can be dropped. accepts says
// whether a drag may land here, and drop runs with the dragged node once it has.
function dropTarget(node, accepts, drop) {
  node.addEventListener('dragover', (e) => {
    if (!dragging || !accepts(dragging)) return;
    e.preventDefault();
    node.classList.add('drop-active');
  });
  node.addEventListener('dragleave', () => node.classList.remove('drop-active'));
  node.addEventListener('drop', (e) => {
    node.classList.remove('drop-active');
    const d = dragging;
    dragging = null;
    if (!d || !accepts(d)) return;
    e.preventDefault();
    drop(d);
  });
  return node;
}

function endDrag() {
  dragging = null;
  document.querySelectorAll('.drop-active').forEach((x) => x.classList.remove('drop-active'));
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
