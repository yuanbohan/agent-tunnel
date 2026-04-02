import{c as m,C as f,e as v,d as y}from"./protocol-D7XtB1cA.js";async function _(){const e=await fetch("/api/sessions",{credentials:"same-origin"});if(!e.ok)throw new Error(`failed to load sessions: ${e.status}`);return e.json()}function w(e,s){return`${e.protocol==="https:"?"wss:":"ws:"}//${e.host}/api/sessions/${encodeURIComponent(s)}/ws`}function $(e){var i,t;const s=((i=e.label)==null?void 0:i.trim())||c(e.launcher),n=c(e.launcher),r=((t=e.last_preview)==null?void 0:t.trim())||"No preview yet";return`
    <a class="session-card" href="/sessions/${encodeURIComponent(e.session_id)}">
      <div class="session-card__row">
        <div class="session-card__identity">
          <span class="session-card__icon">${C(e.launcher)}</span>
          <div class="session-card__identity-copy">
            <div class="session-card__title">${a(s)}</div>
            <div class="session-card__launcher">${a(n)}</div>
          </div>
        </div>
        <div class="session-card__time">${a(g(e.last_active_at))}</div>
      </div>
      <div class="session-card__command">${a(e.command_preview)}</div>
      <div class="session-card__cwd">${a(e.cwd)}</div>
      <div class="session-card__preview">${a(r)}</div>
    </a>
  `}function C(e){switch(e.trim().toLowerCase()){case"codex":return"CX";case"gemini":return"GM";case"claude":return"CL";default:return"--"}}function c(e){const s=e.trim();return s===""?"Unknown":s.charAt(0).toUpperCase()+s.slice(1)}function g(e){if(!e)return"--";const s=Date.parse(e);if(Number.isNaN(s))return"--";const n=Math.max(0,Math.floor((Date.now()-s)/1e3));return n<60?"now":n<3600?`${Math.floor(n/60)}m`:n<86400?`${Math.floor(n/3600)}h`:`${Math.floor(n/86400)}d`}function a(e){return e.split("&").join("&amp;").split("<").join("&lt;").split(">").join("&gt;").split('"').join("&quot;")}function b(e){if(e==="/"||e==="")return{kind:"dashboard"};const s=e.match(/^\/sessions\/([^/]+)$/);return s?{kind:"session",sessionId:decodeURIComponent(s[1])}:{kind:"dashboard"}}function L(e){return!e}function l(e){return e?"Input on":"Read-only"}function d(e){return e?"input-chip input-chip--enabled":"input-chip"}const p=document.getElementById("relay-root"),u=b(window.location.pathname);u.kind==="dashboard"?M():I(u.sessionId);async function M(){p.innerHTML=`
    <main class="relay-shell">
      <header class="relay-shell__header">
        <div>
          <p class="relay-shell__eyebrow">agent-tunnel relay</p>
          <h1 class="relay-shell__title">Live sessions</h1>
        </div>
      </header>
      <section id="relay-list" class="relay-list">
        <div class="relay-placeholder">Loading sessions…</div>
      </section>
    </main>
  `;const e=document.getElementById("relay-list");try{const s=await _();if(s.length===0){e.innerHTML='<div class="relay-placeholder">No live sessions right now.</div>';return}e.innerHTML=s.map($).join("")}catch(s){e.innerHTML='<div class="relay-placeholder">Failed to load sessions.</div>',console.error(s)}}function I(e){p.innerHTML=`
    <main class="relay-shell relay-shell--session">
      <header class="session-header">
        <a class="back-link" href="/">Live</a>
        <button id="input-chip" class="${d(!1)}">${l(!1)}</button>
      </header>
      <section id="terminal" class="relay-terminal"></section>
    </main>
  `;const s=m(document.getElementById("terminal")),n=new f(w(window.location,e)),r=document.getElementById("input-chip");let i=!1;r.addEventListener("click",()=>{i=L(i),r.textContent=l(i),r.className=d(i)}),s.onData(t=>{i&&n.send(v(t))}),s.onResize((t,o)=>{n.send(JSON.stringify({type:"resize",cols:t,rows:o}))}),n.onMessage(t=>{t.type==="output"&&s.write(y(t))}),n.onStatusChange(t=>{if(t!=="connected")return;const{cols:o,rows:h}=s.currentSize();n.send(JSON.stringify({type:"resize",cols:o,rows:h}))})}
