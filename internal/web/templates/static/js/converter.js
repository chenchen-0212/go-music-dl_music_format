(function () {
  "use strict";

  const ROOT = window.API_ROOT || "/music";
  const LOCAL_TOOLS = window.MUSIC_DL_LOCAL_TOOLS === true;
  const STATUS_TEXT = {
    pending: "等待中",
    running: "转换中",
    success: "已完成",
    failed: "转换失败",
    cancelled: "已取消",
  };
  const tasks = new Map();
  const selectedFiles = new Map();
  let eventsBound = false;

  function elements() {
    return {
      modal: document.getElementById("audioConverterModal"),
      hint: document.getElementById("converterHint"),
      count: document.getElementById("converterSelectedCount"),
      paths: document.getElementById("converterPaths"),
      bitrate: document.getElementById("converterBitrate"),
      outputDir: document.getElementById("converterOutputDir"),
      conflict: document.getElementById("converterConflict"),
      start: document.getElementById("converterStartButton"),
      cancelAll: document.getElementById("converterCancelAllButton"),
      list: document.getElementById("converterTaskList"),
      pickFiles: document.getElementById("converterPickFilesButton"),
      pickFolder: document.getElementById("converterPickFolderButton"),
      pickOutput: document.getElementById("converterPickOutputButton"),
    };
  }

  function setHint(message, isError) {
    const el = elements().hint;
    if (!el) return;
    el.textContent = message || "";
    el.classList.toggle("error", !!isError);
  }

  function showToastIfAvailable(title, message, type) {
    if (typeof window.showToast === "function") {
      window.showToast(title, message, type || "error");
    } else {
      setHint(message || title, true);
    }
  }

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, options);
    const payload = await response.json().catch(() => null);
    if (!response.ok) {
      const error = new Error((payload && payload.error) || `请求失败（${response.status}）`);
      error.cancelled = !!(payload && payload.cancelled);
      throw error;
    }
    return payload;
  }

  function addFiles(paths) {
    let added = 0;
    for (const rawPath of Array.isArray(paths) ? paths : []) {
      const path = String(rawPath || "").trim();
      if (!path || selectedFiles.has(path)) continue;
      selectedFiles.set(path, true);
      added++;
    }
    updateSelectedCount();
    if (added > 0) setHint(`已添加 ${added} 个音频文件`);
    else setHint("没有新增支持的音频文件", true);
    return added;
  }

  function addPathText() {
    const el = elements();
    const lines = String(el.paths ? el.paths.value : "").split(/\r?\n|,|;/);
    const cleaned = lines.map(line => line.trim().replace(/^["']+|["']+$/g, "")).filter(Boolean);
    addFiles(cleaned);
  }

  async function pickAudioFiles() {
    try {
      setHint("请选择音频文件...");
      const result = await requestJSON(`${ROOT}/converter/picker/files`);
      addFiles(result.files || []);
    } catch (error) {
      if (!error.cancelled) showToastIfAvailable("添加文件失败", error.message);
      else setHint("");
    }
  }

  async function pickAudioFolder() {
    try {
      setHint("正在读取文件夹...");
      const picked = await requestJSON(`${ROOT}/converter/picker/folder`);
      const dir = picked.dir || "";
      if (!dir) return;
      elements().outputDir.value = elements().outputDir.value || "";
      const result = await requestJSON(`${ROOT}/converter/files/from-folder`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ dir }),
      });
      addFiles(result.files || []);
    } catch (error) {
      if (!error.cancelled) showToastIfAvailable("添加文件夹失败", error.message);
      else setHint("");
    }
  }

  async function pickOutputDirectory() {
    try {
      setHint("请选择输出目录...");
      const result = await requestJSON(`${ROOT}/converter/picker/folder`);
      if (result.dir) elements().outputDir.value = result.dir;
      setHint("");
    } catch (error) {
      if (!error.cancelled) showToastIfAvailable("选择目录失败", error.message);
      else setHint("");
    }
  }

  function updateSelectedCount() {
    const el = elements();
    if (!el.count) return;
    el.count.textContent = `待转换 ${selectedFiles.size} 个`;
    if (el.start) el.start.disabled = selectedFiles.size === 0;
  }

  async function startConversion() {
    if (selectedFiles.size === 0) return;
    const el = elements();
    // A textarea change can fire immediately before the Start button click.
    addPathText();
    const payload = {
      files: [...selectedFiles.keys()],
      format: "mp3",
      bitrate: el.bitrate.value || "320k",
      outputDir: String(el.outputDir.value || "").trim(),
      conflictPolicy: el.conflict.value || "rename",
    };
    try {
      setHint("正在创建转换任务...");
      el.start.disabled = true;
      const result = await requestJSON(`${ROOT}/converter/tasks`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const created = result.tasks || [];
      created.forEach(task => upsertTask(task));
      selectedFiles.clear();
      if (el.paths) el.paths.value = "";
      updateSelectedCount();
      refreshTasks();
      setHint(`已创建 ${created.length} 个转换任务`);
    } catch (error) {
      updateSelectedCount();
      showToastIfAvailable("创建任务失败", error.message);
    }
  }

  function taskAction(task) {
    if (task.status === "pending" || task.status === "running") {
      return `<button type="button" class="btn-pill btn-pill-danger" onclick="cancelConverterTask('${task.id}')">取消</button>`;
    }
    if (task.status === "failed" || task.status === "cancelled") {
      return `<button type="button" class="btn-pill btn-pill-dl" onclick="retryConverterTask('${task.id}')">重试</button>`;
    }
    return `<span class="converter-task-status-text">${task.skipped ? "已跳过" : ""}</span>`;
  }

  function renderTask(task) {
    const escapedName = escapeHTML(task.input_name || task.input || "");
    const escapedOutput = escapeHTML(task.output || "");
    const escapedError = escapeHTML(task.error || "");
    const statusClass = task.status === "success" ? "success" : task.status;
    const percent = Math.max(0, Math.min(100, Number(task.progress) || 0));
    return `
      <div class="converter-task" id="converter-${task.id}">
        <div class="converter-task-name">
          ${escapedName}
          <span class="converter-task-output">${escapedOutput}</span>
        </div>
        <div>
          <div class="converter-task-state converter-status-${statusClass}">
            ${STATUS_TEXT[task.status] || task.status}${percent > 0 ? ` ${Math.floor(percent)}%` : ""}
          </div>
          <div class="converter-progress-track"><div class="converter-progress-bar" style="width:${percent}%"></div></div>
          ${escapedError ? `<span class="converter-task-error">${escapedError}</span>` : ""}
        </div>
        <div class="converter-task-side">${taskAction(task)}</div>
      </div>`;
  }

  function upsertTask(task) {
    if (!task || !task.id) return;
    tasks.set(task.id, task);
    const list = elements().list;
    if (!list) return;
    list.querySelector(".converter-empty")?.remove();
    const current = document.getElementById(`converter-${CSS.escape(task.id)}`);
    if (current) {
      current.outerHTML = renderTask(task);
      return;
    }
    list.insertAdjacentHTML("beforeend", renderTask(task));
  }

  async function refreshTasks() {
    try {
      const rows = await requestJSON(`${ROOT}/converter/tasks`);
      const list = elements().list;
      if (!Array.isArray(rows)) return;
      if (list) list.innerHTML = "";
      tasks.clear();
      rows.forEach(upsertTask);
      if (!rows.length && list) {
        list.innerHTML = '<div class="converter-empty">暂无转换任务</div>';
      }
    } catch (error) {
      showToastIfAvailable("加载任务失败", error.message);
    }
  }

  async function actionRequest(id, action) {
    try {
      const task = await requestJSON(`${ROOT}/converter/tasks/${encodeURIComponent(id)}/${action}`, { method: "POST" });
      upsertTask(task);
    } catch (error) {
      showToastIfAvailable("操作失败", error.message);
    }
  }

  async function cancelAllTasks() {
    const active = [...tasks.values()].filter(task => task.status === "pending" || task.status === "running");
    for (const task of active) {
      // Cancel runs sequentially to avoid issuing hundreds of competing requests.
      await actionRequest(task.id, "cancel");
    }
  }

  function escapeHTML(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  window.openAudioConverter = async function () {
    const el = elements();
    if (el.modal) el.modal.style.display = "flex";
    [el.pickFiles, el.pickFolder, el.pickOutput].forEach(button => {
      button.style.display = LOCAL_TOOLS ? "" : "none";
    });
    bindEvents();
    updateSelectedCount();
    await refreshTasks();
    connectEvents();
  };

  window.closeAudioConverter = function () {
    const modal = elements().modal;
    if (modal) modal.style.display = "none";
  };

  window.cancelConverterTask = id => actionRequest(id, "cancel");
  window.retryConverterTask = id => actionRequest(id, "retry");

  function connectEvents() {
    if (eventsBound || typeof EventSource === "undefined") return;
    eventsBound = true;
    const source = new EventSource(`${ROOT}/converter/events`);
    source.addEventListener("task", event => {
      try {
        upsertTask(JSON.parse(event.data));
      } catch (_) {}
    });
    source.onerror = () => setTimeout(() => {
      if (elements().modal?.style.display !== "none") {
        eventsBound = false;
        connectEvents();
      }
    }, 3000);
  }

  function bindEvents() {
    const el = elements();
    if (!el.start || el.start.dataset.bound === "1") return;
    el.start.dataset.bound = "1";
    el.paths?.addEventListener("change", addPathText);
    el.pickFiles?.addEventListener("click", pickAudioFiles);
    el.pickFolder?.addEventListener("click", pickAudioFolder);
    el.pickOutput?.addEventListener("click", pickOutputDirectory);
    el.start?.addEventListener("click", startConversion);
    el.cancelAll?.addEventListener("click", cancelAllTasks);
  }
})();
