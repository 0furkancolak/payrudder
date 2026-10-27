<script setup lang="ts">
import { askConfirm } from "@/shared/lib/confirm";
import { Copy, Inbox, RefreshCw } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { copyText } from "@/shared/lib/clipboard";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { useExpenseInbox, useInboxItems, useRotateInbox } from "./api";

/**
 * The invoice inbox: the address a tenant forwards the invoices it receives to, and how many of them wait. A forwarded invoice is only stored; it is read in the
 * browser when a person opens the review (or a recurring-vendor rule has it recorded), so the console must be open for it to be processed.
 */
const props = defineProps<{ tenantId: string }>();
const emit = defineEmits<{ review: []; rules: [] }>();
const { t } = useI18n();
const errorText = useErrorText();
const inbox = useExpenseInbox(() => props.tenantId);
const rotate = useRotateInbox(() => props.tenantId);
const items = useInboxItems(() => props.tenantId);
const count = computed(() => items.data.value?.items?.length ?? 0);
const address = computed(() => inbox.data.value?.address || (inbox.data.value?.token ? `invoices+${inbox.data.value.token}@…` : ""));

async function copy() {
  const text = inbox.data.value?.address || inbox.data.value?.token || "";
  if (await copyText(text)) toast.success(t("common.copied"));
  else toast.error(t("common.copyFailed"));
}
async function renew() {
  if (!(await askConfirm({ body: t("billing.accounting.inbox.confirmRotate") }))) return;
  rotate.mutate(undefined, { onError: (e) => toast.error(errorText(e)) });
}
</script>

<template>
  <Card class="mb-6" role="region" :aria-label="t('billing.accounting.inbox.title')" data-testid="inbox-card">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-lg font-semibold"><Inbox class="size-5" aria-hidden="true" />{{ t("billing.accounting.inbox.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("billing.accounting.inbox.hint") }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" data-testid="inbox-rules" @click="emit('rules')">{{ t("billing.accounting.rules.open") }}</Button>
        <Button type="button" size="sm" :variant="count ? 'default' : 'outline'" data-testid="inbox-review" @click="emit('review')">
          {{ count ? t("billing.accounting.inbox.review", { count }) : t("billing.accounting.inbox.nothing") }}
        </Button>
      </div>
    </div>
    <div v-if="address" class="flex flex-wrap items-center gap-2">
      <code class="bg-muted min-w-0 flex-1 truncate rounded-lg px-3 py-2 text-xs" data-testid="inbox-address" dir="ltr">{{ address }}</code>
      <Button type="button" variant="outline" size="sm" :aria-label="t('billing.paymentLinks.copy')" @click="copy"><Copy class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.copy") }}</Button>
      <Button type="button" variant="ghost" size="sm" :disabled="rotate.isPending.value" @click="renew"><RefreshCw class="size-4" aria-hidden="true" />{{ t("billing.accounting.inbox.rotate") }}</Button>
    </div>
    <p v-if="inbox.data.value && !inbox.data.value.address" class="text-muted-foreground text-xs">{{ t("billing.accounting.inbox.noDomain") }}</p>
  </Card>
</template>
