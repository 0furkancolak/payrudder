<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { CopyPlus, Eye, Plus, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useCanWrite } from "@/shared/lib/access";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreateMeter, useMeters, type NewMeter } from "./api";
import { AGGREGATIONS, categories, UNIT_SUGGESTIONS, type Aggregation, type Meter } from "./quota";

/**
 * The meters of a tenant: what is measured, how events add up, in which unit, and under which category of the tenant's own. A meter is versioned: a new
 * version of a code can change everything but its decimal places, and events are checked against the latest one.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const canWrite = useCanWrite();
const fmt = useFormat();
const meters = useMeters(tenantId);
const create = useCreateMeter(tenantId);

const list = computed(() => meters.data.value ?? []);
const inUse = computed(() => categories(list.value));
const aggWord = (a: string | undefined) => (a ?? "").replace("AGGREGATION_", "").toLowerCase();
const table = useTableState(list, {
  searchFields: (m) => [m.code, m.category, m.unit_label, m.value_property],
  filters: { category: (m, v) => (m.category ?? "").trim() === v, aggregation: (m, v) => m.aggregation === v },
});
const index = ref<number | null>(null);
const current = computed(() => (index.value === null ? undefined : table.items.value[index.value]));
const filterText = (m: Meter) =>
  Object.entries(m.filter ?? {})
    .map(([k, v]) => `${k} = ${(v.values ?? []).join(" | ")}`)
    .join("; ");

// --- the form: a new meter, or the next version of one (code and decimal places fixed)
const open = ref(false);
const versionOf = ref("");
const code = ref("");
const category = ref("");
const aggregation = ref<Aggregation>("AGGREGATION_SUM");
const valueProperty = ref("");
const unit = ref("");
const scale = ref("0");
const filters = ref<{ property: string; values: string }[]>([]);
const formError = ref("");

function start(m?: Meter) {
  versionOf.value = m?.code ?? "";
  code.value = m?.code ?? "";
  category.value = m?.category ?? "";
  aggregation.value = m?.aggregation ?? "AGGREGATION_SUM";
  valueProperty.value = m?.value_property ?? "";
  unit.value = m?.unit_label ?? "";
  scale.value = String(m?.quantity_scale ?? 0);
  filters.value = Object.entries(m?.filter ?? {}).map(([property, v]) => ({ property, values: (v.values ?? []).join(", ") }));
  formError.value = "";
  create.reset();
  open.value = true;
}
watch(open, (v) => !v && (versionOf.value = ""));

function submit() {
  formError.value = "";
  const filter: NonNullable<NewMeter["filter"]> = {};
  for (const f of filters.value) {
    const values = f.values
      .split(",")
      .map((v) => v.trim())
      .filter((v) => v !== "");
    if (f.property.trim() === "" || values.length === 0) {
      formError.value = t("billing.meters.badFilter");
      return;
    }
    filter[f.property.trim()] = { values };
  }
  const body: NewMeter = {
    code: code.value.trim(),
    aggregation: aggregation.value,
    unit_label: unit.value.trim(),
    quantity_scale: Number(scale.value),
    ...(aggregation.value === "AGGREGATION_COUNT" ? {} : { value_property: valueProperty.value.trim() }),
    ...(category.value.trim() ? { category: category.value.trim() } : {}),
    ...(Object.keys(filter).length ? { filter } : {}),
  };
  create.mutate(body, { onSuccess: () => (open.value = false) });
}
</script>

<template>
  <Sheet v-if="canWrite" v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ versionOf ? t("billing.meters.newVersionOf", { code: versionOf }) : t("billing.meters.new") }}</SheetTitle>
        <SheetDescription>{{ t("billing.meters.intro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-meter" class="grid gap-3" role="form" :aria-label="t('billing.meters.formLabel')" @submit.prevent="submit">
          <Input v-model="code" :disabled="versionOf !== ''" :aria-label="t('billing.meters.code')" :placeholder="t('billing.meters.codePlaceholder')" pattern="[a-z][a-z0-9_]{0,62}" required />
          <Input v-model="category" list="meter-categories" maxlength="64" :aria-label="t('billing.meters.category')" :placeholder="t('billing.meters.categoryPlaceholder')" />
          <datalist id="meter-categories">
            <option v-for="c in inUse" :key="c" :value="c" />
          </datalist>
          <label class="grid gap-1 text-sm">
            <span class="text-muted-foreground">{{ t("billing.meters.aggregation") }}</span>
            <NativeSelect v-model="aggregation" :aria-label="t('billing.meters.aggregation')">
              <option v-for="a in AGGREGATIONS" :key="a" :value="a">{{ t(`billing.meters.aggregations.${aggWord(a)}`) }}</option>
            </NativeSelect>
            <span class="text-muted-foreground text-xs">{{ t(`billing.meters.aggregationHelp.${aggWord(aggregation)}`) }}</span>
          </label>
          <Input v-if="aggregation !== 'AGGREGATION_COUNT'" v-model="valueProperty" :aria-label="t('billing.meters.valueProperty')" :placeholder="t('billing.meters.valuePropertyPlaceholder')" required />
          <div class="grid grid-cols-[1fr_8rem] gap-2">
            <Input v-model="unit" list="meter-units" maxlength="32" :aria-label="t('billing.meters.unit')" :placeholder="t('billing.meters.unitPlaceholder')" required />
            <NativeSelect v-model="scale" :disabled="versionOf !== ''" :aria-label="t('billing.meters.scale')">
              <option v-for="n in 7" :key="n" :value="String(n - 1)">{{ t("billing.meters.decimals", { n: n - 1 }) }}</option>
            </NativeSelect>
          </div>
          <datalist id="meter-units">
            <option v-for="u in UNIT_SUGGESTIONS" :key="u" :value="u" />
          </datalist>
          <fieldset class="grid gap-2">
            <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.meters.filter") }}</legend>
            <p class="text-muted-foreground text-xs">{{ t("billing.meters.filterHelp") }}</p>
            <div v-for="(f, i) in filters" :key="i" class="grid grid-cols-[1fr_1.5fr_auto] gap-2">
              <Input v-model="f.property" :aria-label="t('billing.meters.filterProperty', { n: i + 1 })" :placeholder="t('billing.meters.filterPropertyPlaceholder')" />
              <Input v-model="f.values" :aria-label="t('billing.meters.filterValues', { n: i + 1 })" :placeholder="t('billing.meters.filterValuesPlaceholder')" />
              <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="filters.splice(i, 1)"><X class="size-4" /></Button>
            </div>
            <div><Button type="button" variant="outline" size="sm" @click="filters.push({ property: '', values: '' })"><Plus class="size-4" />{{ t("billing.meters.addFilter") }}</Button></div>
          </fieldset>
          <ErrorText :message="formError" />
          <ErrorText :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-meter" :disabled="create.isPending.value">{{ versionOf ? t("billing.meters.saveVersion") : t("billing.meters.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Card class="p-3">
    <ErrorText :error="meters.error.value" class="px-3" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <FilterSelect v-model="table.filters.value.category" :label="t('billing.meters.category')" :options="[{ value: '', label: t('billing.meters.allCategories') }, ...inUse.map((c) => ({ value: c }))]" />
      <FilterSelect
        v-model="table.filters.value.aggregation"
        :label="t('billing.meters.aggregation')"
        :options="[{ value: '', label: t('billing.meters.allAggregations') }, ...AGGREGATIONS.map((a) => ({ value: a, label: t(`billing.meters.aggregations.${aggWord(a)}`) }))]"
      />
      <template v-if="canWrite" #actions>
        <Button data-testid="meters-new" @click="start()"><Plus class="size-4" />{{ t("billing.meters.new") }}</Button>
      </template>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.meters.code") }}</TableHead>
          <TableHead>{{ t("billing.meters.category") }}</TableHead>
          <TableHead>{{ t("billing.meters.aggregation") }}</TableHead>
          <TableHead>{{ t("billing.meters.unit") }}</TableHead>
          <TableHead>{{ t("billing.packages.version") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(m, i) in table.items.value" :key="m.code" class="cursor-pointer" :data-state="index === i ? 'selected' : undefined" @click="index = i">
          <TableCell class="mono font-medium">{{ m.code }}</TableCell>
          <TableCell><Badge v-if="m.category" variant="secondary">{{ m.category }}</Badge><span v-else class="text-muted-foreground">—</span></TableCell>
          <TableCell>{{ t(`billing.meters.aggregations.${aggWord(m.aggregation)}`) }}<span v-if="m.value_property" class="text-muted-foreground mono"> · {{ m.value_property }}</span></TableCell>
          <TableCell>{{ m.unit_label }}</TableCell>
          <TableCell>v{{ m.version ?? 1 }}</TableCell>
          <TableCell class="text-end" @click.stop>
            <RowActions
              :actions="[
                { key: 'view', label: t('common.view'), icon: Eye, onSelect: () => (index = i) },
                ...(canWrite ? [{ key: 'version', label: t('billing.meters.newVersion'), icon: CopyPlus, onSelect: () => start(m) }] : []),
              ]"
            />
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!meters.isPending.value && list.length === 0 && !meters.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.meters.empty") }}</p>
    <p v-else-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="index" :total="table.items.value.length" :title="current?.code ?? ''" :description="current?.category || undefined">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm" data-testid="meter-detail">
      <dt class="text-muted-foreground">{{ t("billing.meters.aggregation") }}</dt>
      <dd class="text-end">{{ t(`billing.meters.aggregations.${aggWord(current.aggregation)}`) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.meters.valueProperty") }}</dt>
      <dd class="mono text-end">{{ current.value_property || "—" }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.meters.unit") }}</dt>
      <dd class="text-end">{{ current.unit_label }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.meters.scale") }}</dt>
      <dd class="text-end">{{ t("billing.meters.decimals", { n: current.quantity_scale ?? 0 }) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.meters.filter") }}</dt>
      <dd class="mono text-end">{{ filterText(current) || t("billing.meters.noFilter") }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.packages.version") }}</dt>
      <dd class="text-end">v{{ current.version ?? 1 }} · {{ fmt.date(current.create_time, "day", "—") }}</dd>
    </dl>
  </DetailSheet>
</template>
