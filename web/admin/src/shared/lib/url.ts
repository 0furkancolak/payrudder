/** A link the gateway returned relative ("/i/abc") becomes absolute against the console's origin. */
export function absoluteUrl(url: string | undefined, origin: string): string {
  if (!url) return "";
  return url.startsWith("/") ? origin + url : url;
}
