// 前端 Web UI：单页 SSE 聊天工作台。
//
// 零构建：一个内嵌 HTML 文件，浏览器直接可用；聊天走 /v1/chat/stream（SSE），
// 用 fetch + ReadableStream 解析（POST + JSON body，EventSource 不支持）。
//
// 功能与组件：
//   - 多会话管理：左侧会话侧栏（新会话/切换/删除，localStorage 持久化）
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
//   - 图标：Lucide 风格内联 SVG（ISC 开源协议，currentColor 随亮/暗主题自动变色）
//
// 生产演化方向：独立前端工程（React/Vue）+ WebSocket；会话服务端历史 API。
package server

import "net/http"

const chatUI = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Zebra AI Agent · 工作台</title>
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<style>
  :root{
    --bg:#151517; --panel:#232324; --panel-2:#2c2c2e; --border:rgba(255,255,255,.12);
    --text:#f9fafb; --muted:#9ca3a8; --accent:#5686fe; --accent-2:#4176e6;
    --user-bg:#4176e6; --user-text:#fff; --ok:#22c55e; --err:#f26666; --tool:#f5a623;
    --code-bg:#1f1f21; --hover:rgba(255,255,255,.08); --shadow:0 1px 3px rgba(0,0,0,.25);
    --scroll-thumb:rgba(255,255,255,.22); --scroll-thumb-hover:rgba(255,255,255,.38);
    --sidebar-w:264px;
  }
  [data-theme="light"]{
    --bg:#ffffff; --panel:#ffffff; --panel-2:#f5f6f7; --border:rgba(0,0,0,.1);
    --text:#0f1115; --muted:#81858c; --accent:#4176e6; --accent-2:#5686fe;
    --user-bg:#4176e6; --user-text:#fff; --ok:#16a34a; --err:#e04848; --tool:#b45309;
    --code-bg:#f5f6f7; --hover:rgba(38,49,72,.06); --shadow:0 1px 3px rgba(0,0,0,.08);
    --scroll-thumb:rgba(0,0,0,.16); --scroll-thumb-hover:rgba(0,0,0,.28);
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;display:flex;background:var(--bg);color:var(--text);
       font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;font-size:14px;
       transition:background .2s,color .2s}
  button,input,select,textarea{font:inherit;color:var(--text)}
  button{cursor:pointer;border:1px solid var(--border);border-radius:8px;background:var(--panel-2);padding:7px 12px;font-size:12.5px;transition:background .15s,border-color .15s,color .15s}
  button:hover{border-color:var(--accent)}
  button:disabled{opacity:.5;cursor:not-allowed}
  .btn-primary{background:var(--accent);border-color:var(--accent);color:#fff}
  .btn-primary:hover{background:var(--accent-2)}
  input,select,textarea{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:8px 10px;outline:none;transition:background .2s,border-color .2s}
  input:focus,select:focus,textarea:focus{border-color:var(--accent)}
  code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
  .ic{flex:none;display:inline-block;vertical-align:-2px}
  .icon-btn{display:inline-flex;align-items:center;gap:4px;justify-content:center}

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
  .hamb{display:none;align-items:center;justify-content:center;padding:5px;font-size:0}
  .brand{display:flex;align-items:center;gap:9px;font-weight:700;font-size:15px}
  .brand .mark{width:26px;height:26px;border-radius:7px;display:flex;align-items:center;justify-content:center}
  .status{display:flex;align-items:center;gap:6px;margin-left:auto;font-size:12px;color:var(--muted)}
  .dot{width:8px;height:8px;border-radius:50%;background:var(--ok)}
  .dot.busy{background:var(--tool);animation:pulse 1s infinite}
  @keyframes pulse{50%{opacity:.35}}
  .headbtns{display:flex;align-items:center;gap:6px;font-size:12px}
  .headbtns button{font-size:11.5px;padding:5px 9px}

  main{flex:1;overflow-y:scroll;padding:20px;scrollbar-width:thin;scrollbar-gutter:stable;scrollbar-color:var(--scroll-thumb) transparent}
  main::-webkit-scrollbar{width:12px}
  main::-webkit-scrollbar-track{background:transparent}
  main::-webkit-scrollbar-thumb{background:var(--scroll-thumb);border-radius:6px;border:3px solid transparent;background-clip:content-box}
  main::-webkit-scrollbar-thumb:hover{background:var(--scroll-thumb-hover);border:3px solid transparent;background-clip:content-box}
  .chat{max-width:960px;margin:0 auto;display:flex;flex-direction:column;gap:12px}
  .empty-hint{text-align:center;color:var(--muted);margin-top:52px;font-size:13px;line-height:2}
  .empty-hint .big{font-size:17px;color:var(--text);display:flex;align-items:center;justify-content:center;gap:7px}
  /* 头像不占宽度：绝对定位在消息行左右边缘（bot 左 / user 右），纵向对齐、突出显示；
     气泡因此整行铺满，与规划/工具提示框左右边线完全对齐 */
  .msg{position:relative;width:100%;max-width:100%}
  .avatar{position:absolute;top:5px;z-index:1;width:34px;height:34px;border-radius:10px;display:flex;align-items:center;justify-content:center;font-size:13px;color:#fff;
          border:1px solid rgba(255,255,255,.22);box-shadow:0 0 0 2px var(--bg),0 4px 10px rgba(0,0,0,.18)}
  /* 头像在气泡外侧（探入聊天列两侧页边距，像常规对话组件：bot 在左、user 在右） */
  .msg.assistant .avatar{left:-46px;background:transparent;box-shadow:none;border:none}
  .msg.user .avatar{right:-46px;background:var(--user-bg);box-shadow:0 0 0 2px var(--bg),0 4px 12px rgba(65,118,230,.35)}
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
  .bubble-actions button{font-size:10.5px;padding:2px 7px;background:none;border-color:transparent;color:var(--muted)}
  .bubble-actions button:hover{color:var(--accent);border-color:var(--border)}
  .bubble-actions button.on{color:var(--accent);border-color:var(--accent)}
  .bubble-actions button.on.bad{color:var(--err);border-color:var(--err)}
  .cursor::after{content:"▍";color:var(--tool);animation:blink 1s step-start infinite}
  @keyframes blink{50%{opacity:0}}
  /* 活动轨迹：一次回答内的 阶段/技能/工具调用 统一展示 */
  .activity{display:flex;flex-direction:column;gap:3px;background:var(--panel);border:1px solid var(--border);
            border-radius:10px;padding:8px 12px;margin:2px 0;font-size:12px}
  .phase{display:flex;gap:7px;align-items:center;color:var(--muted);border-left:3px solid var(--accent);padding-left:8px;line-height:1.7}
  .phase.skill{color:var(--accent)}
  /* 工具行与阶段行共用蓝色左竖线，缩进一致（3px 竖线 + 8px 内边距） */
  .tools-row{display:flex;gap:7px;align-items:center;color:var(--tool);border-left:3px solid var(--accent);padding-left:8px;line-height:1.7;overflow-wrap:anywhere}
  .meta{font-size:12px;color:var(--muted);text-align:center;padding:2px 0}
  .err{background:rgba(248,113,113,.12);border:1px solid rgba(248,113,113,.35);color:var(--err);
       border-radius:10px;padding:9px 12px;font-size:13px;display:flex;gap:10px;align-items:center;justify-content:space-between}
  .err-text{display:inline-flex;align-items:center;gap:6px;min-width:0}
  .err button{color:var(--err);border-color:rgba(248,113,113,.35);background:none;font-size:11.5px}
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
  .confirm-toggle{font-size:11px;color:var(--muted);display:inline-flex;align-items:center;gap:4px;cursor:pointer;user-select:none}
  .settings{display:none}
  .settings.open{display:flex;gap:8px;align-items:center;margin-top:8px}
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
  <div class="side-head"><b class="icon-btn" data-ic-prepend="history" data-ic-size="14">会话历史</b>
    <button class="icon-btn" onclick="newConversation()" title="新会话" data-ic-prepend="plus" data-ic-size="14">新会话</button>
  </div>
  <div id="convList"></div>
</aside>
<div class="backdrop" id="backdrop" onclick="closeDrawers()"></div>

<div class="wrap">
  <header>
    <button class="hamb" onclick="toggleSidebar()" title="会话列表" data-ic="menu" data-ic-size="18"></button>
    <div class="brand"><span class="mark" data-ic="logo" data-ic-size="15"></span> Zebra AI Agent <span style="color:var(--muted);font-weight:400">工作台</span></div>
    <div class="status"><span class="dot" id="dot"></span><span id="statusText">就绪</span></div>
    <div class="headbtns">
      <button class="icon-btn" onclick="exportConv('txt')" title="导出为文本" data-ic-prepend="file-text" data-ic-size="14">TXT</button>
      <button class="icon-btn" onclick="exportConv('json')" title="导出为 JSON" data-ic-prepend="braces" data-ic-size="14">JSON</button>
      <button id="themeBtn" class="icon-btn" onclick="toggleTheme()" title="切换亮/暗主题"></button>
    </div>
  </header>

  <main>
    <div class="chat" id="chat">
      <div class="empty-hint" id="empty">
        <div class="big" data-ic-prepend="logo" data-ic-size="22">Zebra AI Agent</div>
        Agent 对话工作台<br>
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
          <button id="btnSend" class="btn-primary icon-btn" onclick="send()" data-ic-prepend="send" data-ic-size="14">发送</button>
          <button id="btnStop" class="icon-btn" style="display:none" onclick="stop()" data-ic-prepend="square" data-ic-size="13">停止</button>
          <button class="icon-btn" onclick="toggleSettings()" title="API Key 设置" data-ic-prepend="settings" data-ic-size="14">设置</button>
          <button class="icon-btn" onclick="clearChat()" title="清空当前对话" data-ic-prepend="trash-2" data-ic-size="14">清空</button>
          <label class="confirm-toggle" title="高危工具（写文件/执行命令/出文档）需用户显式授权">
            <input type="checkbox" id="confirmRisky"> 允许高危操作
          </label>
        </div>
        <span class="hint">SSE 流式 · Markdown · 会话自动保存</span>
      </div>
      <div class="settings" id="settings">
        <input id="key" type="password" placeholder="API Key (Bearer)">
        <button class="icon-btn" onclick="toggleKey(this)" title="显示/隐藏" data-ic-prepend="key-round" data-ic-size="14">显示</button>
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
// 读取本地保存的 API Key；无则留空（强制用户输入，不再内置默认密钥）。
keyEl.value = localStorage.getItem('zebra_key') || '';

// ---- 主题图标（material-symbols 圆角系为主，配套 mynaui/guidance/solar；
// 每个图标自带 viewBox(v) 与完整子元素(b)，ic() 按 v 适配坐标系）----
var ICONS = {
  "alert-circle": { v:24, b:"<path fill=\"currentColor\" d=\"M12 17q.425 0 .713-.288T13 16t-.288-.712T12 15t-.712.288T11 16t.288.713T12 17m-1-4h2V7h-2zm1 9q-2.075 0-3.9-.788t-3.175-2.137T2.788 15.9T2 12t.788-3.9t2.137-3.175T8.1 2.788T12 2t3.9.788t3.175 2.137T21.213 8.1T22 12t-.788 3.9t-2.137 3.175t-3.175 2.138T12 22\"/>" },
  "bot": { v:24, b:"<path fill=\"currentColor\" d=\"M4 15q-1.25 0-2.125-.875T1 12t.875-2.125T4 9V7q0-.825.588-1.412T6 5h3q0-1.25.875-2.125T12 2t2.125.875T15 5h3q.825 0 1.413.588T20 7v2q1.25 0 2.125.875T23 12t-.875 2.125T20 15v4q0 .825-.587 1.413T18 21H6q-.825 0-1.412-.587T4 19zm5-2q.625 0 1.063-.437T10.5 11.5t-.437-1.062T9 10t-1.062.438T7.5 11.5t.438 1.063T9 13m6 0q.625 0 1.063-.437T16.5 11.5t-.437-1.062T15 10t-1.062.438T13.5 11.5t.438 1.063T15 13m-7 4h8v-2H8z\"/>" },
  "braces": { v:24, b:"<path fill=\"currentColor\" d=\"M14 20v-2h3q.425 0 .713-.288T18 17v-2q0-.95.55-1.725t1.45-1.1v-.35q-.9-.325-1.45-1.1T18 9V7q0-.425-.288-.712T17 6h-3V4h3q1.25 0 2.125.875T20 7v2q0 .425.288.713T21 10h1v4h-1q-.425 0-.712.288T20 15v2q0 1.25-.875 2.125T17 20zm-7 0q-1.25 0-2.125-.875T4 17v-2q0-.425-.288-.712T3 14H2v-4h1q.425 0 .713-.288T4 9V7q0-1.25.875-2.125T7 4h3v2H7q-.425 0-.712.288T6 7v2q0 .95-.55 1.725T4 11.825v.35q.9.325 1.45 1.1T6 15v2q0 .425.288.713T7 18h3v2z\"/>" },
  "brain": { v:24, b:"<path fill=\"currentColor\" d=\"M11 15h2l.15-1.25q.2-.075.363-.175t.287-.225l1.15.5l1-1.7l-1-.75q.05-.2.05-.4t-.05-.4l1-.75l-1-1.7l-1.15.5q-.125-.125-.288-.225t-.362-.175L13 7h-2l-.15 1.25q-.2.075-.363.175t-.287.225l-1.15-.5l-1 1.7l1 .75Q9 10.8 9 11t.05.4l-1 .75l1 1.7l1.15-.5q.125.125.288.225t.362.175zm1-2.5q-.625 0-1.062-.437T10.5 11t.438-1.062T12 9.5t1.063.438T13.5 11t-.437 1.063T12 12.5M6 22v-4.3q-1.425-1.3-2.212-3.037T3 11q0-3.75 2.625-6.375T12 2q3.125 0 5.538 1.838t3.137 4.787l1.3 5.125q.125.475-.175.863T21 15h-2v3q0 .825-.587 1.413T17 20h-2v2z\"/>" },
  "copy": { v:24, b:"<path fill=\"none\" stroke=\"currentColor\" stroke-linecap=\"round\" stroke-linejoin=\"round\" stroke-width=\"1.5\" d=\"M20.829 12.861c.171-.413.171-.938.171-1.986s0-1.573-.171-1.986a2.25 2.25 0 0 0-1.218-1.218c-.413-.171-.938-.171-1.986-.171H11.1c-1.26 0-1.89 0-2.371.245a2.25 2.25 0 0 0-.984.984C7.5 9.209 7.5 9.839 7.5 11.1v6.525c0 1.048 0 1.573.171 1.986c.229.551.667.99 1.218 1.218c.413.171.938.171 1.986.171s1.573 0 1.986-.171m7.968-7.968a2.25 2.25 0 0 1-1.218 1.218c-.413.171-.938.171-1.986.171s-1.573 0-1.986.171a2.25 2.25 0 0 0-1.218 1.218c-.171.413-.171.938-.171 1.986s0 1.573-.171 1.986a2.25 2.25 0 0 1-1.218 1.218m7.968-7.968a11.68 11.68 0 0 1-7.75 7.9l-.218.068M16.5 7.5v-.9c0-1.26 0-1.89-.245-2.371a2.25 2.25 0 0 0-.983-.984C14.79 3 14.16 3 12.9 3H6.6c-1.26 0-1.89 0-2.371.245a2.25 2.25 0 0 0-.984.984C3 4.709 3 5.339 3 6.6v6.3c0 1.26 0 1.89.245 2.371c.216.424.56.768.984.984c.48.245 1.111.245 2.372.245H7.5\"/>" },
  "file-text": { v:24, b:"<path fill=\"currentColor\" d=\"M8 18h8v-2H8zm0-4h8v-2H8zm-2 8q-.825 0-1.412-.587T4 20V4q0-.825.588-1.412T6 2h8l6 6v12q0 .825-.587 1.413T18 22zm7-13h5l-5-5z\"/>" },
  "history": { v:24, b:"<path fill=\"currentColor\" d=\"M12 21q-3.15 0-5.575-1.912T3.275 14.2q-.1-.375.15-.687t.675-.363q.4-.05.725.15t.45.6q.6 2.25 2.475 3.675T12 19q2.925 0 4.963-2.037T19 12t-2.037-4.962T12 5q-1.725 0-3.225.8T6.25 8H8q.425 0 .713.288T9 9t-.288.713T8 10H4q-.425 0-.712-.288T3 9V5q0-.425.288-.712T4 4t.713.288T5 5v1.35q1.275-1.6 3.113-2.475T12 3q1.875 0 3.513.713t2.85 1.924t1.925 2.85T21 12t-.712 3.513t-1.925 2.85t-2.85 1.925T12 21m1-9.4l2.5 2.5q.275.275.275.7t-.275.7t-.7.275t-.7-.275l-2.8-2.8q-.15-.15-.225-.337T11 11.975V8q0-.425.288-.712T12 7t.713.288T13 8z\"/>" },
  "key-round": { v:1024, b:"<path fill=\"currentColor\" d=\"M608 112c-167.9 0-304 136.1-304 304c0 70.3 23.9 135 63.9 186.5L255.8 713.6l-62.3-62.3c-3.148-3.08-8.252-3.08-11.4 0l-39.8 39.8c-3.08 3.148-3.08 8.252 0 11.4l62.3 62.3l-44.9 44.9l-62.3-62.3c-3.148-3.08-8.252-3.08-11.4 0l-39.8 39.8c-3.08 3.148-3.08 8.252 0 11.4l110.3 111.2c3.1 3.1 8.2 3.1 11.3 0l253.6-253.6A304.1 304.1 0 0 0 608 720c167.9 0 304-136.1 304-304S775.9 112 608 112m161.2 465.2C726.2 620.3 668.9 644 608 644s-118.2-23.7-161.2-66.8C403.7 534.2 380 476.9 380 416s23.7-118.2 66.8-161.2c43-43.1 100.3-66.8 161.2-66.8s118.2 23.7 161.2 66.8c43.1 43 66.8 100.3 66.8 161.2s-23.7 118.2-66.8 161.2\"/>" },
  "layers": { v:24, b:"<path fill=\"currentColor\" d=\"m12 21.05l-9-7l1.65-1.25L12 18.5l7.35-5.7L21 14.05zM12 16L3 9l9-7l9 7z\"/>" },
  "menu": { v:24, b:"<path fill=\"currentColor\" d=\"M3 18v-2h18v2zm0-5v-2h18v2zm0-5V6h18v2z\"/>" },
  "moon": { v:24, b:"<path fill=\"currentColor\" d=\"M14 22q-2.075 0-3.9-.788t-3.175-2.137T4.788 15.9T4 12t.788-3.9t2.137-3.175T10.1 2.788T14 2q.875 0 1.75.175t1.675.525q.3.125.45.387t.15.538q0 .225-.088.425t-.287.35q-1.75 1.375-2.7 3.375T14 12q0 2.25.925 4.25t2.7 3.35q.2.15.288.363T18 20.4q0 .275-.15.538t-.45.387q-.8.35-1.662.513T14 22\"/>" },
  "plus": { v:24, b:"<path fill=\"none\" stroke=\"currentColor\" stroke-linecap=\"round\" stroke-linejoin=\"round\" stroke-width=\"1.5\" d=\"M18 12h-6m0 0H6m6 0V6m0 6v6\"/>" },
  "refresh-cw": { v:24, b:"<path fill=\"currentColor\" d=\"M12 20q-3.35 0-5.675-2.325T4 12t2.325-5.675T12 4q1.725 0 3.3.712T18 6.75V5q0-.425.288-.712T19 4t.713.288T20 5v5q0 .425-.288.713T19 11h-5q-.425 0-.712-.288T13 10t.288-.712T14 9h3.2q-.8-1.4-2.187-2.2T12 6Q9.5 6 7.75 7.75T6 12t1.75 4.25T12 18q1.7 0 3.113-.862t2.187-2.313q.2-.35.563-.487t.737-.013q.4.125.575.525t-.025.75q-1.025 2-2.925 3.2T12 20\"/>" },
  "send": { v:24, b:"<path fill=\"none\" stroke=\"currentColor\" d=\"M5.5 13L18 6m-1.75 17.5h.25a72.7 72.7 0 0 1 6.504-21.962L23.26 1L23 .74l-.538.256A72.7 72.7 0 0 1 .5 7.5v.25l5 5v7.75h.25l1.774-1.69a12 12 0 0 1 2.313-1.723z\"/>" },
  "settings": { v:24, b:"<path fill=\"currentColor\" d=\"m9.25 22l-.4-3.2q-.325-.125-.612-.3t-.563-.375L4.7 19.375l-2.75-4.75l2.575-1.95Q4.5 12.5 4.5 12.338v-.675q0-.163.025-.338L1.95 9.375l2.75-4.75l2.975 1.25q.275-.2.575-.375t.6-.3l.4-3.2h5.5l.4 3.2q.325.125.613.3t.562.375l2.975-1.25l2.75 4.75l-2.575 1.95q.025.175.025.338v.674q0 .163-.05.338l2.575 1.95l-2.75 4.75l-2.95-1.25q-.275.2-.575.375t-.6.3l-.4 3.2zm2.8-6.5q1.45 0 2.475-1.025T15.55 12t-1.025-2.475T12.05 8.5q-1.475 0-2.488 1.025T8.55 12t1.013 2.475T12.05 15.5\"/>" },
  "sparkles": { v:24, b:"<path fill=\"currentColor\" d=\"m19 9l-1.25-2.75L15 5l2.75-1.25L19 1l1.25 2.75L23 5l-2.75 1.25L19 9Zm0 14l-1.25-2.75L15 19l2.75-1.25L19 15l1.25 2.75L23 19l-2.75 1.25L19 23ZM9 20l-2.5-5.5L1 12l5.5-2.5L9 4l2.5 5.5L17 12l-5.5 2.5L9 20Z\"/>" },
  "square": { v:24, b:"<path fill=\"currentColor\" d=\"M6 16V8q0-.825.588-1.412T8 6h8q.825 0 1.413.588T18 8v8q0 .825-.587 1.413T16 18H8q-.825 0-1.412-.587T6 16\"/>" },
  "sun": { v:24, b:"<path fill=\"currentColor\" d=\"M11 5V1h2v4zm6.65 2.75l-1.375-1.375l2.8-2.875l1.4 1.425zM19 13v-2h4v2zm-8 10v-4h2v4zM6.35 7.7L3.5 4.925l1.425-1.4L7.75 6.35zm12.7 12.8l-2.775-2.875l1.35-1.35l2.85 2.75zM1 13v-2h4v2zm3.925 7.5l-1.4-1.425l2.8-2.8l.725.675l.725.7zM12 18q-2.5 0-4.25-1.75T6 12t1.75-4.25T12 6t4.25 1.75T18 12t-1.75 4.25T12 18\"/>" },
  "thumbs-down": { v:24, b:"<path fill=\"currentColor\" d=\"M6 3h10v13l-7 7l-1.25-1.25q-.175-.175-.288-.475T7.35 20.7v-.35L8.45 16H3q-.8 0-1.4-.6T1 14v-2q0-.175.037-.375t.113-.375l3-7.05q.225-.5.75-.85T6 3m12 13V3h4v13z\"/>" },
  "thumbs-up": { v:24, b:"<path fill=\"currentColor\" d=\"M18 21H8V8l7-7l1.25 1.25q.175.175.288.475t.112.575v.35L15.55 8H21q.8 0 1.4.6T23 10v2q0 .175-.037.375t-.113.375l-3 7.05q-.225.5-.75.85T18 21M6 8v13H2V8z\"/>" },
  "trash-2": { v:24, b:"<path fill=\"currentColor\" d=\"M7 21q-.825 0-1.412-.587T5 19V6H4V4h5V3h6v1h5v2h-1v13q0 .825-.587 1.413T17 21zm2-4h2V8H9zm4 0h2V8h-2z\"/>" },
  "user": { v:24, b:"<path fill=\"currentColor\" d=\"M5.85 17.1q1.275-.975 2.85-1.537T12 15t3.3.563t2.85 1.537q.875-1.025 1.363-2.325T20 12q0-3.325-2.337-5.663T12 4T6.337 6.338T4 12q0 1.475.488 2.775T5.85 17.1M12 13q-1.475 0-2.488-1.012T8.5 9.5t1.013-2.488T12 6t2.488 1.013T15.5 9.5t-1.012 2.488T12 13m0 9q-2.075 0-3.9-.788t-3.175-2.137T2.788 15.9T2 12t.788-3.9t2.137-3.175T8.1 2.788T12 2t3.9.788t3.175 2.137T21.213 8.1T22 12t-.788 3.9t-2.137 3.175t-3.175 2.138T12 22\"/>" },
  "wrench": { v:24, b:"<path fill=\"currentColor\" d=\"M18.85 21.975q-.2 0-.375-.062t-.325-.213l-5.1-5.1q-.15-.15-.213-.325t-.062-.375t.063-.375t.212-.325l2.125-2.125q.15-.15.325-.212t.375-.063t.375.063t.325.212l5.1 5.1q.15.15.213.325t.062.375t-.062.375t-.213.325L19.55 21.7q-.15.15-.325.213t-.375.062M5.125 22q-.2 0-.387-.075T4.4 21.7l-2.1-2.1q-.15-.15-.225-.338T2 18.876t.075-.375t.225-.325l5.3-5.3h2.125l.85-.85L6.45 7.9H5.025L2 4.875L4.825 2.05L7.85 5.075V6.5l4.125 4.125l2.9-2.9L13.8 6.65l1.4-1.4h-2.825l-.7-.7L15.225 1l.7.7v2.825l1.4-1.4l3.55 3.55q.425.425.65.963t.225 1.137t-.225 1.15t-.65.975L18.75 8.775l-1.4 1.4l-1.05-1.05l-5.175 5.175v2.1l-5.3 5.3q-.15.15-.325.225T5.125 22\"/>" }
,
  "close": { v:24, b:"<path fill=\"currentColor\" d=\"m12 13.4l-4.9 4.9q-.275.275-.7.275t-.7-.275t-.275-.7t.275-.7l4.9-4.9l-4.9-4.9q-.275-.275-.275-.7t.275-.7t.7-.275t.7.275l4.9 4.9l4.9-4.9q.275-.275.7-.275t.7.275t.275.7t-.275.7L13.4 12l4.9 4.9q.275.275.275.7t-.275.7t-.7.275t-.7-.275z\"/>" },
  "logout": { v:24, b:"<path fill=\"currentColor\" d=\"M5 21q-.825 0-1.412-.587T3 19V5q0-.825.588-1.412T5 3h6q.425 0 .713.288T12 4t-.288.713T11 5H5v14h6q.425 0 .713.288T12 20t-.288.713T11 21zm12.175-8H10q-.425 0-.712-.288T9 12t.288-.712T10 11h7.175L15.3 9.125q-.275-.275-.275-.675t.275-.7t.7-.313t.725.288L20.3 11.3q.3.3.3.7t-.3.7l-3.575 3.575q-.3.3-.712.288t-.713-.313q-.275-.3-.262-.712t.287-.688z\"/>" },
  "loading": { v:24, b:"<path fill=\"currentColor\" d=\"M12 2A10 10 0 1 0 22 12A10 10 0 0 0 12 2Zm0 18a8 8 0 1 1 8-8A8 8 0 0 1 12 20Z\" opacity=\".5\"/><path fill=\"currentColor\" d=\"M20 12h2A10 10 0 0 0 12 2V4A8 8 0 0 1 20 12Z\"><animateTransform attributeName=\"transform\" dur=\"1s\" from=\"0 12 12\" repeatCount=\"indefinite\" to=\"360 12 12\" type=\"rotate\"/></path>" },
  "three-dots-loading": { v:24, b:"<circle cx=\"18\" cy=\"12\" r=\"0\" fill=\"currentColor\"><animate attributeName=\"r\" begin=\".67\" calcMode=\"spline\" dur=\"1.5s\" keySplines=\"0.2 0.2 0.4 0.8;0.2 0.2 0.4 0.8;0.2 0.2 0.4 0.8\" repeatCount=\"indefinite\" values=\"0;2;0;0\"/></circle><circle cx=\"12\" cy=\"12\" r=\"0\" fill=\"currentColor\"><animate attributeName=\"r\" begin=\".33\" calcMode=\"spline\" dur=\"1.5s\" keySplines=\"0.2 0.2 0.4 0.8;0.2 0.2 0.4 0.8;0.2 0.2 0.4 0.8\" repeatCount=\"indefinite\" values=\"0;2;0;0\"/></circle><circle cx=\"6\" cy=\"12\" r=\"0\" fill=\"currentColor\"><animate attributeName=\"r\" begin=\"0\" calcMode=\"spline\" dur=\"1.5s\" keySplines=\"0.2 0.2 0.4 0.8;0.2 0.2 0.4 0.8;0.2 0.2 0.4 0.8\" repeatCount=\"indefinite\" values=\"0;2;0;0\"/></circle>" },
  "format-indent-decrease": { v:24, b:"<path fill=\"currentColor\" d=\"M4 21q-.425 0-.712-.288T3 20t.288-.712T4 19h16q.425 0 .713.288T21 20t-.288.713T20 21zm8-4q-.425 0-.712-.288T11 16t.288-.712T12 15h8q.425 0 .713.288T21 16t-.288.713T20 17zm0-4q-.425 0-.712-.288T11 12t.288-.712T12 11h8q.425 0 .713.288T21 12t-.288.713T20 13zm0-4q-.425 0-.712-.288T11 8t.288-.712T12 7h8q.425 0 .713.288T21 8t-.288.713T20 9zM4 5q-.425 0-.712-.288T3 4t.288-.712T4 3h16q.425 0 .713.288T21 4t-.288.713T20 5zm2.15 10.15l-2.8-2.8Q3.2 12.2 3.2 12t.15-.35l2.8-2.8q.25-.25.55-.125T7 9.2v5.6q0 .35-.3.475t-.55-.125\"/>" },
  "format-indent-increase": { v:24, b:"<path fill=\"currentColor\" d=\"M4 21q-.425 0-.712-.288T3 20t.288-.712T4 19h16q.425 0 .713.288T21 20t-.288.713T20 21zm8-4q-.425 0-.712-.288T11 16t.288-.712T12 15h8q.425 0 .713.288T21 16t-.288.713T20 17zm0-4q-.425 0-.712-.288T11 12t.288-.712T12 11h8q.425 0 .713.288T21 12t-.288.713T20 13zm0-4q-.425 0-.712-.288T11 8t.288-.712T12 7h8q.425 0 .713.288T21 8t-.288.713T20 9zM4 5q-.425 0-.712-.288T3 4t.288-.712T4 3h16q.425 0 .713.288T21 4t-.288.713T20 5zm-.15 10.15q-.25.25-.55.125T3 14.8V9.2q0-.35.3-.475t.55.125l2.8 2.8q.15.15.15.35t-.15.35z\"/>" },
  "minimize": { v:24, b:"<path fill=\"currentColor\" d=\"M7 21q-.425 0-.712-.288T6 20t.288-.712T7 19h10q.425 0 .713.288T18 20t-.288.713T17 21z\"/>" },
  "user-outlined": { v:1024, b:"<path fill=\"currentColor\" d=\"M858.5 763.6a374 374 0 0 0-80.6-119.5a375.6 375.6 0 0 0-119.5-80.6c-.4-.2-.8-.3-1.2-.5C719.5 518 760 444.7 760 362c0-137-111-248-248-248S264 225 264 362c0 82.7 40.5 156 102.8 201.1c-.4.2-.8.3-1.2.5c-44.8 18.9-85 46-119.5 80.6a375.6 375.6 0 0 0-80.6 119.5A371.7 371.7 0 0 0 136 901.8a8 8 0 0 0 8 8.2h60c4.4 0 7.9-3.5 8-7.8c2-77.2 33-149.5 87.8-204.3c56.7-56.7 132-87.9 212.2-87.9s155.5 31.2 212.2 87.9C779 752.7 810 825 812 902.2c.1 4.4 3.6 7.8 8 7.8h60a8 8 0 0 0 8-8.2c-1-47.8-10.9-94.3-29.5-138.2M512 534c-45.9 0-89.1-17.9-121.6-50.4S340 407.9 340 362s17.9-89.1 50.4-121.6S466.1 190 512 190s89.1 17.9 121.6 50.4S684 316.1 684 362s-17.9 89.1-50.4 121.6S557.9 534 512 534\"/>" },
  "plug-connected": { v:24, b:"<path fill=\"currentColor\" d=\"M17.78 3.28a.75.75 0 0 0-1.06-1.06l-2.446 2.445a4.04 4.04 0 0 0-5.128.481l-.3.3a1.49 1.49 0 0 0 0 2.108l2.465 2.464a5.51 5.51 0 0 1 4.552-.848a4.04 4.04 0 0 0-.528-3.444zM7.554 8.846l2.464 2.465a5.51 5.51 0 0 0-.848 4.552a4.04 4.04 0 0 1-3.444-.528L3.28 17.78a.75.75 0 0 1-1.06-1.06l2.446-2.446a4.04 4.04 0 0 1 .48-5.128l.3-.3a1.49 1.49 0 0 1 2.108 0M19 14.5a4.5 4.5 0 1 1-9 0a4.5 4.5 0 0 1 9 0m-2.146-1.854a.5.5 0 0 0-.708 0L13.5 15.293l-.646-.647a.5.5 0 0 0-.708.708l1 1a.5.5 0 0 0 .708 0l3-3a.5.5 0 0 0 0-.708\"/>" },
  "plug-disconnected": { v:24, b:"<path fill=\"none\" stroke=\"currentColor\" stroke-linecap=\"round\" stroke-linejoin=\"round\" stroke-width=\"2\" d=\"m20 16l-4 4m-9-8l5 5l-1.5 1.5a3.536 3.536 0 1 1-5-5zm10 0l-5-5l1.5-1.5a3.536 3.536 0 1 1 5 5zM3 21l2.5-2.5m13-13L21 3m-11 8l-2 2m5 1l-2 2m5 0l4 4\"/>" },
  "verified": { v:24, b:"<path fill=\"currentColor\" d=\"M9.592 3.2a6 6 0 0 1-.495.399c-.298.2-.633.338-.985.408c-.153.03-.313.043-.632.068c-.801.064-1.202.096-1.536.214a2.71 2.71 0 0 0-1.655 1.655c-.118.334-.15.735-.214 1.536a6 6 0 0 1-.068.632c-.07.352-.208.687-.408.985c-.087.13-.191.252-.399.495c-.521.612-.782.918-.935 1.238c-.353.74-.353 1.6 0 2.34c.153.32.414.626.935 1.238c.208.243.312.365.399.495c.2.298.338.633.408.985c.03.153.043.313.068.632c.064.801.096 1.202.214 1.536a2.71 2.71 0 0 0 1.655 1.655c.334.118.735.15 1.536.214c.319.025.479.038.632.068c.352.07.687.209.985.408c.13.087.252.191.495.399c.612.521.918.782 1.238.935c.74.353 1.6.353 2.34 0c.32-.153.626-.414 1.238-.935c.243-.208.365-.312.495-.399c.298-.2.633-.338.985-.408c.153-.03.313-.043.632-.068c.801-.064 1.202-.096 1.536-.214a2.71 2.71 0 0 0 1.655-1.655c.118-.334.15-.735.214-1.536c.025-.319.038-.479.068-.632c.07-.352.209-.687.408-.985c.087-.13.191-.252.399-.495c.521-.612.782-.918.935-1.238c.353-.74.353-1.6 0-2.34c-.153-.32-.414-.626-.935-1.238a6 6 0 0 1-.399-.495a2.7 2.7 0 0 1-.408-.985a6 6 0 0 1-.068-.632c-.064-.801-.096-1.202-.214-1.536a2.71 2.71 0 0 0-1.655-1.655c-.334-.118-.735-.15-1.536-.214a6 6 0 0 1-.632-.068a2.7 2.7 0 0 1-.985-.408a6 6 0 0 1-.495-.399c-.612-.521-.918-.782-1.238-.935a2.71 2.71 0 0 0-2.34 0c-.32.153-.626.414-1.238.935\" opacity=\".5\"/><path fill=\"currentColor\" d=\"M16.374 9.863a.814.814 0 0 0-1.151-1.151l-4.85 4.85l-1.595-1.595a.814.814 0 0 0-1.151 1.151l2.17 2.17a.814.814 0 0 0 1.15 0z\"/>" },
  "logo": { v:48, b:"<rect width=\"48\" height=\"48\" rx=\"11\" fill=\"#4176e6\"/><path d=\"M14 14h20l-20 20h20\" fill=\"none\" stroke=\"#fff\" stroke-width=\"5.5\" stroke-linecap=\"round\" stroke-linejoin=\"round\"/><g fill=\"none\" stroke=\"#fff\" stroke-width=\"3\" stroke-linecap=\"round\" opacity=\"0.45\"><path d=\"M6 13 12 7\"/><path d=\"M7 18 13 12\"/><path d=\"M36 41 42 35\"/><path d=\"M33 44 39 38\"/></g>" }
};
function ic(name, size) {
  var s = size || 16;
  var it = ICONS[name] || { v: 24, b: '' };
  var vb = it.v || 24;
  return '<svg class="ic" width="' + s + '" height="' + s + '" viewBox="0 0 ' + vb + ' ' + vb + '" aria-hidden="true">' + it.b + '</svg>';
}
function iconEl(name, size) {
  var d = document.createElement('span');
  d.innerHTML = ic(name, size);
  return d.firstChild;
}
function applyStaticIcons() {
  var nodes = document.querySelectorAll('[data-ic]');
  for (var i = 0; i < nodes.length; i++) {
    nodes[i].innerHTML = ic(nodes[i].getAttribute('data-ic'), parseInt(nodes[i].getAttribute('data-ic-size') || '16', 10));
  }
  nodes = document.querySelectorAll('[data-ic-prepend]');
  for (var j = 0; j < nodes.length; j++) {
    nodes[j].insertAdjacentHTML('afterbegin', ic(nodes[j].getAttribute('data-ic-prepend'), parseInt(nodes[j].getAttribute('data-ic-size') || '16', 10)));
  }
}
initTheme();
applyStaticIcons();
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
  return t.length > 14 ? t.slice(0, 14) + '…' : (t || '新会话');
}

