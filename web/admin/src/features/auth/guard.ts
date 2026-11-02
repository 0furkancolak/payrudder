import { redirect } from "@tanstack/vue-router";
import { returnTo } from "@/features/product/lib";
import { isOperator, isPerson } from "@/shared/lib/session";

/**
 * beforeLoad for every route of the operator console. There is no operator sign-in of its own: an operator is a person who signed in the usual way and
 * whom the server recognises, so a visitor is sent to the sign-in page (which brings them back to the page they asked for, as `requirePerson` does) and a
 * person who is not an operator back to their home. (The server decides on every request; this only keeps the page from showing a console that would answer 403.)
 */
export function requireOperator(ctx?: { location?: { href?: string } }): void {
  if (!isPerson.value) {
    const next = returnTo(ctx?.location?.href);
    throw redirect({ to: "/signin", ...(next ? { search: { next } } : {}) } as never);
  }
  if (!isOperator.value) throw redirect({ to: "/app" });
}
