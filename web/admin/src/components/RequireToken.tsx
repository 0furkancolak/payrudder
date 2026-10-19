"use client";

import { useRouter } from "next/navigation";
import { useEffect, type ReactNode } from "react";
import { useHydrated, useToken } from "@/lib/auth";

/** Renders its children only once an operator token is in the session; otherwise sends the visitor to the sign-in page. */
export function RequireToken({ children }: { children: ReactNode }) {
  const router = useRouter();
  const hydrated = useHydrated();
  const token = useToken();
  useEffect(() => {
    if (hydrated && token === null) router.replace("/");
  }, [hydrated, token, router]);
  return token !== null ? <>{children}</> : null;
}
