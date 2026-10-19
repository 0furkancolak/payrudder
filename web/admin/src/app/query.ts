import { QueryClient } from "@tanstack/vue-query";
import { ApiError } from "@/shared/api/client";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // A refused token or a missing resource will not get better by asking again.
      retry: (count, error) => !(error instanceof ApiError && ["UNAUTHENTICATED", "PERMISSION_DENIED", "NOT_FOUND", "INVALID_ARGUMENT"].includes(error.code)) && count < 2,
      refetchOnWindowFocus: false,
      staleTime: 5_000,
    },
  },
});
