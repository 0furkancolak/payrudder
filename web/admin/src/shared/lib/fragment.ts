/**
 * The token of a mailed link (/verify#token=..., /reset#token=..., /invite#token=...). It travels in the URL fragment, which a browser never sends to a
 * server, so it is in no access log and no Referer. This reads it once and removes it from the address bar and the history entry; it returns "" when
 * the link carries none.
 */
export function takeFragmentToken(): string {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ""));
  const token = params.get("token") ?? "";
  if (window.location.hash !== "") {
    try {
      window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);
    } catch {
      // A sandboxed frame may refuse; the token is then still in the address bar, which is where the person put it.
    }
  }
  return token.trim();
}
