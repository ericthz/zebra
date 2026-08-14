// 前端 Web UI（P14/P61）：单页 SSE 聊天工作台（企业风格）。
//
// 零构建：一个内嵌 HTML 文件，浏览器直接可用；聊天走 /v1/chat/stream（SSE），
// 用 fetch + ReadableStream 解析（POST + JSON body，EventSource 不支持）。
//
// 功能与组件：
//   - 多会话管理：左侧会话侧栏（新建/切换/删除，localStorage 持久化）
//   - 亮/暗主题：跟随系统 + 手动切换（localStorage 记忆）
//   - 流式阶段轨迹：plan/ReAct 的 思考/规划/执行步骤/观察 以阶段行展示
//   - 消息流：角色气泡（头像/时间/流式光标/打字动画）、Markdown 轻量渲染、
//     工具调用内联展示、错误横幅（可重试）
//   - 消息操作：复制 / 重试 / 赞踩反馈（POST /v1/feedback，负面自动回流评测集）
//   - 导出对话：TXT / JSON 下载
//   - 会话过期自愈：session 过期时自动新建并重试一次
//   - 输入区：Enter 发送（兼容中文输入法）/ Shift+Enter 换行 / 自动增高、
//     五种推理模式、发送↔停止、清空
//   - 移动端：侧栏抽屉 + 设置抽屉
//
// 生产演化方向：独立前端工程（React/Vue）+ WebSocket；会话服务端历史 API。
package server

import "net/http"

