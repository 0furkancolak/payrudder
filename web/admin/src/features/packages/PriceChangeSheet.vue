<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useSchedulePriceChange, type PriceChangeResult } from "./api";

/**
 * A price change for the customers of a package: the subscriptions on one version move to another (the latest unless named) at their next renewal, when that is
 * at least the notice away, and each customer is mailed. "Check" counts first and changes nothing; "Schedule and tell customers" does it, after asking. The
 * subscriptions left out are counted by reason: the ones renewing too soon can be done by running it again later.
 */
const props = defineProps<{ tenantId: string; code: string; latest: number }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const run = useSchedulePriceChange(() => props.tenantId);

const fromVersion = ref("1");
const toVersion = ref("");
const noticeDays = ref("30");
const checked = ref<PriceChangeResult | null>(null);
const done = ref<PriceChangeResult | null>(null);

watch(open, (isOpen) => {
  if (!isOpen) return;
  fromVersion.value = String(Math.max(1, props.latest - 1));
  toVersion.value = "";
  noticeDays.value = "30";
  checked.value = null;
  done.value = null;
  run.reset();
});
watch([fromVersion, toVersion, noticeDays], () => (checked.value = null));

const valid = computed(() => Number(fromVersion.value) >= 1 && Number(noticeDays.value) >= 14 && Number(noticeDays.value) <= 365 && (toVersion.value === "" || Number(toVersion.value) >= 1));
const args = (dryRun: boolean) => ({ code: props.code, fromVersion: Number(fromVersion.value), toVersion: toVersion.value ? Number(toVersion.value) : undefined, noticeDays: Number(noticeDays.value), dryRun });

async function check() {
  checked.value = await run.mutateAsync(args(true)).catch(() => null);
}
async function schedule() {
  if (!checked.value || checked.value.scheduled === 0) return;
  if (!(await askConfirm({ body: t("billing.packages.priceChange.confirm", { n: checked.value.scheduled, version: checked.value.to_version }) }))) return;
  const r = await run.mutateAsync(args(false)).catch(() => null);
  if (r) {
    done.value = r;
    checked.value = null;
  }
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
        </form>
        <ErrorText :error="run.error.value" />

        <div v-if="checked" class="bg-muted rounded-2xl px-4 py-3 text-sm" data-testid="pc-check">
          <p class="font-medium">{{ t("billing.packages.priceChange.willSchedule", { n: checked.scheduled, version: checked.to_version }) }}</p>
          <ul v-if="checked.skipped.length > 0" class="text-muted-foreground mt-1 list-disc ps-5 text-xs">
            <li v-for="[reason, n] in reasons(checked)" :key="reason">{{ t(`billing.packages.priceChange.reasons.${reason}`, { n }) }}</li>
          </ul>
        </div>
        <div v-if="done" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status" data-testid="pc-done">
          <p class="font-medium">{{ t("billing.packages.priceChange.done", { n: done.scheduled, version: done.to_version }) }}</p>
          <ul v-if="done.skipped.length > 0" class="mt-1 list-disc ps-5 text-xs">
            <li v-for="[reason, n] in reasons(done)" :key="reason">{{ t(`billing.packages.priceChange.reasons.${reason}`, { n }) }}</li>
          </ul>
        </div>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.close") }}</Button>
        <Button type="submit" form="price-change-form" variant="outline" :disabled="!valid || run.isPending.value" data-testid="pc-check-button">{{ t("billing.packages.priceChange.check") }}</Button>
        <Button type="button" :disabled="!checked || checked.scheduled === 0 || run.isPending.value" data-testid="pc-schedule" @click="schedule">{{ t("billing.packages.priceChange.schedule") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
