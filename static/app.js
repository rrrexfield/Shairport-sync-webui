/* Shairport Sync WebUI 前端逻辑：零依赖 */
"use strict";

const POLL_MS = 2000;
let lastStatus = null;
let pollTimer = null;
let failCount = 0;
let busy = false; // 服务操作互斥（前端侧）

/* ---------- 工具 ---------- */

const $ = (id) => document.getElementById(id);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function toast(msg, ok) {
  const t = $("toast");
  t.textContent = msg;
  t.className = "toast " + (ok ? "ok" : "err");
  clearTimeout(t._h);
  t._h = setTimeout(() => t.classList.add("hidden"), 3000);
}

async function fetchJSON(url, opts) {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), 8000);
  try {
    const resp = await fetch(url, Object.assign({ signal: ctrl.signal }, opts));
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || ("HTTP " + resp.status));
    return data;
  } finally {
    clearTimeout(t);
  }
}

function fmtMem(kb) {
  if (!kb) return "—";
  const mb = kb / 1024;
  return mb >= 1024 ? (mb / 1024).toFixed(1) + " GB" : Math.round(mb) + " MB";
}

function fmtTime(unix) {
  if (!unix) return "—";
  const d = new Date(unix * 1000);
  return d.toLocaleTimeString("zh-CN", { hour12: false });
}

/* ---------- 状态轮询 ---------- */

async function pollStatus() {
  try {
    const s = await fetchJSON("/api/status");
    lastStatus = s;
    failCount = 0;
    $("conn-banner").classList.add("hidden");
    renderAll(s);
  } catch (e) {
    failCount++;
    if (failCount >= 3) $("conn-banner").classList.remove("hidden");
  }
}

function startPolling() {
  stopPolling();
  pollStatus();
  pollTimer = setInterval(() => {
    if (!document.hidden) pollStatus();
  }, POLL_MS);
}

function stopPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
}

document.addEventListener("visibilitychange", () => {
  if (document.hidden) stopPolling();
  else startPolling();
});

/* ---------- 渲染 ---------- */

function renderAll(s) {
  $("webui-version").textContent = s.webui_version ? "v" + s.webui_version : "";
  renderService(s.service);
  renderPlayer(s);
  renderSys(s);
}

function renderService(svc) {
  const dot = $("svc-dot");
  dot.className = "dot " + (svc.active ? "running"
    : svc.state === "failed" || svc.state === "inactive" ? "stopped" : "unknown");
  $("svc-state").textContent = svc.active ? "运行中"
    : svc.state === "failed" ? "失败"
    : svc.state === "inactive" ? "已停止" : "未知";
  $("svc-detail").textContent = svc.available
    ? (svc.state + (svc.sub_state ? " / " + svc.sub_state : ""))
    : "无法获取状态";
  $("svc-since").textContent = svc.since || "—";
  $("svc-init").textContent = svc.init || "—";
  $("btn-start").disabled = busy || svc.active;
  $("btn-stop").disabled = busy || !svc.active;
  $("btn-restart").disabled = busy || !svc.active;
}

function renderPlayer(s) {
  const p = s.player;
  const st = $("player-state");
  let stateText = "空闲", stateClass = "";
  if (p.active_session) {
    stateText = "播放中"; stateClass = "playing";
  } else if (p.state && p.state !== "Not Available" && p.state !== "") {
    if (/play/i.test(p.state)) { stateText = "播放中"; stateClass = "playing"; }
    else if (/pause/i.test(p.state)) { stateText = "已暂停"; stateClass = "paused"; }
    else { stateText = p.state; }
  }
  st.textContent = stateText;
  st.className = "pill" + (stateClass ? " " + stateClass : "");

  // 音质：优先 4.x active_session 解析，其次 asfm 格式码
  let quality = "";
  if (p.sample_rate > 0) {
    quality = p.sample_rate + " Hz / " + p.bit_depth + " bit";
  }
  $("player-quality").textContent = quality || (s.track.format || "");

  const t = s.track;
  $("track-title").textContent = t.title || "未在播放";
  $("track-artist").textContent = [t.artist, t.album].filter(Boolean).join(" · ");
  $("player-protocol").textContent = p.protocol || "—";
  $("player-client").textContent = p.client || "—";
  $("player-progress").textContent = p.progress || "—";
}

