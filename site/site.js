// Burrow landing page. Progressive enhancement: without JS the page shows a
// static sample issue list and plain code blocks.
(() => {
  "use strict";
  document.documentElement.classList.add("js");
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // Activity bars from data-bars (14 daily counts, oldest first).
  const drawBars = (el, counts) => {
    const peak = Math.max(1, ...counts);
    el.replaceChildren(...counts.map((n) => {
      const bar = document.createElement("span");
      bar.className = n ? "bar" : "bar zero";
      bar.style.height = n ? `${Math.max(8, Math.round((n / peak) * 100))}%` : "1px";
      return bar;
    }));
  };
  document.querySelectorAll("[data-bars]").forEach((el) => drawBars(el, el.dataset.bars.split(",").map(Number)));

  // A barcode for the spec label, drawn from the version string's bytes.
  const code = document.querySelector("[data-barcode]");
  if (code) {
    [..."burrow-1-binary-sqlite-mit"].forEach((ch, i) => {
      const bar = document.createElement("span");
      bar.style.height = `${30 + ((ch.charCodeAt(0) * 7 + i * 13) % 70)}%`;
      bar.style.width = ch.charCodeAt(0) % 3 === 0 ? "4px" : "2px";
      code.append(bar);
    });
  }

  // Copy buttons.
  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-copy]");
    if (!button) return;
    const source = document.querySelector(button.dataset.copy);
    if (!source) return;
    const label = button.textContent;
    try {
      await navigator.clipboard.writeText(source.textContent.trim());
      button.textContent = "Copied";
    } catch {
      const range = document.createRange();
      range.selectNodeContents(source);
      const selection = window.getSelection();
      selection.removeAllRanges(); selection.addRange(range);
      button.textContent = "Selected";
    }
    window.setTimeout(() => { button.textContent = label; }, 1800);
  });

  // SDK tabs (roving tabindex, arrow keys).
  const tabs = [...document.querySelectorAll(".sdk-tab")];
  const select = (tab) => {
    tabs.forEach((t) => {
      const on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
      document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
    });
  };
  tabs.forEach((tab, i) => {
    tab.addEventListener("click", () => select(tab));
    tab.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
      const next = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
      select(next); next.focus();
    });
  });

  // The live issue stream: sample events arrive and group into issues. Every
  // cycle also plays the regression story: the TypeError is resolved, then
  // happens again and reopens. Visitors can resolve it themselves too.
  const stream = document.querySelector("[data-stream]");
  if (!stream) return;
  const list = stream.querySelector("[data-issues]");
  const incoming = stream.querySelector("[data-incoming]");
  const target = stream.querySelector("[data-target]");
  const unresolved = stream.querySelector("[data-unresolved]");
  const pause = stream.querySelector("[data-pause]");
  const announce = stream.querySelector("[data-announce]");
  const row = (key) => list.querySelector(`[data-issue="${key}"]`);
  const typeError = { msg: "TypeError: Cannot read properties of undefined (reading 'total')", issue: "typeerror", title: "TypeError" };
  // One cycle. "resolve" marks the TypeError resolved unless the visitor already did.
  const script = [
    { msg: "TimeoutError: timed out after 4521ms", issue: "timeout", title: "TimeoutError" },
    { msg: "ZeroDivisionError: division by zero", issue: "zero", title: "ZeroDivisionError" },
    { resolve: true },
    { msg: "TimeoutError: timed out after 30012ms", issue: "timeout", title: "TimeoutError" },
    typeError,
    { msg: "KeyError: 'shipping_address'", issue: "keyerror", create: { level: "error", title: "KeyError: 'shipping_address'", culprit: "checkout/views.py in confirm" } },
    { msg: "ZeroDivisionError: division by zero", issue: "zero", title: "ZeroDivisionError" },
    { reset: true },
  ];
  let step = 0;
  let paused = reducedMotion;
  let held = false; // pointer or keyboard focus inside the stream
  let visitorResolved = false;
  let parked = null; // the row a new issue temporarily replaces, so the list keeps its height

  const countUnresolved = () => {
    unresolved.textContent = String(list.querySelectorAll(".issue:not(.is-resolved)").length);
  };
  const setPill = (el, html) => { const pill = el.querySelector("[data-pill]"); if (pill) pill.innerHTML = html; };
  const markResolved = (el) => {
    el.classList.add("is-resolved");
    setPill(el, '<span class="pill resolved">Resolved</span>');
    const button = el.querySelector("[data-resolve]");
    if (button) button.hidden = true;
    countUnresolved();
  };
  const bump = (el) => {
    const count = el.querySelector("[data-count]");
    count.textContent = String(Number(count.textContent) + 1);
    const bars = el.querySelector("[data-bars]");
    const counts = bars.dataset.bars.split(",").map(Number);
    counts[13] += 1;
    bars.dataset.bars = counts.join(",");
    drawBars(bars, counts);
    el.classList.remove("arrived"); void el.offsetWidth; el.classList.add("arrived");
    list.prepend(el);
  };
  const createIssue = (key, spec) => {
    const li = document.createElement("li");
    li.className = "issue is-new";
    li.dataset.issue = key;
    li.innerHTML = `<span class="level ${spec.level}">${spec.level[0].toUpperCase() + spec.level.slice(1)}</span>
      <span class="issue-main"><span class="issue-title"></span><span class="issue-culprit"></span></span>
      <span class="bars" aria-hidden="true" data-bars="0,0,0,0,0,0,0,0,0,0,0,0,0,0"></span>
      <span class="issue-count" data-count>0</span>
      <span class="issue-side"></span>`;
    li.querySelector(".issue-title").textContent = spec.title;
    li.querySelector(".issue-culprit").textContent = spec.culprit;
    parked = list.lastElementChild;
    parked.remove();
    list.prepend(li);
    return li;
  };
  const deliver = (ev) => {
    incoming.textContent = ev.msg;
    let el = row(ev.issue);
    if (!el && ev.create) {
      el = createIssue(ev.issue, ev.create);
      target.textContent = "new issue";
    } else {
      target.textContent = `grouped into ${ev.title}`;
    }
    if (el.classList.contains("is-resolved")) {
      el.classList.remove("is-resolved");
      setPill(el, '<span class="pill regression">Regression</span>');
      const button = el.querySelector("[data-resolve]");
      if (button) button.hidden = false;
      target.textContent = "reopened as a regression";
      if (visitorResolved) announce.textContent = "The TypeError happened again and reopened as a regression.";
      visitorResolved = false;
    }
    bump(el);
    countUnresolved();
  };
  const tick = () => {
    if (paused || held || document.hidden) return;
    const item = script[step % script.length];
    step += 1;
    if (item.resolve) {
      const el = row("typeerror");
      if (!el.classList.contains("is-resolved")) {
        markResolved(el);
        incoming.textContent = "Resolved: TypeError";
        target.textContent = "watch for it to come back";
      }
      return;
    }
    if (item.reset) {
      // Start the next cycle clean: drop the new issue, restore the parked row.
      row("keyerror")?.remove();
      if (parked) { list.append(parked); parked = null; }
      const el = row("typeerror");
      if (!el.classList.contains("is-resolved")) setPill(el, "");
      return;
    }
    deliver(item);
  };
  window.setInterval(tick, 2600);

  // Nothing moves under the pointer or keyboard focus.
  stream.addEventListener("pointerenter", () => { held = true; });
  stream.addEventListener("pointerleave", () => { held = false; });
  stream.addEventListener("focusin", () => { held = true; });
  stream.addEventListener("focusout", (e) => { if (!stream.contains(e.relatedTarget)) held = false; });

  stream.addEventListener("click", (event) => {
    const resolve = event.target.closest("[data-resolve]");
    if (resolve) {
      markResolved(resolve.closest(".issue"));
      visitorResolved = true;
      announce.textContent = "Resolved the TypeError. It will happen again in a few seconds.";
      pause.focus();
      // It happens again a few seconds later, whatever the loop is doing.
      window.setTimeout(() => { if (row("typeerror").classList.contains("is-resolved")) deliver(typeError); }, 3500);
    }
    if (event.target.closest("[data-pause]")) {
      paused = !paused;
      pause.textContent = paused ? "Play" : "Pause";
    }
  });
  if (paused) pause.textContent = "Play";
})();
