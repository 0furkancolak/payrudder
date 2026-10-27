<script setup lang="ts">
import { Copy, ExternalLink, Receipt, RefreshCw, Trash2 } from "@lucide/vue";
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { usePortalLinkActions } from "@/features/paymentlinks/api";
import { copyText } from "@/shared/lib/clipboard";
import { absoluteUrl } from "@/shared/lib/url";
import { Button } from "@/shared/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";

/**
 * The customer's payment page (the hosted page at /c/<token>) as a small menu: copy its link, open it in a new tab, regenerate it, revoke every link.
 * Only a hash of a link is stored, so the address is known here only after it was made; copying or opening makes one first (valid DAYS days) and
 * keeps it for the rest of the time the sheet is open. Nothing is rendered in the sheet itself.
 */
const props = defineProps<{ tenantId: string; customer: string }>();
const DAYS = 30;
const { t } = useI18n();
const act = usePortalLinkActions(() => props.tenantId);
const link = ref("");
watch(
  () => props.customer,
  () => (link.value = ""),
);

async function make(): Promise<string> {
  const res = await act.create.mutateAsync({ externalId: props.customer, days: DAYS });
  link.value = absoluteUrl(res?.url, window.location.origin);
  return link.value;
}
async function guard<T>(run: () => Promise<T>): Promise<T | undefined> {
  try {
    return await run();
  } catch (e) {
    toast.error(e instanceof Error ? e.message : String(e));
    return undefined;
  }
}
async function copy() {
  const url = link.value || (await guard(make));
  if (!url) return;
  if (await copyText(url)) toast.success(t("billing.paymentLinks.portal.copied"));
  else toast.error(t("common.copyFailed"));
}
async function openPage() {
  // The tab is opened before the link is made, so the browser counts it as the click's own and does not block it.
  const tab = link.value ? null : window.open("", "_blank");
  if (tab) tab.opener = null;
  const url = link.value || (await guard(make));
  if (!url) {
    tab?.close();
    return;
  }
  if (tab) tab.location.href = url;
  else window.open(url, "_blank", "noopener");
}
async function regenerate() {
  await guard(async () => {
    await act.revoke.mutateAsync(props.customer);
    const url = await make();
    if (await copyText(url)) toast.success(t("billing.paymentLinks.portal.regenerated"));
    else toast.error(t("common.copyFailed"));
  });
}
async function revoke() {
  if (!window.confirm(t("billing.paymentLinks.portal.confirmRevoke"))) return;
  await guard(async () => {
    await act.revoke.mutateAsync(props.customer);
    link.value = "";
    toast.success(t("billing.paymentLinks.portal.revoked"));
  });
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button size="sm" variant="outline" data-testid="customer-payment-page"><Receipt aria-hidden="true" />{{ t("billing.paymentLinks.portal.open") }}</Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="min-w-56">
      <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">{{ t("billing.paymentLinks.portal.validFor", { days: DAYS }) }}</DropdownMenuLabel>
      <DropdownMenuItem class="rounded-xl" data-testid="payment-page-copy" @select="copy"><Copy class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.portal.copy") }}</DropdownMenuItem>
      <DropdownMenuItem class="rounded-xl" data-testid="payment-page-open" @select="openPage"><ExternalLink class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.portal.openPage") }}</DropdownMenuItem>
      <DropdownMenuItem class="rounded-xl" data-testid="payment-page-regenerate" @select="regenerate"><RefreshCw class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.portal.regenerate") }}</DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem class="text-destructive rounded-xl" data-testid="payment-page-revoke" @select="revoke"><Trash2 class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.portal.revoke") }}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