function renderSys(s) {
  const sys = s.sys;
  $("sys-hostname").textContent = sys.hostname || "—";
  $("sys-wifi").textContent = sys.wifi_ssid || "未连接";
  const used = sys.mem_total_kb - sys.mem_avail_kb;
  $("sys-mem").textContent = sys.mem_total_kb
    ? fmtMem(used) + " / " + fmtMem(sys.mem_total_kb)
    : "—";
  $("mem-info").textContent = sys.mem_total_kb
    ? fmtMem(used) + " / " + fmtMem(sys.mem_total_kb)
    : "";
  $("sys-load").textContent = sys.load_percent !== undefined && sys.load_percent !== null
    ? sys.load_percent + "%"
    : "—";
  const mp = s.meta_pipe;
  $("sys-pipe").textContent = mp.error
    ? "异常：" + mp.error
    : mp.open ? "已连接（最近数据 " + (mp.last_item_at ? fmtTime(mp.last_item_at) : "—") + "）" : "未连接";
  $("sys-version").textContent = s.player.version_string || s.player.version || "—";
}

/* ---------- 服务控制 ---------- */

async function serviceAction(action) {
  if (busy) return;
  busy = true;
  const msg = $("svc-msg");
  msg.textContent = "正在" + ({ start: "启动", stop: "停止", restart: "重启" })[action] + "…";
  msg.className = "msg";
  try {
    await fetchJSON("/api/service/" + action, { method: "POST" });
    msg.textContent = "操作成功";
    msg.className = "msg ok";
    toast("服务操作成功", true);
    await pollStatus();
  } catch (e) {
    msg.textContent = "操作失败：" + e.message;
    msg.className = "msg err";
    toast("操作失败：" + e.message, false);
  } finally {
    busy = false;
    pollStatus();
  }
}

$("btn-start").onclick = () => serviceAction("start");
$("btn-stop").onclick = () => serviceAction("stop");
$("btn-restart").onclick = () => serviceAction("restart");
$("btn-refresh").onclick = () => pollStatus();

/* ---------- 配置折叠 ---------- */

$("config-toggle").onclick = () => {
  const btn = $("config-toggle");
  const body = $("config-body");
  const open = body.classList.toggle("open");
  btn.setAttribute("aria-expanded", open ? "true" : "false");
};

/* ---------- 分段控件 ---------- */

document.querySelectorAll(".seg").forEach((seg) => {
  seg.onclick = () => {
    document.querySelectorAll(".seg").forEach((x) => {
      x.classList.remove("active");
      x.setAttribute("aria-selected", "false");
    });
    seg.classList.add("active");
    seg.setAttribute("aria-selected", "true");
    ["form", "advanced", "raw"].forEach((name) => {
      $("tab-" + name).classList.toggle("hidden", name !== seg.dataset.tab);
    });
    if (seg.dataset.tab === "raw") loadRaw();
  };
});

/* ---------- 配置表单 ---------- */

// 表单初始快照与脏状态：有未保存修改时点亮保存按钮
const configInitial = { form: {}, formAdv: {}, raw: "" };

function snapshotForm(form) {
  const snap = {};
  form.querySelectorAll(".field").forEach((field) => {
    const cb = field.querySelector(".switch input");
    const inp = field.querySelector("input[type=text], select");
    if (cb && inp) {
      snap[inp.dataset.section + "." + inp.dataset.key] = { checked: cb.checked, value: inp.value };
    }
  });
  return snap;
}

function formDirty(form, snap) {
  const cur = snapshotForm(form);
  const keys = new Set([...Object.keys(cur), ...Object.keys(snap)]);
  for (const k of keys) {
    const a = cur[k], b = snap[k];
    if (!a || !b || a.checked !== b.checked || a.value !== b.value) return true;
  }
  return false;
}

function setSaveBtn(btn, dirty) {
  btn.disabled = !dirty;
  btn.classList.toggle("primary", dirty);
  btn.classList.toggle("plain", !dirty);
}

function updateDirtyButtons() {
  setSaveBtn($("btn-save-config"), formDirty($("config-form"), configInitial.form));
  setSaveBtn($("btn-save-config-adv"), formDirty($("config-form-adv"), configInitial.formAdv));
  const rawEl = $("raw-editor");
  const rawDirty = rawEl.dataset.initial !== undefined && rawEl.value !== rawEl.dataset.initial;
  setSaveBtn($("btn-save-raw"), rawDirty);
}

