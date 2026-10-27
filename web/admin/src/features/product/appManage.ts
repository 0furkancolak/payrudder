/** Owners and admins rename an app; the server checks the role again. */
export function canRenameApp(role: string | undefined): boolean {
  return role === "owner" || role === "admin";
}

/** Only an owner deletes an app (an admin manages it, a viewer reads it). */
export function canDeleteApp(role: string | undefined): boolean {
  return role === "owner";
}

/** The name to send for a rename, or "" when there is nothing to save: blank, or the name the app already has. */
export function nameToSave(typed: string, current: string | undefined): string {
  const name = typed.trim();
  return name === "" || name === (current ?? "") ? "" : name;
}

/**
 * Whether the person typed the name of the app to confirm the delete: the whole name, spelled as it is (case and inner spaces count), with the spaces around
 * it forgiven. An app with no name loaded confirms nothing.
 */
export function confirmsDelete(typed: string, appName: string | undefined): boolean {
  const name = (appName ?? "").trim();
  return name !== "" && typed.trim() === name;
}
