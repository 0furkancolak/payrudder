import { redirect } from "@tanstack/vue-router";
import { isOperator } from "@/shared/lib/session";

/** beforeLoad for every route that needs an operator: a visitor without an operator token is sent to the operator's sign-in page. */
export function requireOperator(): void {
  if (!isOperator.value) throw redirect({ to: "/operator" });
}