function enumLabel(v) {
  return v === "yes" ? "是" : v === "no" ? "否" : v;
}

// 自定义下拉菜单（原生 select 的 option 面板无法跨平台统一样式，
// 且折叠容器 overflow:hidden 会裁剪原生弹出层，故用 fixed 定位的自绘菜单）
function makeSelect(enumValues, selected, disabled, onChange) {
  const wrap = el("div", "select-wrap");

  const btn = el("button", "select-btn");
  btn.type = "button";
  btn.disabled = disabled;
  btn.setAttribute("aria-haspopup", "listbox");
  btn.setAttribute("aria-expanded", "false");
  const valEl = el("span", "select-value", enumLabel(selected));
  btn.appendChild(valEl);
  const chev = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  chev.setAttribute("viewBox", "0 0 24 24");
  chev.setAttribute("fill", "none");
  chev.setAttribute("stroke", "currentColor");
  chev.setAttribute("stroke-width", "2");
  chev.setAttribute("stroke-linecap", "round");
  chev.setAttribute("stroke-linejoin", "round");
  chev.setAttribute("class", "chevron");
  chev.setAttribute("aria-hidden", "true");
  const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
  p.setAttribute("d", "m6 9 6 6 6-6");
  chev.appendChild(p);
  btn.appendChild(chev);

  const menu = el("div", "select-menu hidden");
  menu.setAttribute("role", "listbox");
  enumValues.forEach((v) => {
    const opt = el("div", "select-option" + (v === selected ? " selected" : ""), enumLabel(v));
    opt.dataset.value = v;
    opt.setAttribute("role", "option");
    opt.setAttribute("aria-selected", v === selected ? "true" : "false");
    opt.tabIndex = -1;
    opt.onclick = (e) => {
      e.stopPropagation();
      valEl.textContent = enumLabel(v);
      menu.querySelectorAll(".select-option").forEach((x) => {
        const sel = x.dataset.value === v;
        x.classList.toggle("selected", sel);
        x.setAttribute("aria-selected", sel ? "true" : "false");
      });
      closeMenu(menu);
      onChange(v);
    };
    menu.appendChild(opt);
  });

  const open = () => {
    closeAllMenus(menu);
    const r = btn.getBoundingClientRect();
    menu.style.left = Math.round(r.left) + "px";
    menu.style.top = Math.round(r.bottom + 4) + "px";
    menu.style.minWidth = Math.round(r.width) + "px";
    menu.classList.remove("hidden");
    btn.setAttribute("aria-expanded", "true");
    const cur = menu.querySelector(".selected") || menu.firstChild;
    if (cur) cur.focus();
  };
  const close = () => closeMenu(menu);

  btn.onclick = (e) => {
    e.stopPropagation();
    if (menu.classList.contains("hidden")) open();
    else close();
  };
  // 键盘：Esc 关闭；菜单内方向键移动、Enter 选择
  btn.onkeydown = (e) => {
    if (e.key === "Escape") close();
  };
  menu.onkeydown = (e) => {
    const opts = [...menu.querySelectorAll(".select-option")];
    const idx = opts.indexOf(document.activeElement);
    if (e.key === "ArrowDown" && idx < opts.length - 1) { e.preventDefault(); opts[idx + 1].focus(); }
    else if (e.key === "ArrowUp" && idx > 0) { e.preventDefault(); opts[idx - 1].focus(); }
    else if (e.key === "Enter") { e.preventDefault(); document.activeElement.click(); }
    else if (e.key === "Escape") { close(); btn.focus(); }
  };

  wrap.appendChild(btn);
  wrap.appendChild(menu);
  return wrap;
}

function closeMenu(menu) {
  if (!menu) return;
  menu.classList.add("hidden");
  const btn = menu.parentElement.querySelector(".select-btn");
  if (btn) btn.setAttribute("aria-expanded", "false");
}

function closeAllMenus(except) {
  document.querySelectorAll(".select-menu:not(.hidden)").forEach((m) => {
    if (m !== except) closeMenu(m);
  });
}

