// Burrow: progressive enhancement only. Every page works without JS.
document.addEventListener("click", async (e) => {
  const btn = e.target.closest("[data-copy]");
  if (!btn) return;
  const el = document.querySelector(btn.dataset.copy);
  if (!el) return;
  try {
    await navigator.clipboard.writeText(el.textContent.trim());
    const old = btn.textContent;
    btn.textContent = "Copied!";
    setTimeout(() => (btn.textContent = old), 1500);
  } catch {
    const r = document.createRange();
    r.selectNodeContents(el);
    getSelection().removeAllRanges();
    getSelection().addRange(r);
  }
});

document.addEventListener("submit", (e) => {
  const msg = e.target.dataset.confirm;
  if (msg && !confirm(msg)) e.preventDefault();
});

document.addEventListener("change", (e) => {
  if (e.target.matches("[data-autosubmit]")) e.target.form.submit();
});