const chatUI = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Zebra AI Agent · AI Agent 工作台</title>
<style>
  :root{
    --bg:#0a1120; --panel:#0f1b2e; --panel-2:#12203a; --border:#1e2f4d;
    --text:#e2e8f0; --muted:#8494b0; --accent:#3b82f6; --accent-2:#2563eb;
    --user-bg:#1d4ed8; --user-text:#fff; --ok:#22c55e; --err:#f87171; --tool:#fbbf24;
    --code-bg:#0b1626; --hover:#172a4a; --shadow:0 4px 18px rgba(0,0,0,.25);
    --scroll-thumb:rgba(148,163,184,.35); --scroll-thumb-hover:rgba(148,163,184,.55);
    --sidebar-w:264px;
  }
  [data-theme="light"]{
    --bg:#f3f6fb; --panel:#ffffff; --panel-2:#f8fafd; --border:#d9e1ec;
    --text:#1a2332; --muted:#64748b; --accent:#2563eb; --accent-2:#1d4ed8;
    --user-bg:#2563eb; --user-text:#fff; --ok:#16a34a; --err:#dc2626; --tool:#b45309;
    --code-bg:#eef2f8; --hover:#eaf0f8; --shadow:0 4px 18px rgba(15,23,42,.08);
    --scroll-thumb:rgba(15,23,42,.22); --scroll-thumb-hover:rgba(15,23,42,.38);
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;display:flex;background:var(--bg);color:var(--text);
       font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;font-size:14px;
       transition:background .2s,color .2s}
  button,input,select,textarea{font:inherit;color:var(--text)}
  button{cursor:pointer;border:1px solid var(--border);border-radius:8px;background:var(--panel-2);padding:7px 12px;transition:background .15s,border-color .15s,color .15s}
  button:hover{border-color:var(--accent)}
  button:disabled{opacity:.5;cursor:not-allowed}
  .btn-primary{background:var(--accent);border-color:var(--accent);color:#fff}
  .btn-primary:hover{background:var(--accent-2)}
  input,select,textarea{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:8px 10px;outline:none;transition:background .2s,border-color .2s}
  input:focus,select:focus,textarea:focus{border-color:var(--accent)}
  code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}

  /* 侧栏 */
  aside{width:var(--sidebar-w);flex:none;background:var(--panel);border-right:1px solid var(--border);
        display:flex;flex-direction:column;height:100vh;transition:transform .25s,background .2s}
  .side-head{display:flex;align-items:center;justify-content:space-between;padding:12px 14px;border-bottom:1px solid var(--border)}
  .side-head b{font-size:13px}
  #convList{flex:1;overflow-y:auto;padding:8px;scrollbar-width:thin;scrollbar-color:var(--scroll-thumb) transparent}
  #convList::-webkit-scrollbar{width:8px}
  #convList::-webkit-scrollbar-track{background:transparent}
  #convList::-webkit-scrollbar-thumb{background:var(--scroll-thumb);border-radius:4px}
  #convList::-webkit-scrollbar-thumb:hover{background:var(--scroll-thumb-hover)}
  .conv{padding:9px 11px;border-radius:9px;cursor:pointer;margin-bottom:3px;border:1px solid transparent}
  .conv:hover{background:var(--hover)}
  .conv.active{background:var(--hover);border-color:var(--accent)}
  .conv .t{font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .conv .s{font-size:11px;color:var(--muted);margin-top:2px}
  .conv .del{float:right;visibility:hidden;border:none;background:none;color:var(--muted);padding:0 4px;font-size:12px}
  .conv:hover .del{visibility:visible}
  .conv .del:hover{color:var(--err)}
  .backdrop{display:none}

  .wrap{flex:1;display:flex;flex-direction:column;min-width:0;height:100vh}
  header{display:flex;align-items:center;gap:10px;padding:10px 16px;background:var(--panel);border-bottom:1px solid var(--border);flex-wrap:wrap;transition:background .2s}
  .hamb{display:none;font-size:18px}
  .brand{display:flex;align-items:center;gap:9px;font-weight:700;font-size:15px}
  .brand .mark{width:26px;height:26px;border-radius:7px;background:linear-gradient(135deg,var(--accent),#7c3aed);display:flex;align-items:center;justify-content:center;font-size:13px;color:#fff}
  .status{display:flex;align-items:center;gap:6px;margin-left:auto;font-size:12px;color:var(--muted)}
  .dot{width:8px;height:8px;border-radius:50%;background:var(--ok)}
  .dot.busy{background:var(--tool);animation:pulse 1s infinite}
  @keyframes pulse{50%{opacity:.35}}
  .headbtns{display:flex;align-items:center;gap:6px;font-size:12px}
  .headbtns button{font-size:12px;padding:5px 9px}

  main{flex:1;overflow-y:scroll;padding:20px;scrollbar-width:thin;scrollbar-gutter:stable;scrollbar-color:var(--scroll-thumb) transparent}
  main::-webkit-scrollbar{width:12px}
  main::-webkit-scrollbar-track{background:transparent}
  main::-webkit-scrollbar-thumb{background:var(--scroll-thumb);border-radius:6px;border:3px solid transparent;background-clip:content-box}
  main::-webkit-scrollbar-thumb:hover{background:var(--scroll-thumb-hover);border:3px solid transparent;background-clip:content-box}
  .chat{max-width:960px;margin:0 auto;display:flex;flex-direction:column;gap:12px}
  .empty-hint{text-align:center;color:var(--muted);margin-top:52px;font-size:13px;line-height:2}
  .empty-hint .big{font-size:17px;color:var(--text)}
  /* 头像不占宽度：绝对定位在消息行左右边缘（◇ 左 / > 右），纵向对齐、突出显示；
     气泡因此整行铺满，与规划/工具提示框左右边线完全对齐 */
  .msg{position:relative;width:100%;max-width:100%}
  .avatar{position:absolute;top:5px;z-index:1;width:34px;height:34px;border-radius:10px;display:flex;align-items:center;justify-content:center;font-size:13px;color:#fff;
          border:1px solid rgba(255,255,255,.22);box-shadow:0 0 0 2px var(--bg),0 4px 10px rgba(0,0,0,.18)}
  /* 头像在气泡外侧（探入聊天列两侧页边距，像常规对话组件：◇ 在左、> 在右） */
  .msg.assistant .avatar{left:-46px;background:linear-gradient(135deg,var(--accent),#7c3aed);box-shadow:0 0 0 2px var(--bg),0 4px 12px rgba(124,58,237,.4)}
  .msg.user .avatar{right:-46px;background:var(--user-bg);box-shadow:0 0 0 2px var(--bg),0 4px 12px rgba(37,99,235,.4)}
  .msg .col{width:100%;min-width:0}
  .msg.user .col{width:fit-content;margin-left:auto}
  .bubble{background:var(--panel-2);border:1px solid var(--border);border-radius:12px;padding:10px 13px;word-break:break-word;line-height:1.65;box-shadow:var(--shadow);transition:background .2s,border-color .2s}
  .msg.user .bubble{background:var(--user-bg);border-color:var(--user-bg);color:var(--user-text);border-top-right-radius:4px}
  .msg.assistant .bubble{border-top-left-radius:4px}
  .bubble .inline{background:var(--code-bg);border:1px solid var(--border);border-radius:4px;padding:0 4px;font-size:.92em}
  .bubble pre.code{background:var(--code-bg);border:1px solid var(--border);border-radius:8px;padding:10px;overflow-x:auto;font-size:12.5px;line-height:1.5;margin:6px 0}
  .bubble pre.code code{background:none;border:none;padding:0}
  .meta-line{margin-top:6px;font-size:11px;color:var(--muted);display:flex;gap:8px;align-items:center;opacity:.9}
  .msg.user .meta-line{justify-content:flex-end}
  .bubble-actions{display:inline-flex;gap:2px;margin-left:auto;visibility:hidden}
  .msg:hover .bubble-actions,.bubble-actions.show{visibility:visible}
  .bubble-actions button{font-size:11px;padding:2px 7px;background:none;border-color:transparent;color:var(--muted)}
  .bubble-actions button:hover{color:var(--accent);border-color:var(--border)}
  .bubble-actions button.on{color:var(--accent);border-color:var(--accent)}
  .bubble-actions button.on.bad{color:var(--err);border-color:var(--err)}
  .cursor::after{content:"▍";color:var(--tool);animation:blink 1s step-start infinite}
  @keyframes blink{50%{opacity:0}}
  /* 活动轨迹：一次回答内的 阶段/技能/工具调用 统一展示 */
  .activity{display:flex;flex-direction:column;gap:3px;background:var(--panel);border:1px solid var(--border);
            border-radius:10px;padding:8px 12px;margin:2px 0;font-size:12px}
  .phase{display:flex;gap:8px;align-items:baseline;color:var(--muted);border-left:3px solid var(--accent);padding-left:8px;line-height:1.7}
  .phase.skill{color:var(--accent)}
  /* 工具行与阶段行共用蓝色左竖线，缩进一致（3px 竖线 + 8px 内边距） */
  .tools-row{color:var(--tool);border-left:3px solid var(--accent);padding-left:8px;line-height:1.7;overflow-wrap:anywhere}
  .meta{font-size:12px;color:var(--muted);text-align:center;padding:2px 0}
  .err{background:rgba(248,113,113,.12);border:1px solid rgba(248,113,113,.35);color:var(--err);
       border-radius:10px;padding:9px 12px;font-size:13px;display:flex;gap:10px;align-items:center;justify-content:space-between}
  .err button{color:var(--err);border-color:rgba(248,113,113,.35);background:none;font-size:12px}
  .typing{display:inline-flex;gap:4px;padding:4px 2px}
  .typing i{width:6px;height:6px;border-radius:50%;background:var(--muted);animation:bounce 1.2s infinite}
  .typing i:nth-child(2){animation-delay:.2s}.typing i:nth-child(3){animation-delay:.4s}
  @keyframes bounce{0%,60%,100%{transform:translateY(0)}30%{transform:translateY(-4px)}}

  footer{border-top:1px solid var(--border);background:var(--panel);padding:12px 16px 14px;transition:background .2s}
  .composer{max-width:960px;margin:0 auto;display:flex;flex-direction:column;gap:8px}
  #msg{resize:none;min-height:44px;max-height:150px;line-height:1.5}
  .actions{display:flex;gap:8px;align-items:center;justify-content:space-between;flex-wrap:wrap}
  .actions .left,.actions .right{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
  .hint{font-size:11px;color:var(--muted)}
  .settings{display:none}
  .settings.open{display:flex;gap:8px;margin-top:8px}
  #key{flex:1;min-width:200px}

  /* 窄屏：聊天列两侧页边距放不下外侧头像时，退回气泡边缘（加内边距避免遮字） */
  @media (max-width:1360px){
    .msg.assistant .avatar{left:0}
    .msg.user .avatar{right:0}
    .msg.assistant .bubble{padding-left:48px}
    .msg.user .bubble{padding-right:48px}
  }

  @media (max-width:768px){
    aside{position:fixed;left:0;top:0;z-index:50;transform:translateX(-100%)}
    aside.open{transform:none}
    .backdrop{position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:40}
    .backdrop.show{display:block}
    .hamb{display:inline-flex}
    main{padding:12px}
    .settings.open{flex-wrap:wrap}
  }
</style>
</head>
<body>
<aside id="sidebar">
  <div class="side-head"><b>会话历史</b>
    <button onclick="newConversation()" title="新建会话">＋ 新建</button>
  </div>
  <div id="convList"></div>
</aside>
<div class="backdrop" id="backdrop" onclick="closeDrawers()"></div>

<div class="wrap">
  <header>
    <button class="hamb" onclick="toggleSidebar()" title="会话列表">☰</button>
    <div class="brand"><span class="mark">◆</span> Zebra AI Agent <span style="color:var(--muted);font-weight:400">AI Agent 工作台</span></div>
    <div class="status"><span class="dot" id="dot"></span><span id="statusText">就绪</span></div>
    <div class="headbtns">
      <button onclick="exportConv('txt')" title="导出为文本">TXT</button>
      <button onclick="exportConv('json')" title="导出为 JSON">JSON</button>
      <button id="themeBtn" onclick="toggleTheme()" title="切换亮/暗主题">☾ 暗色</button>
    </div>
  </header>

  <main>
    <div class="chat" id="chat">
      <div class="empty-hint" id="empty">
        <div class="big">◆ Zebra AI Agent</div>
        企业级 Agent 对话工作台<br>
        支持工具调用 · RAG 知识库 · 记忆画像 · 五种推理模式<br>
        输入问题开始对话，Enter 发送 / Shift+Enter 换行
      </div>
    </div>
  </main>

  <footer>
    <div class="composer">
      <textarea id="msg" rows="1" placeholder="输入你的问题…（Enter 发送 / Shift+Enter 换行）"></textarea>
      <div class="actions">
        <div class="left">
          <select id="mode" title="推理模式">
            <option value="">普通对话</option>
            <option value="plan">规划-执行</option>
            <option value="supervisor">多 Agent 路由</option>
            <option value="reflect">反思</option>
            <option value="react">ReAct</option>
            <option value="debate">辩论</option>
          </select>
          <button id="btnSend" class="btn-primary" onclick="send()">发送</button>
          <button id="btnStop" style="display:none" onclick="stop()">停止</button>
          <button onclick="toggleSettings()" title="API Key 设置">设置</button>
          <button onclick="clearChat()" title="清空当前对话">清空</button>
        </div>
        <span class="hint">SSE 流式 · Markdown · 会话自动保存</span>
      </div>
      <div class="settings" id="settings">
        <input id="key" type="password" placeholder="API Key (Bearer)">
        <button onclick="toggleKey()" title="显示/隐藏">显示</button>
      </div>
    </div>
  </footer>
</div>

<script>
// ---- 多会话状态（localStorage 持久化）----
let conversations = [];
let activeId = null;
let controller = null;
let currentAnswer = null;
let toolCount = 0;
let activityEl = null; // 当前回答的活动轨迹块
let tools = {};        // 工具名 → 调用次数（实时去重计数）
const LS_CONVS = 'zebra_conversations';
const chat = document.getElementById('chat');
const empty = document.getElementById('empty');
const keyEl = document.getElementById('key');
const modeEl = document.getElementById('mode');
const msgEl = document.getElementById('msg');
keyEl.value = localStorage.getItem('zebra_key') || 'admin-key';
initTheme();
loadConversations();

function now() { return new Date().toLocaleTimeString('zh-CN', { hour12: false }); }
function nowFull() { return new Date().toLocaleString('zh-CN', { hour12: false }); }
function setStatus(text, state) {
  document.getElementById('statusText').textContent = text;
  document.getElementById('dot').className = 'dot' + (state ? ' ' + state : '');
}
function active() {
  for (var i = 0; i < conversations.length; i++) if (conversations[i].id === activeId) return conversations[i];
  return null;
}
function saveConvs() {
  try { localStorage.setItem(LS_CONVS, JSON.stringify(conversations)); } catch(e) {}
}
function convTitleOf(text) {
  var t = text.replace(/\s+/g, ' ').trim();
  return t.length > 14 ? t.slice(0, 14) + '…' : (t || '新对话');
}

function loadConversations() {
  try { conversations = JSON.parse(localStorage.getItem(LS_CONVS) || '[]'); } catch(e) { conversations = []; }
  if (!conversations.length) {
    conversations.push({ id: 'c' + Date.now(), sid: '', title: '新对话', createdAt: nowFull(), messages: [] });
    saveConvs();
  }
  activeId = conversations[conversations.length - 1].id;
  renderSidebar();
  renderChat();
}
function renderSidebar() {
  var list = document.getElementById('convList');
  list.innerHTML = '';
  conversations.forEach(function(c) {
    var el = document.createElement('div');
    el.className = 'conv' + (c.id === activeId ? ' active' : '');
    var del = document.createElement('button');
    del.className = 'del'; del.textContent = '✕';
    del.onclick = function(e) { e.stopPropagation(); deleteConversation(c.id); };
    var t = document.createElement('div'); t.className = 't'; t.textContent = c.title;
    var s = document.createElement('div'); s.className = 's';
    s.textContent = (c.messages.length || 0) + ' 条 · ' + (c.sid ? c.sid.slice(0, 6) + '…' : '未连接');
    el.appendChild(del); el.appendChild(t); el.appendChild(s);
    el.onclick = function() { switchConversation(c.id); };
    list.appendChild(el);
  });
}
function newConversation() {
  var c = { id: 'c' + Date.now(), sid: '', title: '新对话', createdAt: nowFull(), messages: [] };
  conversations.push(c);
  saveConvs();
  activeId = c.id;
  closeDrawers();
  renderSidebar();
  renderChat();
  setStatus('新会话已创建', '');
}
function switchConversation(id) {
  if (controller) stop();
  activeId = id;
  closeDrawers();
  renderSidebar();
  renderChat();
}
function deleteConversation(id) {
  if (!confirm('删除该会话？此操作不可恢复。')) return;
  conversations = conversations.filter(function(c) { return c.id !== id; });
  if (!conversations.length) {
    conversations.push({ id: 'c' + Date.now(), sid: '', title: '新对话', createdAt: nowFull(), messages: [] });
  }
  if (activeId === id) activeId = conversations[conversations.length - 1].id;
  saveConvs();
  renderSidebar();
  renderChat();
}
function renderChat() {
  chat.innerHTML = '';
  chat.appendChild(empty);
  var c = active();
  if (c && c.messages.length) {
    empty.style.display = 'none';
    c.messages.forEach(function(m) {
      var b = bubble(m.role, m.role === 'user' ? '你' : 'Zebra', m.role === 'user' ? '>' : '◆');
      b.innerHTML = renderText(m.text);
      // bubble() 返回气泡 div：b → col → .msg 外层（wrap），按钮与活动块都挂在 wrap 上
      var wrap = b.parentNode.parentNode;
      wireActions(wrap, b, m.q || '');
      // 活动轨迹随消息持久化：刷新/服务重启后按原顺序重放（阶段/技能/工具）
      if (m.role === 'assistant' && m.activity && m.activity.length) {
        var act = renderActivityBlock(m.activity);
        if (act) chat.insertBefore(act, wrap);
      }
    });
  }
  scrollBottom(true); // 打开/切换会话后直接看最新内容
}
// renderActivityBlock 从持久化事件重放活动轨迹块（与直播渲染同一结构）。
function renderActivityBlock(events) {
  var wrap = document.createElement('div');
  wrap.className = 'activity';
  var tools = {}, toolsRow = null;
  events.forEach(function(ev) {
    if (!ev) return;
    if (ev.t === 'phase' && ev.text) {
      var r = document.createElement('div');
      r.className = 'phase'; r.textContent = '◇ ' + ev.text;
      wrap.appendChild(r);
    } else if (ev.t === 'skill' && ev.name) {
      var s = document.createElement('div');
      s.className = 'phase skill'; s.textContent = '■ 技能：' + ev.name;
      wrap.appendChild(s);
    } else if (ev.t === 'tool' && ev.name) {
      tools[ev.name] = (tools[ev.name] || 0) + 1;
      if (!toolsRow) { toolsRow = document.createElement('div'); toolsRow.className = 'tools-row'; wrap.appendChild(toolsRow); }
      var names = Object.keys(tools), parts = [];
      for (var i = 0; i < names.length; i++) parts.push(names[i] + '×' + tools[names[i]]);
      toolsRow.textContent = '▲ 工具：' + parts.join(' · ');
    }
  });
  if (!wrap.childNodes.length) return null;
  return wrap;
}
function clearChat() {
  var c = active();
  if (c) { c.messages = []; saveConvs(); }
  renderChat();
}
function closeDrawers() {
  document.getElementById('sidebar').classList.remove('open');
  document.getElementById('backdrop').classList.remove('show');
}
function toggleSidebar() {
  var sb = document.getElementById('sidebar');
  sb.classList.toggle('open');
  document.getElementById('backdrop').classList.toggle('show', sb.classList.contains('open'));
}

// ---- 亮/暗主题 ----
function initTheme() {
  var saved = localStorage.getItem('zebra_theme');
  var theme = saved || (window.matchMedia && matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
  applyTheme(theme);
}
function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  localStorage.setItem('zebra_theme', t);
  document.getElementById('themeBtn').textContent = t === 'dark' ? '☼ 亮色' : '☾ 暗色';
}
function toggleTheme() { applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark'); }

// ---- 最小 Markdown 渲染（先转义再包装，防 XSS）----
function esc(s) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}
function renderText(raw) {
  var out = '';
  // Go 内嵌字符串不允许字面反引号，用 fromCharCode 构造代码块围栏
  var fence = String.fromCharCode(96).repeat(3);
  var bt = String.fromCharCode(96);
  var parts = raw.split(fence);
  for (var i = 0; i < parts.length; i++) {
    if (i % 2 === 0) {
      var t = esc(parts[i]);
      t = t.replace(new RegExp(bt + '([^' + bt + '\\n]+)' + bt, 'g'), '<code class="inline">$1</code>');
      t = t.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
      out += t.replace(/\n/g, '<br>');
    } else {
      out += '<pre class="code"><code>' + esc(parts[i].replace(/^[a-zA-Z0-9]+\n/, '')) + '</code></pre>';
    }
  }
  return out;
}

function bubble(role, name, avatar) {
  var wrap = document.createElement('div');
  wrap.className = 'msg ' + role;
  var av = document.createElement('div');
  av.className = 'avatar'; av.textContent = avatar;
  var col = document.createElement('div');
  col.className = 'col';
  var b = document.createElement('div');
  b.className = 'bubble';
  var meta = document.createElement('div');
  meta.className = 'meta-line';
  meta.innerHTML = '<span>' + name + '</span><span>' + now() + '</span>';
  var actions = document.createElement('span');
  actions.className = 'bubble-actions';
  meta.appendChild(actions);
  col.appendChild(b); col.appendChild(meta);
  wrap.appendChild(av); wrap.appendChild(col);
  chat.insertBefore(wrap, empty);
  return b;
}
function ensureActivity() {
  if (!activityEl || !activityEl.isConnected) {
    activityEl = document.createElement('div');
    activityEl.className = 'activity';
    chat.insertBefore(activityEl, empty);
  }
  return activityEl;
}
function addPhase(text) {
  var row = document.createElement('div');
  row.className = 'phase'; row.textContent = '◇ ' + text;
  ensureActivity().appendChild(row);
  scrollBottom();
}
function addSkill(name) {
  var row = document.createElement('div');
  row.className = 'phase skill'; row.textContent = '■ 技能：' + name;
  ensureActivity().appendChild(row);
  scrollBottom();
}
function addTool(name) {
  tools[name] = (tools[name] || 0) + 1;
  var wrap = ensureActivity();
  var row = wrap.querySelector('.tools-row');
  if (!row) { row = document.createElement('div'); row.className = 'tools-row'; wrap.appendChild(row); }
  var names = Object.keys(tools);
  var parts = [];
  for (var i = 0; i < names.length; i++) parts.push(names[i] + '×' + tools[names[i]]);
  row.textContent = '▲ 工具：' + parts.join(' · ');
  scrollBottom();
}
function addMeta(text) {
  var m = document.createElement('div');
  m.className = 'meta'; m.textContent = text;
  chat.insertBefore(m, empty);
}
function addError(text, retry) {
  var e = document.createElement('div');
  e.className = 'err';
  var s = document.createElement('span');
  s.textContent = '✗ ' + text;
  e.appendChild(s);
  if (retry) {
    var b = document.createElement('button');
    b.textContent = '重试';
    b.onclick = function() { msgEl.value = retry; send(false); };
    e.appendChild(b);
  }
  chat.insertBefore(e, empty);
  scrollBottom();
}
function scrollBottom(force) {
  var main = document.querySelector('main');
  var near = main.scrollHeight - main.scrollTop - main.clientHeight < 120;
  if (force || near) main.scrollTop = main.scrollHeight;
}
function typingIndicator(on) {
  var t = document.getElementById('typing');
  if (on && !t) {
    var d = document.createElement('div');
    d.className = 'typing'; d.id = 'typing';
    d.innerHTML = '<i></i><i></i><i></i>';
    chat.insertBefore(d, empty);
  } else if (!on && t) {
    t.remove();
  }
}
function setBusy(busy) {
  document.getElementById('btnSend').style.display = busy ? 'none' : '';
  document.getElementById('btnStop').style.display = busy ? '' : 'none';
  setStatus(busy ? '生成中…' : '就绪', busy ? 'busy' : '');
}

// ---- 消息操作（复制 / 重试 / 赞踩反馈）----
function wireActions(wrap, b, q) {
  if (!wrap) return;
  if (q) wrap.dataset.q = q;
  var actions = wrap.querySelector('.bubble-actions');
  var copy = document.createElement('button');
  copy.textContent = '复制';
  copy.onclick = function() { navigator.clipboard.writeText(b.innerText); setStatus('已复制', ''); };
  actions.appendChild(copy);
  if (wrap.classList.contains('assistant')) {
    var up = document.createElement('button');
    up.textContent = '赞';
    var down = document.createElement('button');
    down.textContent = '踩';
    up.onclick = function() { feedback(1, up, down); };
    down.onclick = function() { feedback(-1, up, down); };
    actions.appendChild(up); actions.appendChild(down);
    var retry = document.createElement('button');
    retry.textContent = '重试';
    retry.onclick = function() {
      if (wrap.dataset.q) { msgEl.value = wrap.dataset.q; send(false); }
    };
    actions.appendChild(retry);
  }
}
function feedback(rating, upBtn, downBtn) {
  var c = active();
  if (!c || !c.sid) { setStatus('无会话，无法反馈', ''); return; }
  fetch('/v1/feedback', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + keyEl.value.trim() },
    body: JSON.stringify({ session_id: c.sid, rating: rating })
  }).then(function(resp) {
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    var on = rating === 1 ? upBtn : downBtn;
    var other = rating === 1 ? downBtn : upBtn;
    on.classList.add('on'); other.classList.remove('on');
    if (rating === -1) on.classList.add('bad');
    setStatus(rating === 1 ? '已赞' : '已踩（将回流评测集）', '');
  }).catch(function() { setStatus('反馈失败', ''); });
}

// ---- 导出对话 ----
function exportConv(kind) {
  var c = active();
  if (!c || !c.messages.length) { setStatus('当前会话为空', ''); return; }
  var name = (c.title || '对话').replace(/\s+/g, '_');
  var ts = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
  var content, mime;
  if (kind === 'json') {
    content = JSON.stringify({ title: c.title, createdAt: c.createdAt, messages: c.messages }, null, 2);
    mime = 'application/json';
    name += '-' + ts + '.json';
  } else {
    var lines = ['Zebra AI Agent 对话导出', '会话：' + (c.title || '未命名'), '时间：' + c.createdAt, '----'];
    c.messages.forEach(function(m) {
      lines.push('【' + (m.role === 'user' ? '你' : 'Zebra') + ' ' + m.time + '】');
      lines.push(m.text);
      lines.push('----');
    });
    content = lines.join('\n');
    mime = 'text/plain';
    name += '-' + ts + '.txt';
  }
  var blob = new Blob([content], { type: mime });
  var a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = name;
  document.body.appendChild(a); a.click(); a.remove();
  setStatus('已导出 ' + name, '');
}

function toggleSettings() { document.getElementById('settings').classList.toggle('open'); }
function toggleKey() {
  if (keyEl.type === 'password') { keyEl.type = 'text'; event.target.textContent = '隐藏'; }
  else { keyEl.type = 'password'; event.target.textContent = '显示'; }
}

msgEl.addEventListener('keydown', function(e) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) {
    e.preventDefault();
    send(false);
  }
});
msgEl.addEventListener('input', function() {
  msgEl.style.height = 'auto';
  msgEl.style.height = Math.min(msgEl.scrollHeight, 150) + 'px';
});

async function send(retried) {
  if (controller) return;
  var text = msgEl.value.trim();
  if (!text) return;
  msgEl.value = '';
  msgEl.style.height = 'auto';
  empty.style.display = 'none';
  var c = active();
  if (!c) return;
  if (!c.title || c.title === '新对话') { c.title = convTitleOf(text); renderSidebar(); }
  var q = text;
  var userBubble = bubble('user', '你', '>');
  userBubble.textContent = text;
  wireActions(userBubble.parentNode.parentNode, userBubble, '');
  scrollBottom(true); // 发送后立即跳到最新位置，随后流式按"接近底部则跟随"
  setBusy(true);
  typingIndicator(true);
  toolCount = 0;
  activityEl = null;
  tools = {};
  var activity = []; // 本次活动轨迹（阶段/技能/工具，随消息持久化）
  controller = new AbortController();
  var answerEl = null;
  var raw = '';

  var body = { message: text, stream: true, confirm_risky: true, session_id: c.sid, mode: modeEl.value };
  try {
    var resp = await fetch('/v1/chat/stream', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + keyEl.value.trim() },
      body: JSON.stringify(body),
      signal: controller.signal
    });
    if (!resp.ok) {
      var t = await resp.text().catch(function() { return ''; });
      // 会话过期自愈：自动新建会话并重试一次
      if (!retried && (resp.status === 401 || resp.status === 404) && t.indexOf('会话') >= 0) {
        c.sid = '';
        addMeta('会话已过期，已自动新建并重试…');
        controller = null;
        setBusy(false); typingIndicator(false);
        msgEl.value = text;
        send(true);
        return;
      }
      addError('HTTP ' + resp.status + (t ? '：' + t.slice(0, 200) : ''), q);
      setBusy(false); typingIndicator(false); controller = null;
      return;
    }
    var reader = resp.body.getReader();
    var dec = new TextDecoder();
    var buf = '';
    while (true) {
      var r = await reader.read();
      if (r.done) break;
      buf += dec.decode(r.value, { stream: true });
      var blocks = buf.split('\n\n');
      buf = blocks.pop();
      for (var bi = 0; bi < blocks.length; bi++) {
        var block = blocks[bi];
        var type = 'message', data = '';
        var lines = block.split('\n');
        for (var li = 0; li < lines.length; li++) {
          if (lines[li].indexOf('event:') === 0) type = lines[li].slice(6).trim();
          if (lines[li].indexOf('data:') === 0) data += lines[li].slice(5).trim();
        }
        if (!data) continue;
        try {
          var ev = JSON.parse(data);
          if (type === 'session') {
            c.sid = ev;
            saveConvs(); renderSidebar();
          } else if (type === 'phase') {
            typingIndicator(false);
            activity.push({ t: 'phase', text: ev.phase || '' });
            addPhase(ev.phase || '');
          } else if (type === 'delta') {
            typingIndicator(false);
            if (!answerEl) {
              answerEl = bubble('assistant', 'Zebra', '◆');
              currentAnswer = answerEl;
              answerEl.classList.add('cursor');
            }
            raw += ev.content;
            answerEl.innerHTML = renderText(raw);
            scrollBottom();
          } else if (type === 'tool_call') {
            toolCount++;
            setStatus('生成中… · 工具 ×' + toolCount, 'busy');
            activity.push({ t: 'tool', name: ev.tool_name || '' });
            addTool(ev.tool_name || '');
          } else if (type === 'skill') {
            activity.push({ t: 'skill', name: ev.skill_name || ev.tool_name || '' });
            addSkill(ev.skill_name || ev.tool_name || '');
          } else if (type === 'done') {
            if (answerEl) answerEl.classList.remove('cursor');
          } else if (type === 'error') {
            addError(ev.message || '未知错误', q);
          }
        } catch(e) { /* 半包忽略 */ }
      }
    }
  } catch (e) {
    if (e.name === 'AbortError') addMeta('已停止');
    else addError('网络错误：' + e.message, q);
  }
  if (answerEl) {
    answerEl.classList.remove('cursor');
    wireActions(answerEl.parentNode.parentNode, answerEl, q);
  }
  if (raw) {
    c.messages.push({ role: 'user', text: q, time: now() });
    c.messages.push({ role: 'assistant', text: raw, time: now(), q: q,
                      activity: activity.length ? activity : undefined });
  }
  saveConvs(); renderSidebar();
  currentAnswer = null;
  typingIndicator(false);
  setBusy(false);
  controller = null;
}

function stop() {
  if (controller) controller.abort();
}
</script>
</body>
</html>`

// uiHandler 返回聊天界面（公开访问，鉴权在 API 层）。
func uiHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(chatUI))
	}
}