// 菜单注册与全局关闭：点击外部、窗口滚动、resize 时收起
document.addEventListener("click", () => closeAllMenus());
window.addEventListener("scroll", () => closeAllMenus(), true);
window.addEventListener("resize", () => closeAllMenus());

function makeField(def, isAdv) {
  const row = el("div", "field");

  const top = el("div", "field-top");
  top.appendChild(el("label", "", def.label));

  // iOS 开关：开 = 使用自定义值，关 = 使用默认值（字段被注释）
  const sw = el("label", "switch");
  const cb = el("input");
  cb.type = "checkbox";
  cb.checked = !def.commented && def.editable;
  cb.disabled = !def.editable;
  cb.setAttribute("aria-label", def.label + "（使用自定义值）");
  sw.appendChild(cb);
  sw.appendChild(el("span", "slider"));
  top.appendChild(sw);
  row.appendChild(top);

  // 注意顺序：disabled 必须在创建自定义下拉之前赋值，否则开关关闭时下拉仍可点
  let input;
  if (def.type === "enum") {
    // 隐藏的原生 select 作为值存储（snapshot/collect 逻辑复用），
    // 视觉上使用自定义下拉菜单
    input = el("select");
    input.hidden = true;
    input.setAttribute("aria-hidden", "true");
    def.enum.forEach((v) => {
      const o = el("option", "", enumLabel(v));
      o.value = v;
      input.appendChild(o);
    });
    input.value = def.value || def.default;
  } else {
    input = el("input");
    input.type = "text";
    input.placeholder = def.default === "" ? "（空）" : def.default;
    input.value = def.value;
  }
  input.dataset.section = def.section;
  input.dataset.key = def.key;
  input.dataset.type = def.type;
  input.disabled = !cb.checked;

  if (def.type === "enum") {
    row.appendChild(input);
    row.appendChild(makeSelect(def.enum, input.value, input.disabled, (v) => {
      input.value = v;
      input.dispatchEvent(new Event("change")); // 触发脏检测
    }));
  } else {
    row.appendChild(input);
  }

  cb.onchange = () => {
    input.disabled = !cb.checked;
    if (cb.checked && !input.value) input.value = def.default;
    // 同步自定义下拉的禁用状态
    const sbtn = row.querySelector(".select-btn");
    if (sbtn) sbtn.disabled = input.disabled;
  };

  if (def.hint && !isAdv) {
    row.appendChild(el("div", "fhint", def.hint));
  }
  return row;
}

function loadConfigForm() {
  fetchJSON("/api/config").then((data) => {
    const form = $("config-form");
    const formAdv = $("config-form-adv");
    form.innerHTML = "";
    formAdv.innerHTML = "";
    data.fields.forEach((f) => {
      const target = f.group === "common" ? form : formAdv;
      target.appendChild(makeField(f, f.group !== "common"));
    });
    $("config-hint").textContent = "配置文件：" + data.path + "（修改后需重启 shairport-sync 服务生效）";
    // 重置初始快照与按钮状态
    configInitial.form = snapshotForm(form);
    configInitial.formAdv = snapshotForm(formAdv);
    updateDirtyButtons();
  }).catch((e) => toast("加载配置失败：" + e.message, false));
}

// 只收集与初始快照不同的字段（脏字段），未修改的字段不提交
function collectChanges(form, snap) {
  const changes = [];
  const cur = snapshotForm(form);
  for (const [key, v] of Object.entries(cur)) {
    const old = snap[key];
    if (!old || old.checked === v.checked && old.value === v.value) continue;
    const dot = key.indexOf(".");
    const section = key.slice(0, dot);
    const fkey = key.slice(dot + 1);
    if (!v.checked) {
      // 开关关 = 使用默认值 = 注释该字段
      changes.push({ section, key: fkey, action: "default" });
    } else {
      changes.push({ section, key: fkey, value: v.value, action: "set" });
    }
  }
  return changes;
}

async function saveConfig(form, msgEl, snap) {
  const changes = collectChanges(form, snap);
  if (changes.length === 0) {
    toast("没有可保存的修改", false);
    return;
  }
  msgEl.textContent = "正在保存…";
  msgEl.className = "msg";
  try {
    await fetchJSON("/api/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ changes: changes }),
    });
    msgEl.textContent = "已保存";
    msgEl.className = "msg ok";
    toast("配置已保存，重启服务后生效", true);
    loadConfigForm();
  } catch (e) {
    msgEl.textContent = "保存失败：" + e.message;
    msgEl.className = "msg err";
    toast("保存失败：" + e.message, false);
  }
}

