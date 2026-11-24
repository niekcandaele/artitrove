(function () {
  "use strict";
  var canCopy = navigator.clipboard && window.isSecureContext;

  function flash(btn, el, text) {
    var old = el.textContent;
    el.textContent = text;
    btn.classList.add("flash");
    setTimeout(function () { el.textContent = old; btn.classList.remove("flash"); }, 1400);
  }

  var share = document.querySelector("details.share");
  if (share) {
    var summary = share.querySelector("summary");
    var input = share.querySelector("input");
    summary.addEventListener("click", function (e) {
      if (!canCopy) {
        setTimeout(function () { if (share.open) { input.focus(); input.select(); } });
        return;
      }
      e.preventDefault();
      navigator.clipboard.writeText(summary.dataset.url).then(function () {
        flash(summary, summary.querySelector(".txt"), "Copied");
      }, function () {
        share.open = true;
        input.focus();
        input.select();
      });
    });
  }

  var copyCurl = document.querySelector(".copy-curl");
  if (copyCurl) {
    var curl = document.getElementById("curl");
    copyCurl.hidden = false;
    copyCurl.addEventListener("click", function () {
      var select = function () {
        var range = document.createRange();
        range.selectNodeContents(curl);
        var sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
      };
      if (!canCopy) { select(); return; }
      navigator.clipboard.writeText(curl.textContent).then(function () {
        flash(copyCurl, copyCurl, "Copied");
      }, select);
    });
  }

  var filters = document.querySelector(".filters");
  if (filters) {
    filters.hidden = false;
    var rows = Array.prototype.slice.call(document.querySelectorAll(".list li"));
    var chips = Array.prototype.slice.call(filters.querySelectorAll(".chip"));
    var search = filters.querySelector(".search");
    var count = document.getElementById("count");
    var empty = document.getElementById("empty");
    var kind = "";
    var noun = function (n) { return n === 1 ? " artifact" : " artifacts"; };
    var apply = function () {
      var needle = search.value.trim().toLowerCase();
      var shown = 0;
      rows.forEach(function (li) {
        var text = (li.querySelector(".t").textContent + " " + li.querySelector(".type").textContent).toLowerCase();
        li.hidden = !(text.indexOf(needle) >= 0 && (!kind || li.dataset.kind === kind));
        if (!li.hidden) { shown++; }
      });
      count.textContent = (shown === rows.length ? shown : shown + " of " + rows.length) + noun(rows.length);
      empty.hidden = shown > 0;
    };
    search.addEventListener("input", apply);
    chips.forEach(function (chip) {
      chip.addEventListener("click", function () {
        kind = chip.dataset.kind;
        chips.forEach(function (c) { c.setAttribute("aria-pressed", String(c === chip)); });
        apply();
      });
    });
  }

  var rel = typeof Intl !== "undefined" && Intl.RelativeTimeFormat ? new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }) : null;
  document.querySelectorAll("time[datetime]").forEach(function (t) {
    var d = new Date(t.getAttribute("datetime"));
    t.title = t.textContent;
    if (t.classList.contains("ago")) {
      var s = (d - Date.now()) / 1000;
      var units = [["day", 86400, 7], ["hour", 3600, 24], ["minute", 60, 60]];
      if (rel && -s < 7 * 86400) {
        for (var i = 0; i < units.length; i++) {
          if (-s >= units[i][1]) { t.textContent = rel.format(Math.round(s / units[i][1]), units[i][0]); return; }
        }
        t.textContent = rel.format(0, "second");
      } else {
        t.textContent = d.toLocaleDateString(undefined, { dateStyle: "medium" });
      }
    } else {
      t.textContent = d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
    }
  });
})();
