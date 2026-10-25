import { computed, ref } from "vue";

/**
 * Paging by the API's page tokens. A token only goes forward, so the tokens of the pages already seen are kept to go back. A filter that changes
 * starts over from the first page (`reset`).
 */
export function usePager() {
  const seen = ref<string[]>([]);
  const token = ref("");
  return {
    token,
    hasPrev: computed(() => seen.value.length > 0),
    next(nextToken: string) {
      seen.value = [...seen.value, token.value];
      token.value = nextToken;
    },
    prev() {
      token.value = seen.value[seen.value.length - 1] ?? "";
      seen.value = seen.value.slice(0, -1);
    },
    reset() {
      seen.value = [];
      token.value = "";
    },
  };
}