$("btn-save-config").onclick = () => saveConfig($("config-form"), $("config-msg"), configInitial.form);
$("btn-save-config-adv").onclick = () => saveConfig($("config-form-adv"), $("config-msg"), configInitial.formAdv);

// 表单变化 → 重算脏状态（事件委托，input + change 覆盖文本/开关/下拉）
$("config-form").addEventListener("input", updateDirtyButtons);
$("config-form").addEventListener("change", updateDirtyButtons);
$("config-form-adv").addEventListener("input", updateDirtyButtons);
$("config-form-adv").addEventListener("change", updateDirtyButtons);

/* 原始编辑（含行号栏） */
async function loadRaw() {
  try {
    const resp = await fetch("/api/config/raw");
    const text = await resp.text();
    if (!resp.ok) throw new Error("HTTP " + resp.status);
    const rawEl = $("raw-editor");
    rawEl.value = text;
    rawEl.dataset.initial = text;
    updateLineNums();
    updateDirtyButtons();
  } catch (e) {
    toast("加载原文失败：" + e.message, false);
  }
}

let lastLineCount = -1;

// 行号重建（仅在行数变化时更新 DOM），重建后立即同步滚动位置
function updateLineNums() {
  const ta = $("raw-editor");
  const count = ta.value.split("\n").length;
  if (count === lastLineCount) {
    $("raw-linenums").scrollTop = ta.scrollTop; // 行数未变但位置可能已滚
    return;
  }
  lastLineCount = count;
  let html = "";
  for (let i = 1; i <= count; i++) html += i + "<br>";
  $("raw-linenums").innerHTML = html;
  $("raw-linenums").scrollTop = ta.scrollTop;
}

// 滚动同步：行号栏跟随编辑器滚动
$("raw-editor").addEventListener("scroll", () => {
  $("raw-linenums").scrollTop = $("raw-editor").scrollTop;
});

$("raw-editor").addEventListener("input", () => {
  updateLineNums();
  updateDirtyButtons();
});

/* ---------- 恢复默认配置（弹窗式确认） ---------- */

function showResetModal() {
  $("modal-overlay").classList.remove("hidden");
  $("modal-cancel").focus();
}

function hideResetModal() {
  $("modal-overlay").classList.add("hidden");
}

$("btn-reset-config").onclick = showResetModal;
$("modal-cancel").onclick = hideResetModal;
$("modal-confirm").onclick = () => {
  hideResetModal();
  doResetConfig();
};
// 点击遮罩关闭；Esc 关闭
$("modal-overlay").onclick = (e) => {
  if (e.target === $("modal-overlay")) hideResetModal();
};
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !$("modal-overlay").classList.contains("hidden")) {
    hideResetModal();
  }
});

async function doResetConfig() {
  const msg = $("reset-msg");
  msg.textContent = "正在恢复…";
  msg.className = "msg";
  try {
    const r = await fetchJSON("/api/config/reset", { method: "POST" });
    msg.textContent = "已恢复默认（备份 " + r.backup + "）";
    msg.className = "msg ok";
    toast("已恢复默认配置，重启服务后生效", true);
    loadConfigForm();
    loadRaw();
  } catch (e) {
    msg.textContent = "恢复失败：" + e.message;
    msg.className = "msg err";
    toast("恢复失败：" + e.message, false);
  }
}

$("btn-save-raw").onclick = async () => {
  const msg = $("raw-msg");
  msg.textContent = "正在保存…";
  msg.className = "msg";
  try {
    await fetchJSON("/api/config/raw", {
      method: "PUT",
      headers: { "Content-Type": "text/plain" },
      body: $("raw-editor").value,
    });
    msg.textContent = "已保存";
    msg.className = "msg ok";
    toast("配置已保存，重启服务后生效", true);
    loadRaw();
    loadConfigForm();
  } catch (e) {
    msg.textContent = "保存失败：" + e.message;
    msg.className = "msg err";
    toast("保存失败：" + e.message, false);
  }
};

/* ---------- 启动 ---------- */
loadConfigForm();
startPolling();
