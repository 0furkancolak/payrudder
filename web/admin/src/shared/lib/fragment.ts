/**
 * The token of a mailed link (/verify#token=..., /reset#token=..., /invite#token=...). It travels in the URL fragment, which a browser never sends to a
 * server, so it is in no access log and no Referer. This reads it once and removes it from the address bar and the history entry; it returns "" when
 * the link carries none.
 */
export function takeFragmentToken(): string {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ""));
  const token = params.get("token") ?? "";
  if (window.location.hash !== "") dropFragment();
  return token.trim();
}

/** Takes the fragment off the address bar and the history entry. */
function dropFragment(): void {
  try {
    window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);
  } catch {
    // A sandboxed frame may refuse; the token is then still in the address bar, which is where the person put it.
  }
}

/** The second step of a sign-in that the gateway handed to the page after Google: the challenge (qc_...) and when it ends (RFC 3339, or ""). */
export interface FragmentChallenge {
  challenge: string;
  expires: string;
}

/** Reads `#challenge=qc_...&expires=...` from a fragment; null when it holds no challenge of the right shape. */
export function parseChallengeFragment(hash: string): FragmentChallenge | null {
  const params = new URLSearchParams(hash.replace(/^#/, ""));
  const challenge = (params.get("challenge") ?? "").trim();
  if (!/^qc_\S+$/.test(challenge)) return null;
  return { challenge, expires: (params.get("expires") ?? "").trim() };
}

/**
 * The challenge of a sign-in with Google for a person who has two-factor sign-in on (`/signin/complete#challenge=...&expires=...` instead of
 * `#token=...`). Like the token it is read once and removed from the address bar; a fragment with no challenge is left alone for `takeFragmentToken`.
 */
export function takeFragmentChallenge(): FragmentChallenge | null {
  const found = parseChallengeFragment(window.location.hash);
  if (found) dropFragment();
  return found;
}
