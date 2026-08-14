// 前端 Web UI（P14）：单页 SSE 聊天工作台（企业风格）。
//
// 零构建：一个内嵌 HTML 文件，浏览器直接可用；聊天走 /v1/chat/stream（SSE），
// 用 fetch + ReadableStream 解析（POST + JSON body，EventSource 不支持）。
//
// 功能与组件：
//   - 亮/暗主题：跟随系统 + 手动切换（localStorage 记忆）
//   - 顶栏：品牌 + 生成状态（含工具调用计数）+ 会话 ID（复制/新建）
//   - 消息流：角色气泡（头像/时间/流式光标/打字动画）、Markdown 轻量渲染、
//     工具调用内联展示、错误横幅（可重试）
//   - 消息操作：复制 / 重试 / 赞踩反馈（POST /v1/feedback，负面自动回流评测集）
//   - 会话历史持久化（localStorage，刷新不丢）
//   - 输入区：Enter 发送（兼容中文输入法）/ Shift+Enter 换行 / 自动增高、
//     五种推理模式、发送↔停止、清空
//   - 设置：API Key（localStorage 记忆、可显隐）
//
// 生产演化方向：独立前端工程（React/Vue）+ WebSocket；会话历史侧栏；移动端适配。
package server

import "net/http"

const chatUI = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Zebra AI Agent · 企业控制台</title>
<style>
  :root{
    --bg:#0a1120; --panel:#0f1b2e; --panel-2:#12203a; --border:#1e2f4d;
    --text:#e2e8f0; --muted:#8494b0; --accent:#3b82f6; --accent-2:#2563eb;
    --user-bg:#1d4ed8; --user-text:#fff; --ok:#22c55e; --err:#f87171; --tool:#fbbf24;
    --code-bg:#0b1626; --hover:#172a4a; --shadow:0 4px 18px rgba(0,0,0,.25);
  }
  [data-theme="light"]{
    --bg:#f3f6fb; --panel:#ffffff; --panel-2:#f8fafd; --border:#d9e1ec;
    --text:#1a2332; --muted:#64748b; --accent:#2563eb; --accent-2:#1d4ed8;
    --user-bg:#2563eb; --user-text:#fff; --ok:#16a34a; --err:#dc2626; --tool:#b45309;
    --code-bg:#eef2f8; --hover:#eaf0f8; --shadow:0 4px 18px rgba(15,23,42,.08);
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;display:flex;flex-direction:column;background:var(--bg);color:var(--text);
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

  header{display:flex;align-items:center;gap:12px;padding:10px 18px;background:var(--panel);border-bottom:1px solid var(--border);flex-wrap:wrap;transition:background .2s}
  .brand{display:flex;align-items:center;gap:10px;font-weight:700;font-size:15px}
  .brand .mark{width:26px;height:26px;border-radius:7px;background:linear-gradient(135deg,var(--accent),#7c3aed);display:flex;align-items:center;justify-content:center;font-size:13px;color:#fff}
  .status{display:flex;align-items:center;gap:6px;margin-left:auto;font-size:12px;color:var(--muted)}
  .dot{width:8px;height:8px;border-radius:50%;background:var(--ok)}
  .dot.busy{background:var(--tool);animation:pulse 1s infinite}
  @keyframes pulse{50%{opacity:.35}}
  .session{margin-left:8px;font-size:12px;color:var(--muted);display:flex;align-items:center;gap:6px;flex-wrap:wrap}
  .session code{color:var(--tool);background:var(--code-bg);border:1px solid var(--border);padding:2px 6px;border-radius:5px}

  main{flex:1;overflow-y:auto;padding:20px}
  .chat{max-width:860px;margin:0 auto;display:flex;flex-direction:column;gap:14px}
  .empty-hint{text-align:center;color:var(--muted);margin-top:56px;font-size:13px;line-height:2}
  .empty-hint .big{font-size:17px;color:var(--text)}
  .msg{display:flex;gap:10px;max-width:84%}
  .msg.user{align-self:flex-end;flex-direction:row-reverse}
  .avatar{flex:none;width:30px;height:30px;border-radius:9px;display:flex;align-items:center;justify-content:center;font-size:13px;color:#fff}
  .msg.user .avatar{background:var(--user-bg)}
  .msg.assistant .avatar{background:linear-gradient(135deg,var(--accent),#7c3aed)}
  .msg > div{min-width:0}
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
  .tool-chip{display:inline-flex;align-items:center;gap:6px;background:rgba(251,191,36,.12);border:1px solid rgba(251,191,36,.35);color:var(--tool);
             font-size:12px;border-radius:7px;padding:3px 9px;margin:2px 4px 2px 0}
  .meta{font-size:12px;color:var(--muted);text-align:center;padding:2px 0}
  .err{background:rgba(248,113,113,.12);border:1px solid rgba(248,113,113,.35);color:var(--err);
       border-radius:10px;padding:9px 12px;font-size:13px;display:flex;gap:10px;align-items:center;justify-content:space-between}
  .err button{color:var(--err);border-color:rgba(248,113,113,.35);background:none;font-size:12px}
  .typing{display:inline-flex;gap:4px;padding:4px 2px}
  .typing i{width:6px;height:6px;border-radius:50%;background:var(--muted);animation:bounce 1.2s infinite}
  .typing i:nth-child(2){animation-delay:.2s}.typing i:nth-child(3){animation-delay:.4s}
  @keyframes bounce{0%,60%,100%{transform:translateY(0)}30%{transform:translateY(-4px)}}

  footer{border-top:1px solid var(--border);background:var(--panel);padding:12px 18px 14px;transition:background .2s}
  .composer{max-width:860px;margin:0 auto;display:flex;flex-direction:column;gap:8px}
  #msg{resize:none;min-height:44px;max-height:150px;line-height:1.5}
  .actions{display:flex;gap:8px;align-items:center;justify-content:space-between;flex-wrap:wrap}
  .actions .left,.actions .right{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
  .hint{font-size:11px;color:var(--muted)}
  .settings{margin-top:8px;display:none;gap:8px}
  .settings.open{display:flex}
  #key{flex:1;min-width:200px}

  @media (max-width:640px){
    .msg{max-width:94%}
    main{padding:12px}
    header{padding:10px 12px}
  }
</style>
</head>
<body>
<header>
  <div class="brand"><span class="mark">◆</span> Zebra AI Agent <span style="color:var(--muted);font-weight:400">企业控制台</span></div>
  <div class="status"><span class="dot" id="dot"></span><span id="statusText">就绪</span></div>
  <div class="session">会话 <code id="sid">-</code>
    <button onclick="copySid()" title="复制会话 ID">复制</button>
    <button onclick="newSession()" title="开始新会话">新会话</button>
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
        <button onclick="clearChat()" title="清空对话并删除本地历史">清空</button>
      </div>
      <span class="hint">SSE 流式 · session 自动续接 · 支持 Markdown</span>
    </div>
    <div class="settings" id="settings">
      <input id="key" type="password" placeholder="API Key (Bearer)">
      <button onclick="toggleKey()" title="显示/隐藏">显示</button>
    </div>
  </div>
</footer>

<script>
let sessionID = localStorage.getItem('zebra_sid') || '';
let controller = null;
let history = [];
let currentAnswer = null; // 当前正在流式生成的回答气泡（工具 chip 挂这里）
const chat = document.getElementById('chat');
const empty = document.getElementById('empty');
const keyEl = document.getElementById('key');
const modeEl = document.getElementById('mode');
const msgEl = document.getElementById('msg');
const sidEl = document.getElementById('sid');
keyEl.value = localStorage.getItem('zebra_key') || 'admin-key';
updateSid();
initTheme();
loadHistory();

function now() {
  return new Date().toLocaleTimeString('zh-CN', { hour12: false });
}
function setStatus(text, state) {
  document.getElementById('statusText').textContent = text;
  document.getElementById('dot').className = 'dot' + (state ? ' ' + state : '');
}
function updateSid() {
  sidEl.textContent = sessionID ? sessionID.slice(0, 8) + '…' : '-';
}
function copySid() {
  if (!sessionID) return;
  navigator.clipboard.writeText(sessionID).then(function() { setStatus('会话已复制', ''); });
}
function newSession() {
  sessionID = '';
  localStorage.removeItem('zebra_sid');
  updateSid();
  setStatus('新会话已开始', '');
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
function toggleTheme() {
  applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
}

// ---- 历史持久化 ----
function loadHistory() {
  try {
    history = JSON.parse(localStorage.getItem('zebra_history') || '[]');
  } catch(e) { history = []; }
  if (!history.length) return;
  empty.style.display = 'none';
  history.forEach(function(m) {
    var b = bubble(m.role, m.role === 'user' ? '你' : 'Zebra', m.role === 'user' ? '>' : '◆');
    b.innerHTML = renderText(m.text);
    if (m.q) b.parentNode.dataset.q = m.q;
  });
}
function saveHistory() {
  if (history.length > 60) history = history.slice(history.length - 60);
  try { localStorage.setItem('zebra_history', JSON.stringify(history)); } catch(e) {}
}
function clearChat() {
  chat.innerHTML = '';
  chat.appendChild(empty);
  empty.style.display = '';
  history = [];
  localStorage.removeItem('zebra_history');
}

// ---- 最小 Markdown 渲染（先转义再包装，防 XSS）----
function esc(s) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}
function renderText(raw) {
  var out = '';
  // Go 内嵌字符串不允许字面反引号，用 fromCharCode 构造代码块围栏
  var fence = String.fromCharCode(96).repeat(3);
  var parts = raw.split(fence);
  for (var i = 0; i < parts.length; i++) {
    if (i % 2 === 0) {
      var t = esc(parts[i]);
      var bt = String.fromCharCode(96); // 反引号（Go 内嵌串不允许字面）
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
  col.style.flex = '1';
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
function addTool(name) {
  var chip = document.createElement('span');
  chip.className = 'tool-chip'; chip.textContent = '▲ ' + name;
  if (currentAnswer && currentAnswer.isConnected) {
    currentAnswer.appendChild(chip);
  } else {
    var w = document.createElement('div');
    w.appendChild(chip);
    chat.insertBefore(w, empty);
  }
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
    b.onclick = function() { msgEl.value = retry; send(); };
    e.appendChild(b);
  }
  chat.insertBefore(e, empty);
  scrollBottom();
}
function scrollBottom() {
  var main = document.querySelector('main');
  var near = main.scrollHeight - main.scrollTop - main.clientHeight < 120;
  if (near) main.scrollTop = main.scrollHeight;
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
  var meta = wrap.querySelector('.meta-line');
  var actions = meta.querySelector('.bubble-actions');
  var copy = document.createElement('button');
  copy.textContent = '复制';
  copy.title = '复制内容';
  copy.onclick = function() { navigator.clipboard.writeText(b.innerText); setStatus('已复制', ''); };
  actions.appendChild(copy);
  if (wrap.classList.contains('assistant')) {
    var up = document.createElement('button');
    up.textContent = '赞';
    up.onclick = function() { feedback(wrap, b, 1, up, down); };
    var down = document.createElement('button');
    down.textContent = '踩';
    down.onclick = function() { feedback(wrap, b, -1, up, down); };
    actions.appendChild(up); actions.appendChild(down);
    var retry = document.createElement('button');
    retry.textContent = '重试';
    retry.onclick = function() {
      if (wrap.dataset.q) { msgEl.value = wrap.dataset.q; send(); }
    };
    actions.appendChild(retry);
  }
}
function feedback(wrap, b, rating, upBtn, downBtn) {
  if (!sessionID) { setStatus('无会话，无法反馈', ''); return; }
  var key = keyEl.value.trim();
  fetch('/v1/feedback', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key },
    body: JSON.stringify({ session_id: sessionID, rating: rating })
  }).then(function(resp) {
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    var on = rating === 1 ? upBtn : downBtn;
    var other = rating === 1 ? downBtn : upBtn;
    on.classList.add('on'); other.classList.remove('on');
    if (rating === -1) on.classList.add('bad');
    setStatus(rating === 1 ? '已赞' : '已踩（将回流评测集）', '');
  }).catch(function(e) { setStatus('反馈失败', ''); });
}

msgEl.addEventListener('keydown', function(e) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) {
    e.preventDefault();
    send();
  }
});
msgEl.addEventListener('input', function() {
  msgEl.style.height = 'auto';
  msgEl.style.height = Math.min(msgEl.scrollHeight, 150) + 'px';
});

async function send() {
  var key = keyEl.value.trim();
  localStorage.setItem('zebra_key', key);
  var text = msgEl.value.trim();
  if (!text || controller) return;
  msgEl.value = '';
  msgEl.style.height = 'auto';
  empty.style.display = 'none';
  var q = text;
  var userBubble = bubble('user', '你', '>');
  userBubble.textContent = text;
  wireActions(userBubble.parentNode, userBubble, '');
  history.push({ role: 'user', text: text, time: now() });
  saveHistory();
  setBusy(true);
  typingIndicator(true);
  var toolCount = 0;
  controller = new AbortController();

  var body = { message: text, stream: true, confirm_risky: true, session_id: sessionID, mode: modeEl.value };
  var answerEl = null;
  var raw = '';
  try {
    var resp = await fetch('/v1/chat/stream', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key },
      body: JSON.stringify(body),
      signal: controller.signal
    });
    if (!resp.ok) {
      var t = await resp.text().catch(function() { return ''; });
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
            sessionID = ev;
            localStorage.setItem('zebra_sid', sessionID);
            updateSid();
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
            addTool(ev.tool_name || '');
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
    wireActions(answerEl.parentNode, answerEl, q);
    history.push({ role: 'assistant', text: raw, time: now(), q: q });
    saveHistory();
  }
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
