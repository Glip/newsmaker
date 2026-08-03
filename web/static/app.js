const mediaItems = [];
let previewTimer = null;

function selectedChannelInfos() {
  return [...document.querySelectorAll('input[name="channel"]:checked')].map((el) => ({
    id: Number(el.value),
    platform: el.dataset.platform,
    name: el.dataset.name,
  }));
}

function selectedChannels() {
  return selectedChannelInfos().map((c) => c.id);
}

function wrapSelection(open, close) {
  const ta = document.getElementById("text");
  const start = ta.selectionStart;
  const end = ta.selectionEnd;
  const value = ta.value;
  ta.value = value.slice(0, start) + open + value.slice(start, end) + close + value.slice(end);
  ta.focus();
  ta.selectionStart = start + open.length;
  ta.selectionEnd = end + open.length;
  schedulePreview();
}

document.querySelectorAll(".toolbar [data-wrap]").forEach((btn) => {
  btn.addEventListener("click", () => wrapSelection(btn.dataset.wrap, btn.dataset.wrapEnd));
});

document.getElementById("btn-link")?.addEventListener("click", () => {
  const href = prompt("URL");
  if (!href) return;
  wrapSelection(`<a href="${href}">`, "</a>");
});

document.getElementById("btn-apply-template")?.addEventListener("click", () => {
  const sel = document.getElementById("template-select");
  const opt = sel.selectedOptions[0];
  if (!opt || !opt.dataset.body) return;
  let body = opt.dataset.body;
  const title = prompt("title", "") ?? "";
  const textBody = prompt("body", "") ?? "";
  const date = new Date().toISOString().slice(0, 10);
  body = body.replaceAll("{{title}}", title).replaceAll("{{body}}", textBody).replaceAll("{{date}}", date);
  const ta = document.getElementById("text");
  ta.value = ta.value ? ta.value + "\n\n" + body : body;
  schedulePreview();
});

function mediaHTML() {
  if (!mediaItems.length) return "";
  const parts = mediaItems.map((m) => {
    if (m.kind === "video") {
      return `<div class="preview-media"><video src="${m.url || ""}" controls muted></video><span>${escapeHtml(m.filename)}</span></div>`;
    }
    if (m.kind === "audio") {
      return `<div class="preview-media preview-audio"><audio src="${m.url || ""}" controls></audio><span>${escapeHtml(m.filename)}</span></div>`;
    }
    return `<div class="preview-media"><img src="${m.url || ""}" alt=""><span>${escapeHtml(m.filename)}</span></div>`;
  });
  return `<div class="preview-media-row">${parts.join("")}</div>`;
}

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

async function refreshPreview() {
  const box = document.getElementById("previews");
  if (!box) return;
  const channels = selectedChannelInfos();
  if (!channels.length) {
    box.innerHTML = `<p class="muted">Выберите канал — появится превью.</p>`;
    return;
  }
  const res = await fetch("/api/preview", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      text: document.getElementById("text").value,
      use_signature: document.getElementById("use-signature").checked,
    }),
  });
  const data = await res.json();
  if (!res.ok) {
    box.innerHTML = `<p class="error">${escapeHtml(data.error || "preview error")}</p>`;
    return;
  }
  const byPlatform = {};
  for (const p of data.previews || []) {
    byPlatform[p.platform] = p;
  }
  const media = mediaHTML();
  box.innerHTML = channels
    .map((ch) => {
      const p = byPlatform[ch.platform] || { label: ch.platform, html: "", note: "" };
      const note = p.note ? `<p class="preview-note">${escapeHtml(p.note)}</p>` : "";
      return `<article class="preview-card platform-${escapeHtml(ch.platform)}">
        <header>
          <span class="preview-platform">${escapeHtml(p.label || ch.platform)}</span>
          <strong>${escapeHtml(ch.name)}</strong>
        </header>
        ${media}
        <div class="preview-body">${p.html || ""}</div>
        ${note}
      </article>`;
    })
    .join("");
}

function schedulePreview() {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(refreshPreview, 200);
}

document.getElementById("text")?.addEventListener("input", schedulePreview);
document.getElementById("use-signature")?.addEventListener("change", schedulePreview);
document.querySelectorAll('input[name="channel"]').forEach((el) => {
  el.addEventListener("change", schedulePreview);
});

document.getElementById("file")?.addEventListener("change", async (e) => {
  const files = [...e.target.files];
  const list = document.getElementById("media-list");
  for (const file of files) {
    const fd = new FormData();
    fd.append("file", file);
    const res = await fetch("/api/upload", { method: "POST", body: fd });
    const data = await res.json();
    if (!res.ok) {
      alert(data.error || "upload failed");
      continue;
    }
    mediaItems.push(data);
    const li = document.createElement("li");
    li.innerHTML = `<span>${data.kind}: ${data.filename}</span><button type="button">×</button>`;
    li.querySelector("button").addEventListener("click", () => {
      const idx = mediaItems.findIndex((m) => m.id === data.id);
      if (idx >= 0) mediaItems.splice(idx, 1);
      li.remove();
      schedulePreview();
    });
    list.appendChild(li);
  }
  e.target.value = "";
  schedulePreview();
});

document.getElementById("btn-send")?.addEventListener("click", async () => {
  const status = document.getElementById("status");
  const channel_ids = selectedChannels();
  if (!channel_ids.length) {
    status.innerHTML = `<div class="item err">Выберите канал</div>`;
    return;
  }
  status.innerHTML = `<div class="item">Отправка…</div>`;
  const payload = {
    text: document.getElementById("text").value,
    channel_ids,
    media: mediaItems,
    use_signature: document.getElementById("use-signature").checked,
  };
  const res = await fetch("/api/send", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const data = await res.json();
  if (!res.ok) {
    status.innerHTML = `<div class="item err">${data.error || "error"}</div>`;
    return;
  }
  status.innerHTML = (data.results || [])
    .map((r) => {
      const cls = r.OK || r.ok ? "ok" : "err";
      const name = r.ChannelName || r.channel_name || r.channel_id;
      const platform = r.Platform || r.platform || "";
      const ok = r.OK || r.ok;
      const url = r.PostURL || r.post_url || "";
      const ref = r.MessageRef || r.message_ref || "";
      let msg;
      if (!ok) {
        msg = r.Error || r.error || "error";
      } else if (url) {
        msg = `<a href="${escapeHtml(url)}" target="_blank" rel="noopener">открыть пост</a>`;
      } else if (ref && ref !== "ok") {
        msg = `ok · ref ${escapeHtml(ref)}`;
      } else {
        msg = "ok";
      }
      return `<div class="item ${cls}"><strong>${escapeHtml(platform)}</strong> ${escapeHtml(String(name))}: ${msg}</div>`;
    })
    .join("");
});

schedulePreview();
