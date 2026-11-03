<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import PriceChangeHistory from "./PriceChangeHistory.vue";
import { NativeSelect } from "@/shared/ui/native-select";
import { languages, locales } from "@/app/i18n";
import { usePreviewPriceChangeMail, useSchedulePriceChange, type PriceChangeResult } from "./api";

/**
 * A price change for the customers of a package: the subscriptions on one version move to another (the latest unless named) at their next renewal, when that is
 * at least the notice away, and each customer is mailed. "Check" counts first and changes nothing; "Schedule and tell customers" does it, after asking. The
 * subscriptions left out are counted by reason: the ones renewing too soon can be done by running it again later. Running it again continues the same price
 * change (what it scheduled is not scheduled or mailed twice), and the price changes made so far are listed below, each with its retry and withdraw actions.
 */
const props = defineProps<{ tenantId: string; code: string; latest: number }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const run = useSchedulePriceChange(() => props.tenantId);
const preview = usePreviewPriceChangeMail(() => props.tenantId);
/** The language of the example mail; empty is the tenant's own default. */
const previewLocale = ref("");

const fromVersion = ref("1");
const toVersion = ref("");
const noticeDays = ref("30");
/** The optional date (yyyy-mm-dd) the new price starts from; empty moves each subscription at its next renewal. */
const fromDate = ref("");
/** The optional days before the new price that the notice mails go out; empty sends them when the run schedules the change. */
const sendDays = ref("");
const checked = ref<PriceChangeResult | null>(null);
const done = ref<PriceChangeResult | null>(null);

watch(open, (isOpen) => {
  if (!isOpen) return;
  fromVersion.value = String(Math.max(1, props.latest - 1));
  toVersion.value = "";
  noticeDays.value = "30";
  fromDate.value = "";
  sendDays.value = "";
  checked.value = null;
  done.value = null;
  previewLocale.value = "";
  run.reset();
  preview.reset();
});
watch([fromVersion, toVersion, noticeDays, fromDate, sendDays], () => ((checked.value = null), preview.reset()));

const sendDaysValid = computed(() => sendDays.value === "" || (Number(sendDays.value) >= Number(noticeDays.value) && Number(sendDays.value) <= 365));
const valid = computed(() => Number(fromVersion.value) >= 1 && Number(noticeDays.value) >= 14 && Number(noticeDays.value) <= 365 && (toVersion.value === "" || Number(toVersion.value) >= 1) && sendDaysValid.value);
const args = (dryRun: boolean) => ({
  code: props.code,
  fromVersion: Number(fromVersion.value),
  toVersion: toVersion.value ? Number(toVersion.value) : undefined,
  noticeDays: Number(noticeDays.value),
  ...(fromDate.value ? { effectiveFrom: `${fromDate.value}T00:00:00Z` } : {}),
  ...(sendDays.value ? { noticeSendDaysBefore: Number(sendDays.value) } : {}),
  dryRun,
});
/** The example mail counts its date as now plus the notice: with a start date, that is the days to it (the mail API takes at most a year). */
const previewNotice = () => (fromDate.value ? Math.min(365, Math.max(14, Math.ceil((Date.parse(`${fromDate.value}T00:00:00Z`) - Date.now()) / 86_400_000))) : Number(noticeDays.value));

async function check() {
  checked.value = await run.mutateAsync(args(true)).catch(() => null);
}
async function schedule() {
  if (!checked.value || checked.value.scheduled === 0) return;
  if (!(await askConfirm({ body: t("billing.packages.priceChange.confirm", { n: checked.value.scheduled, version: checked.value.to_version }, checked.value.scheduled) }))) return;
  const r = await run.mutateAsync(args(false)).catch(() => null);
  if (r) {
    done.value = r;
    checked.value = null;
  }
}
async function showPreview() {
  await preview.mutateAsync({ code: props.code, ...(toVersion.value ? { toVersion: Number(toVersion.value) } : {}), noticeDays: previewNotice(), locale: previewLocale.value }).catch(() => null);
}
const reasons = (r: PriceChangeResult) => {
  const m = new Map<string, number>();
  for (const s of r.skipped) m.set(s.reason, (m.get(s.reason) ?? 0) + 1);
  return [...m.entries()];
};
</script>

