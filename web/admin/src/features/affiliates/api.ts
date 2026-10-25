import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { toApiError, tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import { PROGRAM_NOT_SET, type Affiliate, type Balance, type Commission, type Funnel, type JournalEntry, type Payout, type Program, type Referral } from "./logic";

type Id = MaybeRefOrGetter<string>;
const tid = toValue<string>;

export const affiliateKeys = {
  all: (tenantId: string) => ["tenants", tenantId, "affiliates"] as const,
};

/** A list the API pages with tokens. */
export interface Page<T> {
  items: T[];
  next: string;
  total: number;
}

const clean = <T extends Record<string, unknown>>(q: T): T => Object.fromEntries(Object.entries(q).filter(([, v]) => v !== "" && v !== undefined)) as T;

// --- the program

/** The program, or null before one is set (the API answers 404 with a stable key, which is a normal state, not an error). */
export function useProgram(tenantId: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "program"]),
    queryFn: async (): Promise<Program | null> => {
      try {
        return await unwrap(tenantApi(tid(tenantId)).GET("/v1/affiliates/program"));
      } catch (e) {
        if (toApiError(e).key === PROGRAM_NOT_SET) return null;
        throw e;
      }
    },
  });
}

function useRefresh(tenantId: Id) {
  const queries = useQueryClient();
  return () => queries.invalidateQueries({ queryKey: affiliateKeys.all(tid(tenantId)) });
}

export function useSetProgram(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({ mutationFn: (body: Program) => unwrap(tenantApi(tid(tenantId)).PUT("/v1/affiliates/program", { body })), onSuccess: refresh });
}

// --- partners

export interface AffiliateFilter {
  status: string;
  search: string;
}

export function useAffiliates(tenantId: Id, filter: MaybeRefOrGetter<AffiliateFilter>, pageToken: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "list", toValue(filter), toValue(pageToken)]),
    queryFn: async (): Promise<Page<Affiliate>> => {
      const f = toValue(filter);
      const r = await unwrap(
        tenantApi(tid(tenantId)).GET("/v1/affiliates", {
          params: { query: clean({ status: f.status as "active", search: f.search.trim(), page_size: PAGE.paged, page_token: toValue(pageToken) }) },
        }),
      );
      return { items: (r.affiliates ?? []) as Affiliate[], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

/** Every partner (up to one page of 200), for the selects that name one and to turn an id into a code. */
export function useAffiliateOptions(tenantId: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "options"]),
    queryFn: async () => ((await unwrap(tenantApi(tid(tenantId)).GET("/v1/affiliates", { params: { query: { page_size: PAGE.whole } } }))).affiliates ?? []) as Affiliate[],
  });
}

export interface NewAffiliate {
  email: string;
  name?: string;
  code?: string;
  payout_method?: "manual" | "bank_transfer";
  payout_details?: string;
  pending?: boolean;
}

export function useCreateAffiliate(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({ mutationFn: (body: NewAffiliate) => unwrap(tenantApi(tid(tenantId)).POST("/v1/affiliates", { body })), onSuccess: refresh });
}

export interface AffiliateChange {
  name?: string;
  payout_method?: "manual" | "bank_transfer";
  payout_details?: string;
  status?: "active" | "suspended";
}

export function useUpdateAffiliate(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({
    mutationFn: (v: { id: string; body: AffiliateChange }) => unwrap(tenantApi(tid(tenantId)).PATCH("/v1/affiliates/{id}", { params: { path: { id: v.id } }, body: v.body })),
    onSuccess: refresh,
  });
}

export function useLinkPromotionCode(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({
    mutationFn: (v: { id: string; code: string }) =>
      unwrap(tenantApi(tid(tenantId)).PUT("/v1/affiliates/{id}/promotion-code", { params: { path: { id: v.id } }, body: { promotion_code: v.code } })),
    onSuccess: refresh,
  });
}

