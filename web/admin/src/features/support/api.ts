import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, ApiError, toApiError, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import { currentToken } from "@/shared/lib/session";

export type SupportTicket = components["schemas"]["SupportTicket"];
export type SupportMessage = components["schemas"]["SupportMessage"];
export type SupportEvent = components["schemas"]["SupportEvent"];
export type SupportAttachment = components["schemas"]["SupportAttachment"];
export type SupportReport = components["schemas"]["SupportReport"];
export type SupportOperator = components["schemas"]["SupportOperator"];
export type CannedReply = components["schemas"]["SupportCannedReply"];
export type SupportLink = components["schemas"]["SupportLink"];
export type SupportHoliday = components["schemas"]["SupportHoliday"];
/** What a ticket may link to, and how many (the help desk checks the same). */
export const LINK_KINDS: readonly SupportLink["kind"][] = ["customer", "invoice", "subscription"];
export const MAX_LINKS = 5;
export type SupportCategory = SupportTicket["category"];
export type SupportPriority = SupportTicket["priority"];
export type SupportStatus = SupportTicket["status"];

export const CATEGORIES: readonly SupportCategory[] = ["billing", "integration", "bug", "question", "other"];
export const PRIORITIES: readonly SupportPriority[] = ["low", "normal", "high", "urgent"];
export const STATUSES: readonly SupportStatus[] = ["open", "pending", "solved", "closed"];

/** What a message may carry (the help desk checks the same: png, jpeg, pdf or text, 5 MB each, 10 to a ticket). */
export const ATTACH_TYPES: readonly string[] = ["image/png", "image/jpeg", "application/pdf", "text/plain"];
export const MAX_ATTACH_BYTES = 5 * 1024 * 1024;
export const MAX_ATTACH_FILES = 10;

export const supportKeys = {
  all: ["support"] as const,
  mine: (status: string) => ["support", "mine", status] as const,
  ticket: (id: string) => ["support", "ticket", id] as const,
  queue: (f: QueueFilter) => ["support", "queue", f] as const,
  operatorTicket: (id: string) => ["support", "operator-ticket", id] as const,
  unread: ["support", "unread"] as const,
  report: ["support", "report"] as const,
  operators: ["support", "operators"] as const,
  canned: ["support", "canned"] as const,
  hits: (q: string) => ["support", "mine-search", q] as const,
  holidays: (appId: string) => ["support", "holidays", appId] as const,
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

/**
 * The ids of the tickets whose messages (the ones the person can read) match a search, for the page's own search to add to what it finds in the subject and number it has
 * loaded. Whole words, Postgres full text; nothing is asked for less than two characters.
 */
export function useMyTicketHits(search: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => supportKeys.hits(toValue(search).trim())),
    queryFn: async () => new Set((await unwrap(api.GET("/app/v1/support/tickets", { params: { query: { search: toValue(search).trim() } } }))).tickets.map((t) => t.id)),
    enabled: computed(() => toValue(search).trim().length >= 2),
    staleTime: 30_000,
  });
}

/** How many of the person's tickets have news they have not read: the badge on the help page's link in the sidebar. */
export function useMyUnread(enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: supportKeys.unread,
    queryFn: async () => (await unwrap(api.GET("/app/v1/support/unread"))).unread,
    enabled: computed(() => toValue(enabled)),
    refetchInterval: 60_000,
    retry: false,
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
    rate: useMutation({
      mutationFn: (v: { id: string; rating: number; comment: string }) => unwrap(api.POST("/app/v1/support/tickets/{id}/rating", { params: { path: { id: v.id } }, body: { rating: v.rating, comment: v.comment } })),
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
  tag: string;
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
        ...(f.tag ? { tag: f.tag } : {}),
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
      mutationFn: (v: { id: string; status?: SupportStatus; priority?: SupportPriority; assignee?: string; tags?: string[]; links?: SupportLink[] }) =>
        unwrap(api.PATCH("/admin/v1/support/tickets/{id}", { params: { path: { id: v.id } }, body: { status: v.status, priority: v.priority, assignee: v.assignee, tags: v.tags, links: v.links } })),
      onSuccess: done,
    }),
    /** Moves the ticket's messages into another ticket of the same person and closes it. */
    merge: useMutation({
      mutationFn: (v: { id: string; into: string }) => unwrap(api.POST("/admin/v1/support/tickets/{id}/merge", { params: { path: { id: v.id } }, body: { into_ticket_id: v.into } })),
      onSuccess: done,
    }),
    saveHolidays: useMutation({
      mutationFn: (v: { appId: string; holidays: SupportHoliday[] }) => unwrap(api.PUT("/admin/v1/support/apps/{app_id}/holidays", { params: { path: { app_id: v.appId } }, body: { holidays: v.holidays } })),
      onSuccess: done,
    }),
    saveCanned: useMutation({
      mutationFn: (v: { id?: string; title: string; body: string }) =>
        v.id
          ? unwrap(api.PUT("/admin/v1/support/canned-replies/{id}", { params: { path: { id: v.id } }, body: { title: v.title, body: v.body } }))
          : unwrap(api.POST("/admin/v1/support/canned-replies", { body: { title: v.title, body: v.body } })),
      onSuccess: done,
    }),
    deleteCanned: useMutation({
      mutationFn: (id: string) => unwrap(api.DELETE("/admin/v1/support/canned-replies/{id}", { params: { path: { id } } })),
      onSuccess: done,
    }),
  };
}

