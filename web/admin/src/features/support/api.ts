import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";

export type SupportTicket = components["schemas"]["SupportTicket"];
export type SupportMessage = components["schemas"]["SupportMessage"];
export type SupportCategory = SupportTicket["category"];
export type SupportPriority = SupportTicket["priority"];
export type SupportStatus = SupportTicket["status"];

export const CATEGORIES: readonly SupportCategory[] = ["billing", "integration", "bug", "question", "other"];
export const PRIORITIES: readonly SupportPriority[] = ["low", "normal", "high", "urgent"];
export const STATUSES: readonly SupportStatus[] = ["open", "pending", "solved", "closed"];

export const supportKeys = {
  all: ["support"] as const,
  mine: (status: string) => ["support", "mine", status] as const,
  ticket: (id: string) => ["support", "ticket", id] as const,
  queue: (f: QueueFilter) => ["support", "queue", f] as const,
  operatorTicket: (id: string) => ["support", "operator-ticket", id] as const,
};

// --- the person's side: the tickets of every account they belong to

export function useMyTickets(status: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => supportKeys.mine(toValue(status))),
    queryFn: async () => {
      const s = toValue(status) as "active" | "open" | "pending" | "solved" | "closed" | "";
      return (await unwrap(api.GET("/app/v1/support/tickets", { params: { query: s ? { status: s } : {} } }))).tickets;
    },
  });
}

export function useMyTicket(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => supportKeys.ticket(toValue(id))),
    queryFn: () => unwrap(api.GET("/app/v1/support/tickets/{id}", { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => toValue(id) !== ""),
  });
}

export function useMySupportActions() {
  const client = useQueryClient();
  const done = () => client.invalidateQueries({ queryKey: supportKeys.all });
  return {
    open: useMutation({
      mutationFn: (body: components["schemas"]["OpenSupportTicketRequest"]) => unwrap(api.POST("/app/v1/support/tickets", { body })),
      onSuccess: done,
    }),
    reply: useMutation({
      mutationFn: (v: { id: string; body: string }) => unwrap(api.POST("/app/v1/support/tickets/{id}/replies", { params: { path: { id: v.id } }, body: { body: v.body } })),
      onSuccess: done,
    }),
    setStatus: useMutation({
      mutationFn: (v: { id: string; status: "solved" | "open" }) => unwrap(api.PUT("/app/v1/support/tickets/{id}/status", { params: { path: { id: v.id } }, body: { status: v.status } })),
      onSuccess: done,
    }),
  };
}

// --- the operator's side: the whole queue

export interface QueueFilter {
  status: string;
  priority: string;
  assignee: string;
  search: string;
  late: boolean;
}

export function useQueue(filter: MaybeRefOrGetter<QueueFilter>) {
  return useQuery({
    queryKey: computed(() => supportKeys.queue(toValue(filter))),
    queryFn: async () => {
      const f = toValue(filter);
      const query = {
        ...(f.status ? { status: f.status as "active" } : {}),
        ...(f.priority ? { priority: f.priority as "low" } : {}),
        ...(f.assignee ? { assignee: f.assignee } : {}),
        ...(f.search ? { search: f.search } : {}),
        ...(f.late ? { sla_breached: true } : {}),
      };
      return (await unwrap(api.GET("/admin/v1/support/tickets", { params: { query } }))).tickets;
    },
    refetchInterval: 30_000,
  });
}

export function useOperatorTicket(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => supportKeys.operatorTicket(toValue(id))),
    queryFn: () => unwrap(api.GET("/admin/v1/support/tickets/{id}", { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => toValue(id) !== ""),
  });
}

export function useOperatorSupportActions() {
  const client = useQueryClient();
  const done = () => client.invalidateQueries({ queryKey: supportKeys.all });
  return {
    reply: useMutation({
      mutationFn: (v: { id: string; body: string; internal: boolean }) =>
        unwrap(api.POST("/admin/v1/support/tickets/{id}/replies", { params: { path: { id: v.id } }, body: { body: v.body, internal: v.internal } })),
      onSuccess: done,
    }),
    update: useMutation({
      mutationFn: (v: { id: string; status?: SupportStatus; priority?: SupportPriority; assignee?: string }) =>
        unwrap(api.PATCH("/admin/v1/support/tickets/{id}", { params: { path: { id: v.id } }, body: { status: v.status, priority: v.priority, assignee: v.assignee } })),
      onSuccess: done,
    }),
  };
}
