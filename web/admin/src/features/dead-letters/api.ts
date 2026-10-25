import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const deadLetterKeys = { all: ["dead-letters"] as const, queue: (q: string) => ["dead-letters", q] as const };

/** The depth of every dead-letter queue, or the messages of one queue when `queue` is set. */
export function useDeadLetters(queue: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => deadLetterKeys.queue(toValue(queue))),
    queryFn: () => {
      const q = toValue(queue);
      return unwrap(api.GET("/admin/v1/dead-letters", { params: { query: q ? { queue: q, limit: PAGE.few } : {} } }));
    },
  });
}

export function useRedrive() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (queue: string) => unwrap(api.POST("/admin/v1/dead-letters/redrive", { body: { queue, limit: PAGE.long } })),
    onSettled: () => client.invalidateQueries({ queryKey: deadLetterKeys.all }),
  });
}