<template>
  <Sheet v-model:open="open" name="price-change">
    <SheetContent data-testid="price-change-sheet">
      <SheetHeader>
        <SheetTitle>{{ t("billing.packages.priceChange.title", { code }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.packages.priceChange.intro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="price-change-form" class="grid gap-3" @submit.prevent="check">
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="grid gap-1 text-sm">{{ t("billing.packages.priceChange.from") }}
              <Input v-model="fromVersion" type="number" min="1" required data-testid="pc-from" />
            </label>
            <label class="grid gap-1 text-sm">{{ t("billing.packages.priceChange.to") }}
              <Input v-model="toVersion" type="number" min="1" :placeholder="t('billing.packages.priceChange.latest', { n: latest })" data-testid="pc-to" />
            </label>
          </div>
          <label class="grid gap-1 text-sm">{{ t("billing.packages.priceChange.notice") }}
            <Input v-model="noticeDays" type="number" min="14" max="365" required data-testid="pc-notice" />
            <span class="text-muted-foreground text-xs">{{ t("billing.packages.priceChange.noticeHelp") }}</span>
          </label>
          <label class="grid gap-1 text-sm">{{ t("billing.packages.priceChange.fromDate") }}
            <Input v-model="fromDate" type="date" data-testid="pc-from-date" />
            <span class="text-muted-foreground text-xs">{{ t("billing.packages.priceChange.fromDateHelp") }}</span>
          </label>
          <label class="grid gap-1 text-sm">{{ t("billing.packages.priceChange.sendDays") }}
            <Input v-model="sendDays" type="number" :min="noticeDays || 14" max="365" data-testid="pc-send-days" :aria-invalid="!sendDaysValid" />
            <span class="text-muted-foreground text-xs">{{ t("billing.packages.priceChange.sendDaysHelp") }}</span>
            <span v-if="!sendDaysValid" class="text-destructive text-xs" role="alert" data-testid="pc-send-days-error">{{ t("billing.packages.priceChange.sendDaysInvalid") }}</span>
          </label>
        </form>
        <ErrorText :error="run.error.value" />

        <section class="grid gap-2" data-testid="pc-preview">
          <h3 class="text-sm font-medium">{{ t("billing.packages.priceChange.preview.title") }}</h3>
          <p class="text-muted-foreground text-xs">{{ t("billing.packages.priceChange.preview.hint") }}</p>
          <div class="flex flex-wrap items-end gap-2">
            <label class="grid gap-1 text-sm">{{ t("billing.packages.priceChange.preview.language") }}
              <NativeSelect v-model="previewLocale" data-testid="pc-preview-language">
                <option value="">{{ t("billing.packages.priceChange.preview.tenantDefault") }}</option>
                <option v-for="l in locales" :key="l" :value="l">{{ languages[l].name }}</option>
              </NativeSelect>
            </label>
            <Button type="button" variant="outline" :disabled="!valid || preview.isPending.value" data-testid="pc-preview-button" @click="showPreview">{{ t("billing.packages.priceChange.preview.show") }}</Button>
          </div>
          <ErrorText :error="preview.error.value" />
          <div v-if="preview.data.value" class="grid gap-2" data-testid="pc-preview-mail">
            <p class="text-sm"><span class="text-muted-foreground">{{ t("billing.packages.priceChange.preview.subject") }}</span> <strong data-testid="pc-preview-subject" dir="auto">{{ preview.data.value.subject }}</strong></p>
            <!-- The mail is the notifier's HTML: it is shown in a frame that runs no script and submits nothing. -->
            <iframe sandbox="" :srcdoc="preview.data.value.html" :title="t('billing.packages.priceChange.preview.title')" class="h-80 w-full rounded-xl border bg-white" data-testid="pc-preview-frame" />
          </div>
        </section>

        <div v-if="checked" class="bg-muted rounded-2xl px-4 py-3 text-sm" data-testid="pc-check">
          <p class="font-medium">{{ t("billing.packages.priceChange.willSchedule", { n: checked.scheduled, version: checked.to_version }, checked.scheduled) }}</p>
          <ul v-if="checked.skipped.length > 0 || checked.already_scheduled > 0" class="text-muted-foreground mt-1 list-disc ps-5 text-xs">
            <li v-if="checked.already_scheduled > 0">{{ t("billing.packages.priceChange.alreadyScheduled", { n: checked.already_scheduled }) }}</li>
            <li v-for="[reason, n] in reasons(checked)" :key="reason">{{ t(`billing.packages.priceChange.reasons.${reason}`, { n }) }}</li>
          </ul>
        </div>
        <div v-if="done" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status" data-testid="pc-done">
          <p class="font-medium">{{ t("billing.packages.priceChange.done", { n: done.scheduled, version: done.to_version }, done.scheduled) }}</p>
          <ul v-if="done.skipped.length > 0 || done.already_scheduled > 0" class="mt-1 list-disc ps-5 text-xs">
            <li v-if="done.already_scheduled > 0">{{ t("billing.packages.priceChange.alreadyScheduled", { n: done.already_scheduled }) }}</li>
            <li v-for="[reason, n] in reasons(done)" :key="reason">{{ t(`billing.packages.priceChange.reasons.${reason}`, { n }) }}</li>
          </ul>
        </div>
        <PriceChangeHistory :tenant-id="tenantId" :code="code" :open="open" />
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.close") }}</Button>
        <Button type="submit" form="price-change-form" variant="outline" :disabled="!valid || run.isPending.value" data-testid="pc-check-button">{{ t("billing.packages.priceChange.check") }}</Button>
        <Button type="button" :disabled="!checked || checked.scheduled === 0 || run.isPending.value" data-testid="pc-schedule" @click="schedule">{{ t("billing.packages.priceChange.schedule") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
