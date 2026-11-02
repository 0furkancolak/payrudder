/** The address of a local stack's gateway, what the code starts with when nothing else is known. */
export const LOCAL_API_ADDRESS = "http://localhost:42800";

/**
 * Where the code on the Connect page points: the address the person typed (kept in this browser), else the deployment's own address (the PUBLIC_API_URL setting, served
 * to the page as /env.js), else a local stack. A typed address wins so a person who deliberately points the code elsewhere is not overruled.
 */
export function initialAddress(stored: string, configured: string | undefined): string {
  return stored.trim() || (configured ?? "").trim() || LOCAL_API_ADDRESS;
}
