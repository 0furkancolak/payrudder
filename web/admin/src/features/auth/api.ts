import { api, unwrap } from "@/shared/api/client";
import { setOperatorToken } from "@/shared/lib/session";

/** Signs in: the token is checked by asking for something only an operator may see; a refused token is forgotten again. */
export async function signIn(token: string): Promise<void> {
  setOperatorToken(token);
  try {
    await unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: 1 } } }));
  } catch (e) {
    setOperatorToken(null);
    throw e;
  }
}
