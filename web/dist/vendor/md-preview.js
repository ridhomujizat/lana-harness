/* Workbench Markdown preview: Mermaid diagrams (owner, 2026-11-22).

   The daemon renders a Markdown file to a page (internal/workbench/preview)
   and adds this script. ```mermaid blocks arrive as
   <pre><code class="language-mermaid">; each becomes a diagram with zoom
   (− / + / fit, ⌘ or Ctrl + wheel, pinch) and plain scrolling once it is
   larger than its box; no drag-to-pan, so its text can be selected and
   copied, and touch scrolling stays native on a phone (owner, 2026-11-22).
   The % button fits the diagram to the width. Mermaid itself (about
   1 MB gzipped) loads only when the page has a diagram. The page lives in a
   sandboxed iframe with an opaque origin, so nothing here uses storage. */
(function () {
  "use strict";

  var blocks = Array.prototype.slice.call(document.querySelectorAll("pre > code.language-mermaid"));
  if (!blocks.length) return;

  var MIN = 0.25;
  var MAX = 4;
  var STEP = 1.2;

  function dark() {
    var t = document.documentElement.getAttribute("data-theme");
    if (t === "dark") return true;
    if (t === "light") return false;
    return !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  }

  var css =
    ".mmd{margin:14px 0;border:1px solid var(--border,#dad7d0);border-radius:10px;background:var(--code-bg,#f6f5f2);overflow:hidden}" +
    ".mmd-bar{display:flex;align-items:center;gap:2px;padding:4px 6px;border-bottom:1px solid var(--border,#dad7d0);font:12px/1 system-ui,-apple-system,sans-serif;color:var(--text-2,#57544e)}" +
    ".mmd-bar .mmd-label{margin-right:auto;padding-left:4px;letter-spacing:.02em}" +
    ".mmd-bar button{display:inline-grid;place-items:center;min-width:26px;height:24px;padding:0 6px;border:0;border-radius:6px;background:transparent;color:inherit;font:inherit;cursor:pointer}" +
    ".mmd-bar button:hover{background:var(--hl,rgba(0,0,0,.06));color:var(--text,#1d1c1a)}" +
    ".mmd-bar .mmd-pct{min-width:46px;font-variant-numeric:tabular-nums}" +
    ".mmd-view{max-height:72vh;overflow:auto;overscroll-behavior:contain;padding:14px}" +
    ".mmd-stage svg text,.mmd-stage svg foreignObject{-webkit-user-select:text;user-select:text}" +
    "@media (pointer:coarse){.mmd-bar button{min-width:36px;height:32px}.mmd-view{max-height:60vh;padding:10px}}" +
    ".mmd-stage{display:flex;justify-content:center;min-width:min-content}" +
    ".mmd-view.is-wide .mmd-stage{justify-content:flex-start}" +
    ".mmd-stage svg{display:block;max-width:none!important;flex:none}" +
    ".mmd-err{margin:0;padding:10px 12px;color:#c2453b;font:12px/1.5 system-ui,sans-serif}" +
    ".mmd-wait{padding:10px 12px;color:var(--text-4,#8c8880);font:12px system-ui,sans-serif}";
  var style = document.createElement("style");
  style.textContent = css;
  document.head.appendChild(style);

  function button(label, title, onClick) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    b.title = title;
    b.setAttribute("aria-label", title);
    b.addEventListener("click", onClick);
    return b;
  }

  /* One diagram box per block, with a "Rendering…" note until Mermaid runs. */
  var boxes = blocks.map(function (code) {
    var pre = code.parentNode;
    var fig = document.createElement("figure");
    fig.className = "mmd";
    fig.style.margin = "14px 0";
    var wait = document.createElement("div");
    wait.className = "mmd-wait";
    wait.textContent = "Rendering diagram…";
    fig.appendChild(wait);
    pre.parentNode.replaceChild(fig, pre);
    return { fig: fig, source: code.textContent || "", pre: pre };
  });

  function fail(box, message) {
    box.fig.textContent = "";
    var err = document.createElement("p");
    err.className = "mmd-err";
    err.textContent = "Mermaid: " + message;
    box.fig.appendChild(err);
    box.fig.appendChild(box.pre);
  }

  function mount(box, svgText) {
    var fig = box.fig;
    fig.textContent = "";
    var bar = document.createElement("div");
    bar.className = "mmd-bar";
    var label = document.createElement("span");
    label.className = "mmd-label";
    label.textContent = "Diagram";
    var view = document.createElement("div");
    view.className = "mmd-view";
    var stage = document.createElement("div");
    stage.className = "mmd-stage";
    stage.innerHTML = svgText;
    view.appendChild(stage);
    var svg = stage.querySelector("svg");
    if (!svg) {
      fail(box, "no diagram");
      return;
    }

    /* The diagram's own size, from its viewBox (Mermaid sets width 100% and
       a max-width, both dropped so the zoom sets the size). */
    var vb = svg.viewBox && svg.viewBox.baseVal;
    var w0 = vb && vb.width ? vb.width : svg.getBoundingClientRect().width || 600;
    var h0 = vb && vb.height ? vb.height : svg.getBoundingClientRect().height || 400;
    svg.removeAttribute("style");
    svg.removeAttribute("width");
    svg.removeAttribute("height");

    var scale = 1;
    var pct = button("100%", "Fit to width", function () { fit(); });
    pct.className = "mmd-pct";

    function apply(next, ax, ay) {
      next = Math.min(MAX, Math.max(MIN, next));
      /* Keep the point under (ax, ay), in view coordinates, where it is. */
      var px = ax === undefined ? view.clientWidth / 2 : ax;
      var py = ay === undefined ? view.clientHeight / 2 : ay;
      var cx = (view.scrollLeft + px) / scale;
      var cy = (view.scrollTop + py) / scale;
      scale = next;
      svg.setAttribute("width", String(Math.round(w0 * scale)));
      svg.setAttribute("height", String(Math.round(h0 * scale)));
      pct.textContent = Math.round(scale * 100) + "%";
      var wide = w0 * scale > view.clientWidth - 28;
      view.classList.toggle("is-wide", wide);
      view.scrollLeft = cx * scale - px;
      view.scrollTop = cy * scale - py;
    }

    function fit() {
      var room = Math.max(120, view.clientWidth - 28);
      apply(Math.min(1, room / w0));
      view.scrollLeft = 0;
      view.scrollTop = 0;
    }

    bar.appendChild(label);
    bar.appendChild(button("−", "Zoom out", function () { apply(scale / STEP); }));
    bar.appendChild(pct);
    bar.appendChild(button("+", "Zoom in", function () { apply(scale * STEP); }));
    fig.appendChild(bar);
    fig.appendChild(view);

    /* ⌘/Ctrl + wheel and trackpad pinch (a wheel event with ctrlKey) zoom
       around the pointer; a plain wheel scrolls. */
    view.addEventListener(
      "wheel",
      function (e) {
        if (!e.ctrlKey && !e.metaKey) return;
        e.preventDefault();
        var r = view.getBoundingClientRect();
        apply(scale * Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
      },
      { passive: false }
    );

    /* Safari's pinch. */
    var gestureStart = 1;
    view.addEventListener("gesturestart", function (e) {
      e.preventDefault();
      gestureStart = scale;
    });
    view.addEventListener("gesturechange", function (e) {
      e.preventDefault();
      apply(gestureStart * e.scale);
    });

    fit();
  }

  var script = document.createElement("script");
  script.src = "/assets/vendor/mermaid.min.js";
  script.onerror = function () {
    boxes.forEach(function (box) { fail(box, "could not load the renderer"); });
  };
  script.onload = function () {
    var mermaid = window.mermaid;
    if (!mermaid) {
      script.onerror();
      return;
    }
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: dark() ? "dark" : "default",
      fontFamily: 'Geist, system-ui, -apple-system, "Segoe UI", sans-serif',
    });
    /* One at a time: Mermaid's render is not reentrant. */
    var chain = Promise.resolve();
    boxes.forEach(function (box, i) {
      chain = chain.then(function () {
        var id = "mmd-" + i;
        /* Parse first: a failed render leaves Mermaid's own error picture
           at the end of the page. */
        return mermaid
          .parse(box.source)
          .then(function () { return mermaid.render(id, box.source); })
          .then(function (out) { mount(box, out.svg); })
          .catch(function (err) {
            ["d" + id, id].forEach(function (left) {
              var el = document.getElementById(left);
              if (el && !box.fig.contains(el)) el.remove();
            });
            fail(box, (err && err.message) || String(err));
          });
      });
    });
  };
  document.head.appendChild(script);
})();
