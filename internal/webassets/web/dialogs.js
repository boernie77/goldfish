// dialogs.js — App-eigene Dialog-Helfer (Ersatz fuer native alert/confirm/prompt).
//
// Wird VOR app.js geladen (gleiche window-Scope), damit alle Aufrufer in app.js
// die Funktionen verfuegbar haben. defer-Reihenfolge in index.html garantiert
// das Loading vor app.js.
//
// Ein einzelnes <dialog>-Element wird je nach Modus umgeschaltet.
// appAlert(msg)               — Promise<void>: nur OK-Button
// appConfirm(msg)             — Promise<boolean>: OK + Abbrechen
// appPrompt(msg, default)     — Promise<string|null>: Input + OK/Abbrechen
// showToast(msg, {kind, duration}) — unaufdringlicher Toast rechts unten
//
// Alle Fenster rendern sich im App-eigenen Stil, blockieren Focus auf den
// Dialog und schliessen bei Escape oder Backdrop-Klick. appDialogQueue
// serialisiert Aufrufe, damit zwei gleichzeitige Confirms sich nicht
// ueberlappen.
let appDialogQueue = Promise.resolve();
function appDialog({ title = "", body = "", input = null, showCancel = false, okLabel = "OK", cancelLabel = "Abbrechen", danger = false }) {
  appDialogQueue = appDialogQueue.then(() => new Promise(resolve => {
    const dlg = document.getElementById("appDialog");
    const tEl = document.getElementById("appDialogTitle");
    const bEl = document.getElementById("appDialogBody");
    const inputRow = document.getElementById("appDialogInputRow");
    const inputEl = document.getElementById("appDialogInput");
    const okBtn = document.getElementById("appDialogOk");
    const cancelBtn = document.getElementById("appDialogCancel");
    if (!dlg) { resolve(showCancel ? (input != null ? null : false) : undefined); return; }

    tEl.textContent = title || "";
    tEl.classList.toggle("hidden", !title);
    bEl.innerHTML = body ? String(body).replace(/\n/g, "<br>") : "";
    okBtn.textContent = okLabel;
    okBtn.classList.toggle("danger", !!danger);
    okBtn.classList.toggle("primary", !danger);
    cancelBtn.textContent = cancelLabel;
    cancelBtn.classList.toggle("hidden", !showCancel);

    if (input != null) {
      inputRow.classList.remove("hidden");
      inputEl.value = String(input);
      inputEl.type = "text";
    } else {
      inputRow.classList.add("hidden");
      inputEl.value = "";
    }

    const cleanup = () => {
      okBtn.removeEventListener("click", onOk);
      cancelBtn.removeEventListener("click", onCancel);
      dlg.removeEventListener("cancel", onCancel);
      dlg.removeEventListener("keydown", onKey);
      dlg.close();
    };
    const onOk = () => {
      const result = input != null ? inputEl.value : (showCancel ? true : undefined);
      cleanup();
      resolve(result);
    };
    const onCancel = (ev) => {
      if (ev) ev.preventDefault();
      cleanup();
      resolve(input != null ? null : (showCancel ? false : undefined));
    };
    const onKey = (e) => {
      if (e.key === "Enter" && input != null) {
        e.preventDefault();
        onOk();
      }
    };
    okBtn.addEventListener("click", onOk);
    cancelBtn.addEventListener("click", onCancel);
    dlg.addEventListener("cancel", onCancel);
    dlg.addEventListener("keydown", onKey);
    dlg.showModal();
    // Fokus nach dem showModal setzen
    setTimeout(() => { (input != null ? inputEl : okBtn).focus(); }, 0);
  }));
  return appDialogQueue;
}
function appAlert(msg, opts = {}) {
  return appDialog({ title: opts.title || "", body: msg, showCancel: false, okLabel: opts.okLabel || "OK" });
}

// showToast: unaufdringlicher Hinweis rechts unten fuer 3 s. Nicht modal.
// Nutzung: showToast("Ist schon in der Playlist") oder showToast(msg, {kind:"error"}).
function showToast(msg, opts = {}) {
  const kind = opts.kind || "info";
  let root = document.getElementById("toastRoot");
  if (!root) {
    root = document.createElement("div");
    root.id = "toastRoot";
    document.body.appendChild(root);
  }
  const t = document.createElement("div");
  t.className = `toast toast--${kind}`;
  t.textContent = msg;
  root.appendChild(t);
  // Reflow erzwingen, damit CSS-Transition greift.
  void t.offsetWidth;
  t.classList.add("toast--shown");
  setTimeout(() => {
    t.classList.remove("toast--shown");
    setTimeout(() => t.remove(), 300);
  }, opts.duration || 3000);
}
function appConfirm(msg, opts = {}) {
  return appDialog({
    title: opts.title || "",
    body: msg,
    showCancel: true,
    okLabel: opts.okLabel || "OK",
    cancelLabel: opts.cancelLabel || "Abbrechen",
    danger: !!opts.danger,
  });
}
function appPrompt(msg, defaultValue = "", opts = {}) {
  return appDialog({
    title: opts.title || "",
    body: msg,
    input: defaultValue == null ? "" : String(defaultValue),
    showCancel: true,
    okLabel: opts.okLabel || "OK",
  });
}