export function useReferrals(tenantId: Id, affiliateId: Id, pageToken: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "referrals", toValue(affiliateId), toValue(pageToken)]),
    enabled: computed(() => toValue(affiliateId) !== ""),
    queryFn: async (): Promise<Page<Referral>> => {
      const r = await unwrap(
        tenantApi(tid(tenantId)).GET("/v1/affiliates/referrals", { params: { query: clean({ affiliate_id: toValue(affiliateId), page_size: PAGE.paged, page_token: toValue(pageToken) }) } }),
      );
      return { items: r.referrals ?? [], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

// --- commissions

export interface CommissionFilter {
  affiliate_id: string;
  status: string;
  kind: string;
  payout_id: string;
  /** ISO instants (`from` inclusive, `to` exclusive), or "". */
  from: string;
  to: string;
}

export const NO_COMMISSION_FILTER: CommissionFilter = { affiliate_id: "", status: "", kind: "", payout_id: "", from: "", to: "" };

export function useCommissions(tenantId: Id, filter: MaybeRefOrGetter<CommissionFilter>, pageToken: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "commissions", toValue(filter), toValue(pageToken)]),
    queryFn: async (): Promise<Page<Commission>> => {
      const f = toValue(filter);
      const r = await unwrap(
        tenantApi(tid(tenantId)).GET("/v1/affiliates/commissions", {
          params: { query: clean({ affiliate_id: f.affiliate_id, status: f.status as "paid", kind: f.kind as "commission", payout_id: f.payout_id, from: f.from, to: f.to, page_size: PAGE.paged, page_token: toValue(pageToken) }) },
        }),
      );
      return { items: r.commissions ?? [], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

export interface ReverseInput {
  invoice_id: string;
  reason: "chargeback" | "manual";
  reference?: string;
  amount_minor?: string;
  note?: string;
}

export function useReverseCommission(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({ mutationFn: (body: ReverseInput) => unwrap(tenantApi(tid(tenantId)).POST("/v1/affiliates/commissions/reverse", { body })), onSuccess: refresh });
}

// --- balances and the funnel

export function useBalances(tenantId: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "balances"]),
    queryFn: async (): Promise<Balance[]> => (await unwrap(tenantApi(tid(tenantId)).GET("/v1/affiliates/balances"))).balances ?? [],
  });
}

export function useFunnel(tenantId: Id, from: Id, to: Id, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "funnel", toValue(from), toValue(to)]),
    enabled: computed(() => toValue(enabled)),
    queryFn: (): Promise<Funnel> => unwrap(tenantApi(tid(tenantId)).GET("/v1/affiliates/funnel", { params: { query: { from: toValue(from), to: toValue(to) } } })),
  });
}

// --- payouts

export function usePayouts(tenantId: Id, status: Id, pageToken: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "payouts", toValue(status), toValue(pageToken)]),
    queryFn: async (): Promise<Page<Payout>> => {
      const r = await unwrap(
        tenantApi(tid(tenantId)).GET("/v1/affiliates/payouts", { params: { query: clean({ status: toValue(status) as "open", page_size: PAGE.paged, page_token: toValue(pageToken) }) } }),
      );
      return { items: r.payouts ?? [], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

export function useCreatePayouts(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({ mutationFn: () => unwrap(tenantApi(tid(tenantId)).POST("/v1/affiliates/payouts", { body: {} })), onSuccess: refresh });
}

export function useMarkPaid(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({
    mutationFn: (v: { id: string; reference: string }) =>
      unwrap(tenantApi(tid(tenantId)).POST("/v1/affiliates/payouts/{id}/pay", { params: { path: { id: v.id } }, body: v.reference ? { reference: v.reference } : {} })),
    onSuccess: refresh,
  });
}

export function useCancelPayout(tenantId: Id) {
  const refresh = useRefresh(tenantId);
  return useMutation({ mutationFn: (id: string) => unwrap(tenantApi(tid(tenantId)).POST("/v1/affiliates/payouts/{id}/cancel", { params: { path: { id } } })), onSuccess: refresh });
}

// --- the journal

export interface JournalFilter {
  currency: string;
  from: string;
  to: string;
}

export function useJournal(tenantId: Id, filter: MaybeRefOrGetter<JournalFilter>, pageToken: Id) {
  return useQuery({
    queryKey: computed(() => [...affiliateKeys.all(tid(tenantId)), "journal", toValue(filter), toValue(pageToken)]),
    queryFn: async () => {
      const f = toValue(filter);
      const r = await unwrap(
        tenantApi(tid(tenantId)).GET("/v1/affiliates/journal", { params: { query: clean({ currency: f.currency, from: f.from, to: f.to, page_size: PAGE.paged, page_token: toValue(pageToken) }) } }),
      );
      return { items: (r.entries ?? []) as JournalEntry[], next: r.next_page_token ?? "", total: r.total ?? 0, totals: r.totals ?? [] };
    },
  });
}

/** Downloads the journal as a CSV. The file needs the session token, so it is fetched through the API client and saved from a blob, not linked. */
export async function downloadJournalCsv(tenantId: string, filter: JournalFilter): Promise<void> {
  const blob = await unwrap(tenantApi(tenantId).GET("/v1/affiliates/journal.csv", { params: { query: clean({ currency: filter.currency, from: filter.from, to: filter.to }) }, parseAs: "blob" }));
  const url = URL.createObjectURL(blob as Blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "affiliate-journal.csv";
  a.click();
  URL.revokeObjectURL(url);
}
