// 前端 Web UI（P14）：单页 SSE 聊天界面。
//
// 零构建：一个内嵌 HTML 文件，浏览器直接可用。聊天走 /v1/chat/stream（SSE），
// 用 fetch + ReadableStream 解析（POST + JSON body，EventSource 不支持）。
// 功能：API Key 输入（localStorage 记忆）、多轮会话（session_id 续接）、
//
//	流式渲染、工具调用展示。
//
// 生产演化方向：独立前端工程（React/Vue）+ WebSocket；接入鉴权/OAuth；
// 会话历史管理界面；移动端适配。
package server

import "net/http"

const chatUI = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>zebra AI Agent</title>
<style>
  body{font-family:system-ui,sans-serif;max-width:760px;margin:0 auto;padding:16px;background:#0f172a;color:#e2e8f0}
  h1{font-size:1.4rem} .bar{display:flex;gap:8px;margin-bottom:12px}
  input,select,button{padding:8px 10px;border-radius:8px;border:1px solid #334155;background:#1e293b;color:#e2e8f0}
  button{cursor:pointer;background:#3b82f6} button:hover{background:#2563eb}
  #log{background:#0b1220;border:1px solid #334155;border-radius:10px;padding:12px;min-height:60vh;overflow-y:auto;white-space:pre-wrap}
  .user{color:#93c5fd}.assistant{color:#a7f3d0}.tool{color:#fbbf24;font-size:.85rem}.err{color:#f87171}
  .meta{font-size:.75rem;color:#64748b}
</style>
</head>
<body>
<h1>◆ zebra AI Agent</h1>
<div class="bar">
  <input id="key" placeholder="API Key (Bearer)" style="flex:1">
</div>
<div class="bar">
  <input id="msg" placeholder="输入你的问题…" style="flex:1" onkeydown="if(event.key==='Enter')send()">
  <select id="mode">
    <option value="">普通对话</option>
    <option value="plan">规划-执行</option>
    <option value="supervisor">多Agent路由</option>
  </select>
  <button onclick="send()">发送</button>
</div>
<div id="log"></div>

<script>
// 会话续接：同一浏览器保持 session_id（多轮记忆）
let sessionID = localStorage.getItem('zebra_sid') || '';
const log = document.getElementById('log');
const keyEl = document.getElementById('key');
const modeEl = document.getElementById('mode');
keyEl.value = localStorage.getItem('zebra_key') || 'admin-key';

function append(text, cls) {
  const el = document.createElement('div');
  el.className = cls; el.textContent = text;
  log.appendChild(el); log.scrollTop = log.scrollHeight;
}

async function send() {
  const key = keyEl.value.trim(); localStorage.setItem('zebra_key', key);
  const text = document.getElementById('msg').value.trim();
  if (!text) return;
  document.getElementById('msg').value = '';
  append('> ' + text, 'user');

  const body = { message: text, stream: true, confirm_risky: true, session_id: sessionID, mode: modeEl.value };
  const resp = await fetch('/v1/chat/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key },
    body: JSON.stringify(body)
  });
  if (!resp.ok) { append('HTTP ' + resp.status, 'err'); return; }

  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  let buf = '', answerEl = null;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    // 解析 SSE：按空行切事件
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
        if (type === 'session') { sessionID = ev; localStorage.setItem('zebra_sid', sessionID); append('# session: ' + sessionID, 'meta'); }
        else if (type === 'delta') { if (!answerEl) { answerEl = document.createElement('div'); answerEl.className = 'assistant'; log.appendChild(answerEl); } answerEl.textContent += ev.content; }
        else if (type === 'tool_call') { append('▲ 工具: ' + (ev.tool_name||''), 'tool'); }
        else if (type === 'done') append('', 'meta');
        else if (type === 'error') append('✗ ' + (ev.message||''), 'err');
      } catch(e) { /* 忽略半包 */ }
    }
  }
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
