<script setup lang="ts">
import { Plus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useTenantCurrency } from "@/features/tenants/currency";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";
import { useFormat } from "@/shared/lib/format";
import { parseMajor } from "@/shared/lib/money";
import { CREDITS_RE, CREDIT_VALIDITY_MAX_DAYS } from "@/shared/lib/rules";
import { useSeededRef } from "@/shared/lib/seeded";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreateCreditPack, useCreditPacks, useSetCreditPackActive } from "./api";
import { formatCredits, validityDays } from "./lib";

/** The packs of credits a tenant sells: a form to add one (priced in major units) and the table of them, each of which can be withdrawn from sale or offered by a purchase link. */
const props = defineProps<{ tenantId: string }>();
const emit = defineEmits<{ purchaseLink: [pack: { id: string; name: string }] }>();
const { t, locale } = useI18n();
const fmt = useFormat();

const packs = useCreditPacks(() => props.tenantId);
const createPack = useCreateCreditPack(() => props.tenantId);
const setActive = useSetCreditPackActive(() => props.tenantId);
const packName = ref("");
const packCredits = ref("");
const packPrice = ref("");
// The pack is priced in the tenant's own currency unless the person picks another one.
const packCurrency = useSeededRef<string>(useTenantCurrency(() => props.tenantId), FALLBACK_CURRENCY);
const packValidity = ref("");
const priceMinor = computed(() => parseMajor(packPrice.value, packCurrency.value));
const creditsValid = computed(() => CREDITS_RE.test(packCredits.value.trim()));
const validity = computed(() => validityDays(packValidity.value, CREDIT_VALIDITY_MAX_DAYS));
const validityValid = computed(() => validity.value !== undefined);

const open = ref(false);
type Pack = NonNullable<NonNullable<typeof packs.data.value>["packs"]>[number];
const table = useTableState<Pack>(() => packs.data.value?.packs ?? [], {
  searchFields: (p) => [p.name],
  filters: { state: (p, v) => (v === "active" ? !!p.active : !p.active) },
});
const stateFilter = computed({ get: () => table.filters.value.state ?? "", set: (v) => (table.filters.value.state = String(v)) });

// A sheet that was closed opens clean: the last values and error are not shown again.
watch(open, (v) => {
  if (!v) return;
  packName.value = packCredits.value = packPrice.value = packValidity.value = "";
  createPack.reset();
});

function submitPack() {
  const days = validity.value;
  if (!priceMinor.value || !creditsValid.value || days === undefined) return;
  createPack.mutate(
    { name: packName.value.trim(), credits: packCredits.value.trim(), price_minor: priceMinor.value, currency: packCurrency.value, ...(days !== null ? { validity_days: days } : {}) },
    {
      onSuccess: () => {
        packName.value = packCredits.value = packPrice.value = packValidity.value = "";
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Card class="mb-6 p-3">
    <div class="px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.credits.packs.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.credits.packs.hint") }}</p>
    </div>
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" class="pt-3" @clear="table.clear()">
      <NativeSelect v-model="stateFilter" class="w-44" :aria-label="t('billing.credits.packs.state')">
        <option value="">{{ t("billing.credits.packs.state") }}</option>
        <option value="active">{{ t("billing.credits.packs.onSale") }}</option>
        <option value="inactive">{{ t("billing.credits.packs.withdrawn") }}</option>
      </NativeSelect>
      <template #actions>
        <Button type="button" @click="open = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.credits.packs.create") }}</Button>
      </template>
    </DataToolbar>
    <ErrorText :error="packs.error.value" class="px-3" />
    <p v-if="packs.data.value && !(packs.data.value.packs ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.credits.packs.none") }}</p>
    <template v-else>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.credits.packs.name") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.packs.credits") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.packs.price") }}</TableHead>
            <TableHead>{{ t("billing.credits.packs.validityCol") }}</TableHead>
            <TableHead>{{ t("billing.credits.packs.state") }}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="p in table.items.value" :key="p.id">
            <TableCell class="font-medium">{{ p.name }}</TableCell>
            <TableCell class="text-end">{{ formatCredits(p.credits, locale) }}</TableCell>
            <TableCell class="text-end">{{ fmt.money(p.price_minor, p.currency) }}</TableCell>
            <TableCell>{{ p.validity_days ? t("billing.credits.packs.days", { n: p.validity_days }) : t("billing.credits.packs.noExpiry") }}</TableCell>
            <TableCell>
              <Badge :variant="p.active ? 'success' : 'secondary'">{{ p.active ? t("billing.credits.packs.onSale") : t("billing.credits.packs.withdrawn") }}</Badge>
            </TableCell>
            <TableCell class="space-x-2 text-end">
              <Button v-if="p.active" type="button" size="sm" @click="emit('purchaseLink', { id: p.id ?? '', name: p.name ?? '' })">{{ t("billing.credits.packs.purchaseLink") }}</Button>
              <Button type="button" variant="outline" size="sm" :disabled="setActive.isPending.value" @click="setActive.mutate({ id: p.id ?? '', active: !p.active })">
                {{ p.active ? t("billing.credits.packs.withdraw") : t("billing.credits.packs.restore") }}
              </Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="table.total.value === 0 && packs.data.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
      <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
    </template>
    <ErrorText :error="setActive.error.value" class="px-3" />

    <Sheet v-model:open="open">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("billing.credits.packs.new") }}</SheetTitle>
          <SheetDescription>{{ t("billing.credits.packs.hint") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="credit-pack-form" class="grid gap-4" role="form" :aria-label="t('billing.credits.packs.formLabel')" @submit.prevent="submitPack">
            <Input v-model="packName" :aria-label="t('billing.credits.packs.name')" :placeholder="t('billing.credits.packs.name')" maxlength="80" required />
            <Input v-model="packCredits" inputmode="numeric" :aria-label="t('billing.credits.packs.credits')" :placeholder="t('billing.credits.packs.credits')" required />
            <Input v-model="packPrice" inputmode="decimal" :aria-label="t('billing.credits.packs.price')" :placeholder="t('billing.credits.packs.price')" required />
            <CurrencySelect v-model="packCurrency" :aria-label="t('billing.credits.packs.currency')" />
            <Input v-model="packValidity" inputmode="numeric" :aria-label="t('billing.credits.packs.validity')" :placeholder="t('billing.credits.packs.validity')" />
            <ErrorText :error="createPack.error.value" />
          </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="credit-pack-form" :disabled="createPack.isPending.value || !priceMinor || !creditsValid || !validityValid">{{ t("billing.credits.packs.create") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </Card>
</template>
