// Starts Scalar on /api/v1/docs. A separate file, not an inline script,
// so the page keeps a strict script-src 'self'. The mount point is not
// called "api-reference" and has no data-url/data-spec-url, because the
// standalone build auto-mounts those with its default config (and fonts).
(function () {
  "use strict";
  var el = document.getElementById("burrow-api-docs");
  if (!el || !window.Scalar) return;
  var dark = window.matchMedia("(prefers-color-scheme: dark)");
  var config = {
    url: el.getAttribute("data-openapi"),
    theme: "none",               // colours come from docs.css
    withDefaultFonts: false,     // no fonts.scalar.com; IBM Plex Mono is local
    layout: "modern",
    forceDarkModeState: dark.matches ? "dark" : "light", // follow the system, like the rest of Burrow
    hideDarkModeToggle: true,
    // "Try it" sends Authorization: Bearer <token>, typed into the
    // auth box. Kept in memory only, never in localStorage.
    authentication: { preferredSecurityScheme: "bearerAuth" },
    persistAuth: false,
    hideClientButton: false,
    showDeveloperTools: "never",
    agent: { disabled: true },
    telemetry: false,
    documentDownloadType: "json",
    defaultOpenAllTags: true,
    metaData: { title: "API docs · Burrow" }
  };
  window.Scalar.createApiReference(el, config);
})();