/** The queue and the first-answer times of the last 30 days; the operator's inbox badge counts `waiting`. */
export function useSupportReport(enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: supportKeys.report,
    queryFn: () => unwrap(api.GET("/admin/v1/support/report")),
    enabled: computed(() => toValue(enabled)),
    refetchInterval: 60_000,
    retry: false,
  });
}

/** The addresses a ticket can be assigned to. */
export function useSupportOperators() {
  return useQuery({ queryKey: supportKeys.operators, queryFn: async () => (await unwrap(api.GET("/admin/v1/support/operators"))).operators, staleTime: 5 * 60_000 });
}

/** The days off of an app: the first-answer clock of its tickets does not run on them. */
export function useSupportHolidays(appId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => supportKeys.holidays(toValue(appId))),
    queryFn: () => unwrap(api.GET("/admin/v1/support/apps/{app_id}/holidays", { params: { path: { app_id: toValue(appId) } } })),
    enabled: computed(() => toValue(appId) !== ""),
  });
}

export function useCannedReplies() {
  return useQuery({ queryKey: supportKeys.canned, queryFn: async () => (await unwrap(api.GET("/admin/v1/support/canned-replies"))).replies });
}

// --- files on a message

export type Side = "person" | "operator";

/** A refusal the page can see before anything is sent: the same keys the help desk answers with. */
export function refuseFile(file: File, already: number): ApiError | undefined {
  if (already >= MAX_ATTACH_FILES) return new ApiError("too many files", "RESOURCE_EXHAUSTED", "support.attachment.too_many");
  if (!ATTACH_TYPES.includes(file.type)) return new ApiError("type not allowed", "INVALID_ARGUMENT", "support.attachment.bad_type");
  if (file.size > MAX_ATTACH_BYTES) return new ApiError("file too large", "INVALID_ARGUMENT", "support.attachment.too_large");
  return undefined;
}

/**
 * Attaches the files to a message, one request each in the order given, and says which could not be: a file that fails does not stop the ones after it. Nothing
 * is thrown; a page shows the failures next to the message it has already sent.
 */
export async function attachFiles(side: Side, ticketId: string, messageId: string, files: readonly File[]): Promise<{ name: string; error: ApiError }[]> {
  const root = side === "operator" ? "/gw/admin/v1" : "/gw/app/v1";
  const failed: { name: string; error: ApiError }[] = [];
  let placed = 0;
  for (const file of files) {
    const refused = refuseFile(file, placed);
    if (refused) {
      failed.push({ name: file.name, error: refused });
      continue;
    }
    try {
      const form = new FormData();
      form.append("file", file, file.name);
      const token = currentToken();
      const res = await fetch(`${root}/support/tickets/${ticketId}/messages/${messageId}/attachments`, { method: "POST", body: form, headers: token ? { Authorization: `Bearer ${token}` } : {} });
      if (!res.ok) throw toApiError(await res.json().catch(() => undefined));
      placed++;
    } catch (e) {
      failed.push({ name: file.name, error: toApiError(e) });
    }
  }
  return failed;
}

/** A short-lived link to the file; the page opens it, the browser downloads it. */
export async function attachmentLink(side: Side, ticketId: string, attachmentId: string): Promise<string> {
  const path = { id: ticketId, attachment_id: attachmentId };
  const r =
    side === "operator"
      ? await unwrap(api.GET("/admin/v1/support/tickets/{id}/attachments/{attachment_id}", { params: { path } }))
      : await unwrap(api.GET("/app/v1/support/tickets/{id}/attachments/{attachment_id}", { params: { path } }));
  return r.url;
}
