import { computed, ref, watch } from "vue";
import { PAGE } from "@/shared/api/limits";

/**
 * Paging by the API's page tokens. A token only goes forward, so the tokens of the pages already seen are kept to go back. A filter that changes
 * (or a new page size) starts over from the first page (`reset`).
 */
export function usePager() {
  const seen = ref<string[]>([]);
  const token = ref("");
  const size = ref<number>(PAGE.paged);
  const reset = () => {
    seen.value = [];
    token.value = "";
  };
  watch(size, reset);
  return {
    token,
    size,
    /** The page the reader is on, counting from 1. */
    page: computed(() => seen.value.length + 1),
    hasPrev: computed(() => seen.value.length > 0),
    next(nextToken: string) {
      seen.value = [...seen.value, token.value];
      token.value = nextToken;
    },
    prev() {
      token.value = seen.value[seen.value.length - 1] ?? "";
      seen.value = seen.value.slice(0, -1);
    },
    reset,
  };
}
