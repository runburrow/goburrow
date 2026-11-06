// Burrow / Night Tape. Progressive enhancement; navigation and forms need no JS.
(() => {
  "use strict";
  document.documentElement.classList.add("js");
  const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const digging = new WeakSet();
  const resubmitting = new WeakSet();

  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-copy]");
    if (!button) return;
    let element;
    try { element = document.querySelector(button.dataset.copy); } catch { return; }
    if (!element) return;
    try {
      await navigator.clipboard.writeText(element.textContent.trim());
      const oldText = button.textContent;
      button.textContent = "Copied!";
      button.setAttribute("aria-live", "polite");
      window.setTimeout(() => { button.textContent = oldText; }, 1500);
    } catch {
      const range = document.createRange();
      range.selectNodeContents(element);
      const selection = window.getSelection();
      if (selection) { selection.removeAllRanges(); selection.addRange(range); }
      button.textContent = "Select & copy";
    }
  });

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    if (resubmitting.has(form)) { resubmitting.delete(form); return; }
    if (event.defaultPrevented) return;
    if (form.dataset.confirm && !window.confirm(form.dataset.confirm)) {
      event.preventDefault();
      return;
    }
    // Only search/filter GET forms with a fox pause briefly. No mutation is delayed.
    if (form.method.toLowerCase() !== "get" || !form.matches("[data-fox-search], .search-form, .filters") || !form.querySelector(".search-fox .fox") || reducedMotion()) return;
    if (typeof form.requestSubmit !== "function") return;
    if (digging.has(form)) { event.preventDefault(); return; }
    const submitter = event.submitter;
    let finished = false;
    const finish = () => {
      if (finished) return;
      finished = true;
      form.removeEventListener("animationend", onEnd);
      form.classList.remove("is-digging");
      form.removeAttribute("aria-busy");
      digging.delete(form);
      resubmitting.add(form);
      try {
        form.requestSubmit(submitter && submitter.isConnected ? submitter : undefined);
      } catch {
        // Native prototype avoids a control named "submit" shadowing the method.
        HTMLFormElement.prototype.submit.call(form);
      }
    };
    const onEnd = (animation) => { if (animation.animationName === "fox-dig") finish(); };
    // Arm the fail-safe before cancelling the normal submit: missing CSS cannot trap it.
    window.setTimeout(finish, 850);
    form.addEventListener("animationend", onEnd);
    digging.add(form);
    form.setAttribute("aria-busy", "true");
    form.classList.add("is-digging");
    event.preventDefault();
  });

  document.addEventListener("change", (event) => {
    if (!event.target.matches("[data-autosubmit]")) return;
    const form = event.target.form;
    if (form && typeof form.requestSubmit === "function") form.requestSubmit();
    // Old browsers retain the normal Filter button; never bypass validation.
  });
  // Restore the visible pose when a browser returns to a cached search page.
  window.addEventListener("pageshow", () => {
    document.querySelectorAll(".is-digging").forEach((form) => {
      form.classList.remove("is-digging"); form.removeAttribute("aria-busy");
    });
  });
})();
