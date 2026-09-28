// Where the user was headed before login sent them away.
//
// This exists for the OAuth consent screen. A client opens
// `/mcp-consent?request=…` in the browser; if that browser has no session yet,
// the root layout bounces to login, and without this the request id would be
// lost and the user would land on the dashboard wondering what happened. The
// authorization request is single-use and short-lived, so "log in and try the
// command again" is a real dead end rather than a minor annoyance.
//
// The stored value is only ever an *internal* path. Login is a natural target
// for an open-redirect attempt — "sign in and we'll send you onward" is exactly
// the shape of the trick — so a scheme, a host, or a protocol-relative path is
// rejected on the way in and again on the way out.

const KEY = "post_login_redirect";

/** The router-relative path (deploy base stripped) plus its query string. */
function routerPath(pathname: string, search: string): string {
  const base = import.meta.env.BASE_URL;
  let path = pathname;
  if (base && base !== "/" && path.startsWith(base)) {
    path = "/" + path.slice(base.length);
  }
  if (!path.startsWith("/")) path = "/" + path;
  return path + search;
}

/** An internal path and nothing else: no scheme, no host, no
 *  protocol-relative form. Backslashes are rejected too, because some browsers
 *  normalise "/\evil.test" into a protocol-relative URL. */
function isInternal(target: string): boolean {
  return (
    target.startsWith("/") &&
    !target.startsWith("//") &&
    !target.startsWith("/\\") &&
    !target.includes("\\")
  );
}

/** Remember a destination for the next successful login. Refuses anything that
 *  is not an internal path, and refuses login itself so signing in cannot loop. */
export function rememberReturnTo(pathname: string, search: string): void {
  const target = routerPath(pathname, search);
  if (!isInternal(target) || target.startsWith("/login")) return;
  try {
    sessionStorage.setItem(KEY, target);
  } catch {
    // Private mode or a blocked storage partition. Losing the return path is a
    // worse landing page, not a broken login.
  }
}

/** Consume the remembered destination. Single-use: a stale entry must not
 *  hijack an unrelated login later in the session. */
export function takeReturnTo(): string | null {
  try {
    const target = sessionStorage.getItem(KEY);
    sessionStorage.removeItem(KEY);
    return target && isInternal(target) ? target : null;
  } catch {
    return null;
  }
}

/** The browser href for a router-relative path, honouring the deploy base
 *  (production serves the SPA under `/dashboard/`). */
export function returnToHref(target: string): string {
  const base = import.meta.env.BASE_URL || "/";
  return base.replace(/\/$/, "") + target;
}
