import { redirect } from "@tanstack/vue-router";
import { currentToken } from "@/shared/lib/session";

/** beforeLoad for every route that needs an operator: a visitor without a token is sent to the sign-in page. */
export function requireOperator(): void {
  if (currentToken() === null) throw redirect({ to: "/" });
}
