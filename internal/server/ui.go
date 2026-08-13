// 前端 Web UI（P14）：单页 SSE 聊天工作台（企业风格）。
//
// 零构建：一个内嵌 HTML 文件，浏览器直接可用；聊天走 /v1/chat/stream（SSE），
// 用 fetch + ReadableStream 解析（POST + JSON body，EventSource 不支持）。
//
// 交互组件：
//   - 顶栏：品牌 + 连接/生成状态 + 会话 ID（可复制）+ 新建会话
//   - 消息流：角色气泡（头像/时间/流式光标）、工具调用内联展示、错误横幅
//   - 输入区：Enter 发送（兼容中文输入法）/ Shift+Enter 换行 / 自动增高、
//     五种推理模式、发送↔停止切换、清空对话
//   - 设置：API Key（localStorage 记忆、可显隐）
//
// 生产演化方向：独立前端工程（React/Vue）+ WebSocket；会话历史管理；移动端适配。
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
    --user-bg:#1d4ed8; --ok:#22c55e; --err:#f87171; --tool:#fbbf24;
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;display:flex;flex-direction:column;background:var(--bg);color:var(--text);
       font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;font-size:14px}
  button,input,select,textarea{font:inherit;color:var(--text)}
  button{cursor:pointer;border:1px solid var(--border);border-radius:8px;background:var(--panel-2);padding:7px 12px;transition:background .15s,border-color .15s}
  button:hover{border-color:var(--accent)}
  button:disabled{opacity:.5;cursor:not-allowed}
  .btn-primary{background:var(--accent);border-color:var(--accent)}
  .btn-primary:hover{background:var(--accent-2)}
  input,select,textarea{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:8px 10px;outline:none}
  input:focus,select:focus,textarea:focus{border-color:var(--accent)}

  header{display:flex;align-items:center;gap:12px;padding:10px 18px;background:var(--panel);border-bottom:1px solid var(--border);flex-wrap:wrap}
  .brand{display:flex;align-items:center;gap:10px;font-weight:700;font-size:15px}
  .brand .mark{width:26px;height:26px;border-radius:7px;background:linear-gradient(135deg,var(--accent),#7c3aed);display:flex;align-items:center;justify-content:center;font-size:13px}
  .status{display:flex;align-items:center;gap:6px;margin-left:auto;font-size:12px;color:var(--muted)}
  .dot{width:8px;height:8px;border-radius:50%;background:var(--ok)}
  .dot.busy{background:var(--tool);animation:pulse 1s infinite}
  @keyframes pulse{50%{opacity:.35}}
  .session{margin-left:8px;font-size:12px;color:var(--muted);display:flex;align-items:center;gap:6px;flex-wrap:wrap}
  .session code{color:var(--tool);background:var(--panel-2);border:1px solid var(--border);padding:2px 6px;border-radius:5px}

  main{flex:1;overflow-y:auto;padding:20px}
  .chat{max-width:860px;margin:0 auto;display:flex;flex-direction:column;gap:14px}
  .empty-hint{text-align:center;color:var(--muted);margin-top:56px;font-size:13px;line-height:2}
  .empty-hint .big{font-size:17px;color:#b9c6dd}
  .msg{display:flex;gap:10px;max-width:82%}
  .msg.user{align-self:flex-end;flex-direction:row-reverse}
  .avatar{flex:none;width:30px;height:30px;border-radius:9px;display:flex;align-items:center;justify-content:center;font-size:13px}
  .msg.user .avatar{background:var(--user-bg)}
  .msg.assistant .avatar{background:linear-gradient(135deg,var(--accent),#7c3aed)}
  .msg > div{min-width:0}
  .bubble{background:var(--panel-2);border:1px solid var(--border);border-radius:12px;padding:10px 13px;white-space:pre-wrap;word-break:break-word;line-height:1.65}
  .msg.user .bubble{background:var(--user-bg);border-color:var(--user-bg);border-top-right-radius:4px}
  .msg.assistant .bubble{border-top-left-radius:4px}
  .meta-line{margin-top:5px;font-size:11px;color:var(--muted);display:flex;gap:8px}
  .msg.user .meta-line{justify-content:flex-end}
  .cursor::after{content:"▍";color:var(--tool);animation:blink 1s step-start infinite}
  @keyframes blink{50%{opacity:0}}
  .tool-chip{display:inline-flex;align-items:center;gap:6px;background:#3b2d0a;border:1px solid #6b4f0a;color:var(--tool);
             font-size:12px;border-radius:7px;padding:3px 9px;margin:2px 4px 2px 0}
  .meta{font-size:12px;color:var(--muted);text-align:center;padding:2px 0}
  .err{background:rgba(248,113,113,.12);border:1px solid rgba(248,113,113,.35);color:var(--err);
       border-radius:10px;padding:9px 12px;font-size:13px}
  .typing{display:inline-flex;gap:4px;padding:4px 2px}
  .typing i{width:6px;height:6px;border-radius:50%;background:var(--muted);animation:bounce 1.2s infinite}
  .typing i:nth-child(2){animation-delay:.2s}.typing i:nth-child(3){animation-delay:.4s}
  @keyframes bounce{0%,60%,100%{transform:translateY(0)}30%{transform:translateY(-4px)}}

  footer{border-top:1px solid var(--border);background:var(--panel);padding:12px 18px 14px}
  .composer{max-width:860px;margin:0 auto;display:flex;flex-direction:column;gap:8px}
  #msg{resize:none;min-height:44px;max-height:150px;line-height:1.5}
  .actions{display:flex;gap:8px;align-items:center;justify-content:space-between;flex-wrap:wrap}
  .actions .left,.actions .right{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
  .hint{font-size:11px;color:var(--muted)}
  .settings{margin-top:8px;display:none;gap:8px}
  .settings.open{display:flex}
  #key{flex:1;min-width:200px}
</style>
</head>
<body>
<header>
  <div class="brand"><span class="mark">◆</span> Zebra AI Agent <span style="color:var(--muted);font-weight:400">企业控制台</span></div>
  <div class="status"><span class="dot" id="dot"></span><span id="statusText">就绪</span></div>
  <div class="session">会话 <code id="sid">-</code>
    <button onclick="copySid()" title="复制会话 ID">复制</button>
    <button onclick="newSession()" title="开始新会话">新会话</button>
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
        <button onclick="clearChat()" title="清空当前对话显示">清空</button>
      </div>
      <span class="hint">SSE 流式 · session 自动续接</span>
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
const chat = document.getElementById('chat');
const empty = document.getElementById('empty');
const keyEl = document.getElementById('key');
const modeEl = document.getElementById('mode');
const msgEl = document.getElementById('msg');
const sidEl = document.getElementById('sid');
keyEl.value = localStorage.getItem('zebra_key') || 'admin-key';
updateSid();

function now() {
  return new Date().toLocaleTimeString('zh-CN', { hour12: false });
}
function setStatus(text, state) {
  document.getElementById('statusText').textContent = text;
  const dot = document.getElementById('dot');
  dot.className = 'dot' + (state ? ' ' + state : '');
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
function clearChat() {
  chat.innerHTML = '';
  chat.appendChild(empty);
}
function toggleSettings() {
  document.getElementById('settings').classList.toggle('open');
}
function toggleKey() {
  if (keyEl.type === 'password') { keyEl.type = 'text'; event.target.textContent = '隐藏'; }
  else { keyEl.type = 'password'; event.target.textContent = '显示'; }
}

function bubble(role, name, avatar) {
  const wrap = document.createElement('div');
  wrap.className = 'msg ' + role;
  const av = document.createElement('div');
  av.className = 'avatar'; av.textContent = avatar;
  const col = document.createElement('div');
  const b = document.createElement('div');
  b.className = 'bubble';
  const meta = document.createElement('div');
  meta.className = 'meta-line';
  meta.innerHTML = '<span>' + name + '</span><span>' + now() + '</span>';
  col.appendChild(b); col.appendChild(meta);
  wrap.appendChild(av); wrap.appendChild(col);
  chat.insertBefore(wrap, empty);
  return b;
}
function addTool(name) {
  const chip = document.createElement('span');
  chip.className = 'tool-chip'; chip.textContent = '▲ ' + name;
  const lastBubble = chat.querySelector('.msg.assistant:last-of-type .bubble');
  if (lastBubble) lastBubble.appendChild(chip);
  else {
    const w = document.createElement('div');
    w.appendChild(chip);
    chat.insertBefore(w, empty);
  }
  scrollBottom();
}
function addMeta(text) {
  const m = document.createElement('div');
  m.className = 'meta'; m.textContent = text;
  chat.insertBefore(m, empty);
}
function addError(text) {
  const e = document.createElement('div');
  e.className = 'err'; e.textContent = '✗ ' + text;
  chat.insertBefore(e, empty);
  scrollBottom();
}
function scrollBottom() {
  const main = document.querySelector('main');
  const near = main.scrollHeight - main.scrollTop - main.clientHeight < 120;
  if (near) main.scrollTop = main.scrollHeight;
}
function typingIndicator(on) {
  const t = document.getElementById('typing');
  if (on && !t) {
    const d = document.createElement('div');
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

msgEl.addEventListener('keydown', function(e) {
  // 中文输入法组合期间不拦截 Enter（避免选词即发送）
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
  const key = keyEl.value.trim();
  localStorage.setItem('zebra_key', key);
  const text = msgEl.value.trim();
  if (!text || controller) return;
  msgEl.value = '';
  msgEl.style.height = 'auto';
  empty.style.display = 'none';
  bubble('user', '你', '>').textContent = text;
  setBusy(true);
  typingIndicator(true);
  controller = new AbortController();

  const body = { message: text, stream: true, confirm_risky: true, session_id: sessionID, mode: modeEl.value };
  let answerEl = null;
  try {
    const resp = await fetch('/v1/chat/stream', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key },
      body: JSON.stringify(body),
      signal: controller.signal
    });
    if (!resp.ok) {
      const t = await resp.text().catch(function() { return ''; });
      addError('HTTP ' + resp.status + (t ? '：' + t.slice(0, 200) : ''));
      setBusy(false); typingIndicator(false); controller = null;
      return;
    }
    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    while (true) {
      const r = await reader.read();
      if (r.done) break;
      buf += dec.decode(r.value, { stream: true });
      const blocks = buf.split('\n\n');
      buf = blocks.pop();
      for (const block of blocks) {
        let type = 'message', data = '';
        for (const line of block.split('\n')) {
          if (line.startsWith('event:')) type = line.slice(6).trim();
          if (line.startsWith('data:')) data += line.slice(5).trim();
        }
        if (!data) continue;
        try {
          const ev = JSON.parse(data);
          if (type === 'session') {
            sessionID = ev;
            localStorage.setItem('zebra_sid', sessionID);
            updateSid();
          } else if (type === 'delta') {
            typingIndicator(false);
            if (!answerEl) {
              answerEl = bubble('assistant', 'Zebra', '◆');
              answerEl.classList.add('cursor');
            }
            answerEl.textContent += ev.content;
            scrollBottom();
          } else if (type === 'tool_call') {
            addTool(ev.tool_name || '');
          } else if (type === 'done') {
            if (answerEl) answerEl.classList.remove('cursor');
          } else if (type === 'error') {
            addError(ev.message || '未知错误');
          }
        } catch(e) { /* 半包忽略 */ }
      }
    }
  } catch (e) {
    if (e.name === 'AbortError') addMeta('已停止');
    else addError('网络错误：' + e.message);
  }
  if (answerEl) answerEl.classList.remove('cursor');
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
