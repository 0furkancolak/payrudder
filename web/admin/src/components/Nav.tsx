"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { setToken } from "@/lib/api";
import { useToken } from "@/lib/auth";

export function Nav() {
  const router = useRouter();
  const signedIn = useToken() !== null;
  return (
    <nav>
      <strong>Quotify</strong>
      {signedIn && (
        <>
          <Link href="/tenants">Tenants</Link>
          <Link href="/dead-letters">Dead letters</Link>
          <button
            className="link"
            onClick={() => {
              setToken(null);
              window.dispatchEvent(new Event("quotify-auth"));
              router.replace("/");
            }}
          >
            Sign out
          </button>
        </>
      )}
    </nav>
  );
}
