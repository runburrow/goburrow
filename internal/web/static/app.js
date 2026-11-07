// Burrow. Progressive enhancement only; navigation and forms work without JS.
(() => {
  "use strict";
  document.documentElement.classList.add("js");

  document.addEventListener("click", async (event) => {
    const opener = event.target.closest("[data-open-shortcuts]");
    if (opener) { openShortcuts(); return; }
    const button = event.target.closest("[data-copy]");
    if (!button) return;
    let element;
    try { element = document.querySelector(button.dataset.copy); } catch { return; }
    if (!element) return;
    const label = button.textContent;
    try {
      await navigator.clipboard.writeText(element.textContent.trim());
      button.textContent = "Copied";
    } catch {
      const range = document.createRange();
      range.selectNodeContents(element);
      const selection = window.getSelection();
      if (selection) { selection.removeAllRanges(); selection.addRange(range); }
      button.textContent = "Selected, press Ctrl+C";
    }
    button.setAttribute("aria-live", "polite");
    window.setTimeout(() => { button.textContent = label; }, 2000);
  });

  // The fox burrows while issues load. A changed search waits for one short
  // dive-and-scrabble (0.65s) so the dig is seen; other filter changes never
  // wait, and the fox only appears if the server is slow. Reduced motion: no
  // animation, no wait.
  const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const startDigging = (caption, search) => {
    const label = document.querySelector(".dig-caption");
    if (label && caption) label.textContent = caption;
    document.documentElement.classList.toggle("search-dig", !!search);
    document.documentElement.classList.add("is-digging");
  };
  const resubmitting = new WeakSet();
  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || event.defaultPrevented || !form.matches("[data-fox-search]")) return;
    if (resubmitting.has(form)) { resubmitting.delete(form); return; }
    form.setAttribute("aria-busy", "true");
    const q = form.querySelector('input[name="q"]');
    const newSearch = q && q.value.trim() !== "" && q.value.trim() !== q.defaultValue.trim();
    if (!newSearch || reducedMotion() || !document.querySelector(".dig-overlay")) {
      window.setTimeout(() => startDigging("Digging for issues", false), 300);
      return;
    }
    startDigging("Digging for issues", true);
    event.preventDefault();
    const submitter = event.submitter;
    window.setTimeout(() => {
      resubmitting.add(form);
      if (typeof form.requestSubmit === "function") form.requestSubmit(submitter && submitter.isConnected ? submitter : undefined);
      else HTMLFormElement.prototype.submit.call(form);
    }, 650);
  });

  // Opening an issue never waits, but if the server is slow to answer the
  // fox starts digging for the details.
  document.addEventListener("click", (event) => {
    if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    if (event.target.closest(".issue-row .row-link")) window.setTimeout(() => startDigging("Digging up issue details", false), 200);
  });

  // A project waiting for its first event checks back every few seconds and
  // reloads once something has arrived.
  if (document.querySelector("[data-wait-first-event]")) {
    const started = Date.now();
    const poll = window.setInterval(async () => {
      if (Date.now() - started > 30 * 60 * 1000) { window.clearInterval(poll); return; }
      if (document.hidden) return;
      try {
        const res = await fetch(window.location.href, { cache: "no-store", credentials: "same-origin" });
        if (res.ok && !(await res.text()).includes("data-wait-first-event")) window.location.reload();
      } catch { /* offline for a moment; try again next tick */ }
    }, 5000);
  }

  // Bulk selection: the bar keeps its place (no layout jump), counts the
  // ticked issues, and only enables its buttons once something is ticked.
  const syncSelection = (form) => {
    const boxes = [...form.querySelectorAll('input[name="id"]')];
    const picked = boxes.filter((b) => b.checked).length;
    const bar = form.querySelector(".bulk-bar");
    if (bar) {
      bar.classList.toggle("has-selection", picked > 0);
      const count = bar.querySelector(".bulk-count");
      if (count) count.textContent = picked ? `${picked} selected` : "Tick issues to change several at once.";
      bar.querySelectorAll("button").forEach((btn) => { btn.disabled = picked === 0; });
    }
    const all = form.querySelector("[data-select-all]");
    if (all) {
      all.checked = picked > 0 && picked === boxes.length;
      all.indeterminate = picked > 0 && picked < boxes.length;
    }
  };

  document.addEventListener("change", (event) => {
    const target = event.target;
    if (target.matches("[data-select-all]")) {
      target.form.querySelectorAll('input[name="id"]').forEach((b) => { b.checked = target.checked; });
    }
    if (target.form && target.form.querySelector(".bulk-bar")) syncSelection(target.form);
    if (target.matches("[data-autosubmit]") && target.form && typeof target.form.requestSubmit === "function") {
      target.form.requestSubmit();
    }
  });

  // A page restored from the back/forward cache should not still be digging.
  window.addEventListener("pageshow", () => {
    document.documentElement.classList.remove("is-digging");
    document.querySelectorAll("[aria-busy]").forEach((el) => el.removeAttribute("aria-busy"));
    document.documentElement.classList.remove("search-dig");
    document.querySelectorAll(".issue-list").forEach(syncSelection);
  });

  // Keyboard shortcuts. Never while typing, and never with modifier keys,
  // so browser and screen-reader shortcuts keep working.
  function openShortcuts() {
    const dialog = document.getElementById("shortcuts");
    if (dialog && !dialog.open && typeof dialog.showModal === "function") dialog.showModal();
  }
  const rows = () => [...document.querySelectorAll(".issue-row .row-link")];
  const press = (selector) => { const el = document.querySelector(selector); if (el) el.click(); return !!el; };

  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey) return;
    const target = event.target;
    if (target instanceof HTMLElement && (target.isContentEditable || target.closest("input:not([type=checkbox]):not([type=radio]), select, textarea, dialog"))) return;
    let handled = true;
    switch (event.key) {
      case "?":
        openShortcuts();
        break;
      case "/": {
        const search = document.getElementById("issue-search");
        if (search) search.focus(); else handled = false;
        break;
      }
      case "j":
      case "k": {
        const links = rows();
        if (!links.length) { handled = false; break; }
        const current = links.indexOf(document.activeElement);
        const step = event.key === "j" ? 1 : -1;
        const next = current < 0 ? (step > 0 ? 0 : links.length - 1) : Math.min(links.length - 1, Math.max(0, current + step));
        links[next].focus();
        links[next].closest(".issue-row").scrollIntoView({ block: "nearest" });
        break;
      }
      case "x": {
        const row = document.activeElement && document.activeElement.closest(".issue-row");
        const box = row && row.querySelector('input[name="id"]');
        if (box) { box.checked = !box.checked; syncSelection(box.form); } else handled = false;
        break;
      }
      case "r":
      case "i": {
        // On the list, act on the ticked issues; on an issue page, on that issue.
        const value = event.key === "r" ? "resolved" : "ignored";
        const bulk = document.querySelector(`.bulk-bar button[value="${value}"]:not(:disabled)`);
        handled = bulk ? (bulk.click(), true) : press(`[data-shortcut="${event.key}"]`);
        break;
      }
      case "u":
        handled = press('[data-shortcut="u"]');
        break;
      default:
        handled = false;
    }
    if (handled) event.preventDefault();
  });
})();