function loadConversations() {
  try { conversations = JSON.parse(localStorage.getItem(LS_CONVS) || '[]'); } catch(e) { conversations = []; }
  if (!conversations.length) {
    conversations.push({ id: 'c' + Date.now(), sid: '', title: '新会话', createdAt: nowFull(), messages: [] });
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
    del.className = 'del icon-btn'; del.title = '删除会话';
    del.innerHTML = ic('trash-2', 13);
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
  var c = { id: 'c' + Date.now(), sid: '', title: '新会话', createdAt: nowFull(), messages: [] };
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
    conversations.push({ id: 'c' + Date.now(), sid: '', title: '新会话', createdAt: nowFull(), messages: [] });
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
      var b = bubble(m.role, m.role === 'user' ? '你' : 'Zebra', m.role === 'user' ? 'user' : 'logo');
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
      r.className = 'phase';
      r.appendChild(iconEl('sparkles', 12));
      r.appendChild(document.createTextNode(' ' + ev.text));
      wrap.appendChild(r);
    } else if (ev.t === 'skill' && ev.name) {
      var s = document.createElement('div');
      s.className = 'phase skill';
      s.appendChild(iconEl('layers', 12));
      s.appendChild(document.createTextNode(' 技能：' + ev.name));
      wrap.appendChild(s);
    } else if (ev.t === 'tool' && ev.name) {
      tools[ev.name] = (tools[ev.name] || 0) + 1;
      if (!toolsRow) { toolsRow = document.createElement('div'); toolsRow.className = 'tools-row'; wrap.appendChild(toolsRow); }
      var names = Object.keys(tools), parts = [];
      for (var i = 0; i < names.length; i++) parts.push(names[i] + '×' + tools[names[i]]);
      toolsRow.innerHTML = '';
      toolsRow.appendChild(iconEl('wrench', 12));
      toolsRow.appendChild(document.createTextNode(' 工具：' + parts.join(' · ')));
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
  document.getElementById('themeBtn').innerHTML = t === 'dark' ? ic('sun', 14) + ' 亮色' : ic('moon', 14) + ' 暗色';
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

function bubble(role, name, icon) {
  var wrap = document.createElement('div');
  wrap.className = 'msg ' + role;
  var av = document.createElement('div');
  av.className = 'avatar'; av.innerHTML = ic(icon, icon === 'logo' ? 24 : 17);
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
  row.className = 'phase';
  row.appendChild(iconEl('sparkles', 12));
  row.appendChild(document.createTextNode(' ' + text));
  ensureActivity().appendChild(row);
  scrollBottom();
}
function addSkill(name) {
  var row = document.createElement('div');
  row.className = 'phase skill';
  row.appendChild(iconEl('layers', 12));
  row.appendChild(document.createTextNode(' 技能：' + name));
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
  row.innerHTML = '';
  row.appendChild(iconEl('wrench', 12));
  row.appendChild(document.createTextNode(' 工具：' + parts.join(' · ')));
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
  s.className = 'err-text';
  s.appendChild(iconEl('alert-circle', 14));
  s.appendChild(document.createTextNode(' ' + text));
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
  copy.className = 'icon-btn';
  copy.innerHTML = ic('copy', 12) + ' 复制';
  copy.onclick = function() { navigator.clipboard.writeText(b.innerText); setStatus('已复制', ''); };
  actions.appendChild(copy);
  if (wrap.classList.contains('assistant')) {
    var up = document.createElement('button');
    up.className = 'icon-btn';
    up.innerHTML = ic('thumbs-up', 12) + ' 赞';
    var down = document.createElement('button');
    down.className = 'icon-btn';
    down.innerHTML = ic('thumbs-down', 12) + ' 踩';
    up.onclick = function() { feedback(1, up, down); };
    down.onclick = function() { feedback(-1, up, down); };
    actions.appendChild(up); actions.appendChild(down);
    var retry = document.createElement('button');
    retry.className = 'icon-btn';
    retry.innerHTML = ic('refresh-cw', 12) + ' 重试';
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
function toggleKey(btn) {
  if (keyEl.type === 'password') { keyEl.type = 'text'; btn.innerHTML = ic('key-round', 14) + ' 隐藏'; }
  else { keyEl.type = 'password'; btn.innerHTML = ic('key-round', 14) + ' 显示'; }
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
  if (!c.title || c.title === '新会话') { c.title = convTitleOf(text); renderSidebar(); }
  var q = text;
  var userBubble = bubble('user', '你', 'user');
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

  var body = { message: text, stream: true, confirm_risky: document.getElementById('confirmRisky').checked, session_id: c.sid, mode: modeEl.value };
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
        var ev = null;
        try { ev = JSON.parse(data); } catch(e) { /* 非 JSON 载荷（如裸字符串 session id）容错 */ }
        if (type === 'session') {
          c.sid = (typeof ev === 'string' && ev) || data.trim();
          saveConvs(); renderSidebar();
        } else if (!ev) {
          continue;
        } else if (type === 'phase') {
          typingIndicator(false);
          activity.push({ t: 'phase', text: ev.phase || '' });
          addPhase(ev.phase || '');
        } else if (type === 'delta') {
          typingIndicator(false);
          if (!answerEl) {
            answerEl = bubble('assistant', 'Zebra', 'logo');
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

// faviconHandler 返回浏览器标签图标（品牌 Z logo，圆角蓝底 + 白 Z 闪电，与主题色一致）。
func faviconHandler() http.HandlerFunc {
	const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><rect width="48" height="48" rx="11" fill="#4176e6"/><path d="M14 14h20l-20 20h20" fill="none" stroke="#fff" stroke-width="5.5" stroke-linecap="round" stroke-linejoin="round"/><g fill="none" stroke="#fff" stroke-width="3" stroke-linecap="round" opacity="0.45"><path d="M6 13 12 7"/><path d="M7 18 13 12"/><path d="M36 41 42 35"/><path d="M33 44 39 38"/></g></svg>`
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write([]byte(faviconSVG))
	}
}
