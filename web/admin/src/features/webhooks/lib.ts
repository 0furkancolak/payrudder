/** Whether an endpoint URL is plain http (case and surrounding space do not matter). Anything else, valid or not, is not "insecure" here. */
export function isInsecureUrl(url: string | undefined): boolean {
  return /^\s*http:\/\//i.test(url ?? "");
}

/**
 * What the endpoint answered, for one line of a table: the answer's text with markup taken out (a 405 page of the receiver is a whole HTML document),
 * whitespace collapsed and cut at `max` characters. The full text goes in the cell's tooltip.
 */
export function plainAnswer(raw: string | undefined, max = 120): { text: string; full: string } {
  const full = (raw ?? "")
    .replace(/<(script|style)[\s\S]*?<\/\1>/gi, " ")
    .replace(/<!--[\s\S]*?-->/g, " ")
    .replace(/<[^>]*>/g, " ")
    .replace(/&nbsp;/g, " ")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
  return { text: full.length > max ? `${full.slice(0, max - 1)}…` : full, full };
}

/** A delivery's last answer is `HTTP 405: <body>` when the endpoint answered and a plain reason when it did not: the part after the status prefix. */
export function answerBody(lastError: string | undefined, lastStatus: string | number | undefined): string {
  const e = (lastError ?? "").trim();
  if (lastStatus === undefined || lastStatus === "") return e;
  return e.replace(new RegExp(`^HTTP\\s+${String(lastStatus)}\\s*:?\\s*`, "i"), "");
}
