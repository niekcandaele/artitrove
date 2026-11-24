package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// filterScript (run after app.js has loaded) drives the index filter and records what is visible after each step in <pre id=out>.
const filterScript = `
window.addEventListener("load", () => {
const vis = () => [...document.querySelectorAll(".list li")].filter(li => !li.hidden).map(li => li.querySelector(".t").textContent).join(",");
const out = [];
const q = document.querySelector(".search");
out.push("start=" + vis());
q.value = "bra"; q.dispatchEvent(new Event("input"));
out.push("title=" + vis());
q.value = "APPLICATION/JSON"; q.dispatchEvent(new Event("input"));
out.push("type=" + vis());
q.value = "zzz"; q.dispatchEvent(new Event("input"));
out.push("none=" + vis() + "|empty=" + !document.getElementById("empty").hidden);
q.value = ""; q.dispatchEvent(new Event("input"));
document.querySelector('.chip[data-kind="markdown"]').click();
out.push("chip=" + vis());
q.value = "alp"; q.dispatchEvent(new Event("input"));
out.push("chip+title=" + vis());
document.querySelector('.chip[data-kind=""]').click();
out.push("all=" + vis());
document.body.insertAdjacentHTML("beforeend", "<pre id=out>" + out.join("\n") + "</pre>");
});
`

func TestIndexFilterHidesRows(t *testing.T) {
	t.Parallel()
	chrome, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("no headless Chrome available")
	}
	s := newServer(t, "ARTITROVE_INDEX=true")
	s.upload(t, []byte("a"), "text/plain", "alpha")
	s.upload(t, []byte("# b"), "text/markdown", "bravo")
	s.upload(t, []byte("{}"), "application/json", "charlie")
	_, page := s.get(t, "/", basic("", testToken)...)

	// The page CSP forbids inline script, so serve a copy that loads the driver as a same-origin file.
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Replace(string(page), "</body>", `<script src="/driver.js"></script></body>`, 1))
	})
	mux.HandleFunc("/driver.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = io.WriteString(w, filterScript)
	})
	mux.Handle("/static/", httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: s.addr}))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, //nolint:gosec // chrome comes from LookPath
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=5000", "--dump-dom", ts.URL+"/")
	dom, err := cmd.Output()
	if err != nil {
		t.Skipf("headless Chrome did not run: %v", err)
	}
	for _, want := range []string{
		"start=charlie,bravo,alpha",
		"title=bravo\n",
		"type=charlie\n",
		"none=|empty=true",
		"chip=bravo\n",
		"chip+title=\n",
		"all=alpha</pre>",
	} {
		if !strings.Contains(string(dom), want) {
			t.Errorf("filter output lacks %q in:\n%s", want, outBlock(string(dom)))
		}
	}
}

func outBlock(dom string) string {
	_, rest, _ := strings.Cut(dom, `<pre id="out">`)
	block, _, _ := strings.Cut(rest, "</pre>")
	return block
}
