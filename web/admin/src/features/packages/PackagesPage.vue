<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { Eye, Plus, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { inRange } from "@/shared/lib/dateRange";
import { useFormat } from "@/shared/lib/format";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { useCanWrite } from "@/shared/lib/access";
import { useTenantCurrency } from "@/features/tenants/currency";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { RowActions } from "@/shared/ui/row-actions";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreatePackage, usePackages } from "./api";
import { CurrencySelect } from "@/shared/ui/search-select";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

const { t, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenantCurrency = useTenantCurrency(tenantId);
const canWrite = useCanWrite();
const packages = usePackages(tenantId);
const create = useCreatePackage(tenantId);

const list = computed(() => packages.data.value ?? []);
const fmt = useFormat();
const range = ref<DateRange>({ from: "", to: "" });
const table = useTableState(() => list.value.filter((p) => inRange(p.version?.publish_time, range.value)), { sheet: { kind: "package", id: (p) => p.plan.code }, searchFields: (p) => [p.plan.code, p.plan.name] });
watch(range, () => (table.page.value = 1), { deep: true });
const active = computed(() => table.active.value || range.value.from !== "" || range.value.to !== "");
function clear() {
  table.clear();
  range.value = { from: "", to: "" };
}
const current = table.row;
type Price = NonNullable<NonNullable<(typeof list.value)[number]["version"]>["prices"]>[number];
/** How a usage price charges, in a few words. */
function priceText(p: Price): string {
  if (p.per_unit) return t("billing.packages.priceKinds.perUnit", { price: p.per_unit.unit_price ?? "" });
  if (p.tiered) return t("billing.packages.priceKinds.tiered", { n: p.tiered.tiers?.length ?? 0 });
  if (p.package) return t("billing.packages.priceKinds.package", { size: p.package.package_size ?? "", price: formatMinor(p.package.package_price_minor, tenantCurrency.value, locale.value) });
  return "—";
}

const creating = ref(false);
const code = ref("");
const name = ref("");
const fee = ref("0");
const extra = ref<{ currency: string; interval: "month" | "year"; amount: string }[]>([]);
const formError = ref("");

// A sheet that was closed opens clean: the last values and error are not shown again.
watch(creating, (v) => {
  if (v) {
    code.value = name.value = formError.value = "";
    fee.value = "0";
    extra.value = [];
    create.reset();
  }
});

function addFee() {
  extra.value.push({ currency: tenantCurrency.value || FALLBACK_CURRENCY, interval: "year", amount: "0" });
}

function submit() {
  formError.value = "";
  const base = parseMajor(fee.value, tenantCurrency.value);
  if (base === null) {
    formError.value = t("billing.packages.badAmount", { currency: tenantCurrency.value });
    return;
  }
  const fees: { currency: string; interval: "month" | "year"; baseFeeMinor: string }[] = [];
  for (const f of extra.value) {
    if (f.interval === "month" && f.currency === tenantCurrency.value) {
      formError.value = t("billing.packages.tenantMonthly", { currency: f.currency });
      return;
    }
    const minor = parseMajor(f.amount, f.currency);
    if (minor === null) {
      formError.value = t("billing.packages.badAmount", { currency: f.currency });
      return;
    }
    fees.push({ currency: f.currency, interval: f.interval, baseFeeMinor: minor });
  }
  create.mutate(
    { code: code.value.trim(), name: name.value.trim(), baseFeeMinor: base, fees },
    {
      onSuccess: () => {
        code.value = name.value = "";
        fee.value = "0";
        extra.value = [];
        creating.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-if="canWrite" v-model:open="creating" name="new-package">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.packages.new") }}</SheetTitle>
        <SheetDescription>{{ t("billing.packages.intro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-package" class="grid gap-3" role="form" :aria-label="t('billing.packages.formLabel')" @submit.prevent="submit">
          <Input v-model="code" :aria-label="t('billing.packages.code')" :placeholder="t('billing.packages.code')" pattern="[a-z][a-z0-9_\-]{0,62}" required />
          <Input v-model="name" :aria-label="t('billing.packages.name')" :placeholder="t('billing.packages.name')" required />
          <Input v-model="fee" inputmode="decimal" :aria-label="t('billing.packages.fee', { currency: tenantCurrency })" :placeholder="t('billing.packages.fee', { currency: tenantCurrency })" required />
          <div v-for="(f, i) in extra" :key="i" class="grid grid-cols-[7rem_1fr_auto] gap-2">
            <CurrencySelect v-model="f.currency" :aria-label="t('billing.packages.extraCurrency')" />
            <NativeSelect v-model="f.interval" :aria-label="t('billing.packages.interval')">
              <option value="month">{{ t("billing.packages.monthly") }}</option>
              <option value="year">{{ t("billing.packages.yearly") }}</option>
            </NativeSelect>
            <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="extra.splice(i, 1)"><X class="size-4" /></Button>
            <Input v-model="f.amount" class="col-span-3" inputmode="decimal" :aria-label="t('billing.packages.extraFee', { n: i + 1 })" required />
          </div>
          <div><Button type="button" variant="outline" @click="addFee"><Plus class="size-4" />{{ t("billing.packages.addFee") }}</Button></div>
          <ErrorText :message="formError" />
          <ErrorText :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="creating = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-package" :disabled="create.isPending.value || tenantCurrency === ''">{{ t("billing.packages.publish") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Card class="p-3">
    <ErrorText :error="packages.error.value" class="px-3" />
    <DataToolbar v-model:search="table.search.value" :active="active" @clear="clear">
      <DateRangePicker v-model="range" />
      <template v-if="canWrite" #actions>
        <Button data-testid="packages-new" @click="creating = true"><Plus class="size-4" />{{ t("billing.packages.new") }}</Button>
      </template>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.packages.code") }}</TableHead>
          <TableHead>{{ t("billing.packages.name") }}</TableHead>
          <TableHead>{{ t("billing.packages.version") }}</TableHead>
          <TableHead>{{ t("billing.packages.published") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.packages.fixedFee") }}</TableHead>
          <TableHead>{{ t("billing.packages.otherFees") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.packages.meters") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(p, i) in table.items.value" :key="p.plan.id" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" @click="table.open(i)">
          <TableCell class="mono font-medium">{{ p.plan.code }}</TableCell>
          <TableCell>{{ p.plan.name }}</TableCell>
          <TableCell>v{{ p.version?.version ?? "—" }}</TableCell>
          <TableCell class="whitespace-nowrap">{{ fmt.date(p.version?.publish_time, "day", "—") }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ p.version ? formatMinor(p.version.base_fee_minor, tenantCurrency, locale) : "—" }}</TableCell>
          <TableCell>
            <div class="flex flex-wrap gap-1">
              <Badge v-for="f in p.version?.fees ?? []" :key="`${f.currency}-${f.interval}`" variant="secondary" class="tabular-nums">
                {{ formatMinor(f.base_fee_minor, f.currency, locale) }}<template v-if="f.interval === 'SUBSCRIPTION_INTERVAL_YEAR'"> {{ t("billing.packages.perYear") }}</template>
              </Badge>
              <span v-if="(p.version?.fees ?? []).length === 0" class="text-muted-foreground">—</span>
            </div>
          </TableCell>
          <TableCell class="text-end tabular-nums">{{ (p.version?.prices ?? []).length }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: Eye, onSelect: () => table.open(i) }]" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!packages.isPending.value && (packages.data.value ?? []).length === 0 && !packages.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.packages.empty") }}
    </p>
    <p v-else-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.plan.name || current?.plan.code || ''" :description="current ? `${current.plan.code} · v${current.version?.version ?? '—'}` : undefined">
    <div v-if="current" class="flex flex-col gap-5" data-testid="package-detail">
      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
        <dt class="text-muted-foreground">{{ t("billing.packages.fixedFee") }}</dt>
        <dd class="text-end text-lg font-semibold tabular-nums">{{ current.version ? formatMinor(current.version.base_fee_minor, tenantCurrency, locale) : "—" }}</dd>
        <dt class="text-muted-foreground">{{ t("billing.packages.published") }}</dt>
        <dd class="text-end">{{ fmt.date(current.version?.publish_time, "day", "—") }}</dd>
        <dt class="text-muted-foreground">{{ t("billing.packages.otherFees") }}</dt>
        <dd class="flex flex-wrap justify-end gap-1">
          <Badge v-for="f in current.version?.fees ?? []" :key="`${f.currency}-${f.interval}`" variant="secondary" class="tabular-nums">
            {{ formatMinor(f.base_fee_minor, f.currency, locale) }}<template v-if="f.interval === 'SUBSCRIPTION_INTERVAL_YEAR'"> {{ t("billing.packages.perYear") }}</template>
          </Badge>
          <span v-if="(current.version?.fees ?? []).length === 0" class="text-muted-foreground">—</span>
        </dd>
      </dl>
      <section v-if="(current.version?.prices ?? []).length" :aria-label="t('billing.packages.meters')">
        <h3 class="mb-2 text-sm font-semibold">{{ t("billing.packages.meters") }}</h3>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("billing.subscriptions.meter") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.packages.priceColumn") }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="pr in current.version?.prices ?? []" :key="pr.meter_code">
              <TableCell class="mono">{{ pr.meter_code }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ priceText(pr) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </section>
      <section v-if="(current.version?.entitlements ?? []).length" :aria-label="t('billing.packages.limits')">
        <h3 class="mb-2 text-sm font-semibold">{{ t("billing.packages.limits") }}</h3>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("billing.subscriptions.meter") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.subscriptions.limit") }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="e in current.version?.entitlements ?? []" :key="e.meter_code">
              <TableCell class="mono">{{ e.meter_code }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ e.limit === "0" ? t("billing.subscriptions.unlimited") : e.limit }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </section>
    </div>
  </DetailSheet>
</template>
