// Mailgrid's page setup. Loaded after htmx, its SSE extension, and the
// HyperChi directives runtime.

// Every htmx request carries the CSRF token as an X-CSRF-Token header, read
// from the csrf_token cookie the middleware sets (it is not HttpOnly for
// this). One listener covers buttons such as Remove and Cancel that send no
// form, so no element needs its own token. Forms also render the token as a
// csrf_token field, which is what a post without JavaScript sends.
function csrfToken() {
  for (const part of document.cookie.split(";")) {
    const [name, ...rest] = part.trim().split("=");
    if (name === "csrf_token") return decodeURIComponent(rest.join("="));
  }
  return "";
}

document.addEventListener("htmx:config:request", (event) => {
  const token = csrfToken();
  if (token) event.detail.ctx.request.headers["X-CSRF-Token"] = token;
});

// Upstream htmx issue, not a HyperChi one: in the htmx 4.0.0 SSE extension
// (hx-sse.js, served unchanged by the framework), cleanup() aborts the
// connection and leaves the abort as an unhandled promise rejection ("signal
// is aborted without reason") when hx-sse:close="done" closes a stream.
// Closing is the intended end of a finished send-out, so that one rejection
// is silenced. Any other error still reaches the console. Remove this once an
// htmx release catches it.
window.addEventListener("unhandledrejection", (event) => {
  const reason = event.reason;
  if (reason?.name === "AbortError" && String(reason.stack).includes("hx-sse.js")) {
    event.preventDefault();
  }
});

HyperChi.start();
