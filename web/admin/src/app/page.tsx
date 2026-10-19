"use client";

import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { api, errorMessage, setToken } from "@/lib/api";

export default function SignIn() {
  const router = useRouter();
  const [token, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setToken(token.trim());
    // The operator token is checked by asking for something only an operator may see.
    const { error } = await api.GET("/admin/v1/tenants", { params: { query: { page_size: 1 } } });
    setBusy(false);
    if (error !== undefined) {
      setToken(null);
      setError(errorMessage(error));
      return;
    }
    window.dispatchEvent(new Event("quotify-auth"));
    router.push("/tenants");
  }

  return (
    <>
      <h1>Operator console</h1>
      <p className="muted">Reads and acts through the gateway only. The token stays in this browser tab (session storage).</p>
      <form onSubmit={submit} className="row">
        <input
          aria-label="Operator token"
          type="password"
          autoComplete="off"
          placeholder="Operator token"
          value={token}
          onChange={(e) => setValue(e.target.value)}
        />
        <button type="submit" disabled={busy || token.trim() === ""}>
          Sign in
        </button>
      </form>
      {error && <p className="error">{error}</p>}
    </>
  );
}
