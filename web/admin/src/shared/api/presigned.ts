import { useMutation } from "@tanstack/vue-query";
import { ApiError } from "./client";

/** Asks `fetchLink` for a presigned link (valid for five minutes) and opens it in a new tab. A reply without a link is an error, not a blank tab. */
export function usePresignedOpen<V = void>(fetchLink: (v: V) => Promise<{ url?: string }>) {
  return useMutation({
    mutationFn: async (v: V) => {
      const r = await fetchLink(v);
      if (!r.url) throw new ApiError("no link", "NO_LINK");
      return r.url;
    },
    onSuccess: (url) => {
      window.open(url, "_blank", "noopener");
    },
  });
}
