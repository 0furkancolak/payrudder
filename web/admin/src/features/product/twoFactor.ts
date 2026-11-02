import type { components } from "@/shared/api/schema";

// Two-factor sign-in (plan 22, G9): the pure parts. The calls are in twoFactorApi.ts, the pages in SecurityPage.vue and TwoFactorStep.vue.

export type SecondFactorRequired = components["schemas"]["SecondFactorRequired"];
type SessionOpened = components["schemas"]["SessionOpened"];

/** Whether a sign-in answered with a challenge for the second step instead of a session. */
export function isChallenge(answer: SessionOpened | SecondFactorRequired): answer is SecondFactorRequired {
  return "second_factor_required" in answer && answer.second_factor_required === true;
}

/** The page every route but sign-out leads to while an organisation the person owns requires two-factor sign-in and they have none. */
export const SECURITY_PATH = "/app/security";

/** Pages that work signed in or out because the link in a mail is their credential: a person held on the security page may still follow one. */
const OPEN_PAGES = ["/signin/complete", "/forgot", "/reset", "/verify", "/invite"];

/**
 * Where a person who must set up two-factor sign-in is sent from `path`: the security page, from anywhere else. Nothing (null) on the security page itself
 * and on the pages a mailed link opens. The API refuses every other route with 403 `identity.two_factor.setup_required` anyway; this is the way out of it.
 */
export function setupRedirect(path: string, required: boolean): string | null {
  if (!required) return null;
  const clean = path.replace(/\/+$/, "") || "/";
  if (clean === SECURITY_PATH || OPEN_PAGES.includes(clean)) return null;
  return SECURITY_PATH;
}

/** At or below this many recovery codes left the page warns: a person who loses the phone then has little to get back in with. */
export const RECOVERY_CODES_LOW = 3;

export const RECOVERY_FILE_NAME = "payrudder-recovery-codes.txt";

/** The text of the file with the ten recovery codes: a header line with the address and the day, a note, and the codes numbered, one to a line. */
export function recoveryFileText(v: { title: string; note: string; codes: readonly string[] }): string {
  const width = String(v.codes.length).length;
  return [v.title, "", v.note, "", ...v.codes.map((c, i) => `${String(i + 1).padStart(width, " ")}. ${c}`), ""].join("\n");
}
