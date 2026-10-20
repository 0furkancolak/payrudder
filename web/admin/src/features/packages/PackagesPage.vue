<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { Plus, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { CURRENCIES } from "@/shared/lib/currencies";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { useTenant } from "@/features/tenants";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreatePackage, usePackages } from "./api";

const { t, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);
const packages = usePackages(tenantId);
const create = useCreatePackage(tenantId);

const code = ref("");
const name = ref("");
const fee = ref("0");
const extra = ref<{ currency: string; interval: "month" | "year"; amount: string }[]>([]);
const formError = ref("");
const tenantCurrency = computed(() => tenant.data.value?.currency ?? "");

function addFee() {
  extra.value.push({ currency: tenantCurrency.value || "USD", interval: "year", amount: "0" });
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
      },
    },
  );
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.packages.new") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.packages.intro") }}</p>
    <form class="grid gap-3" role="form" :aria-label="t('billing.packages.formLabel')" @submit.prevent="submit">
      <div class="grid gap-3 sm:grid-cols-3">
        <Input v-model="code" :aria-label="t('billing.packages.code')" :placeholder="t('billing.packages.code')" pattern="[a-z][a-z0-9_\-]{0,62}" required />
        <Input v-model="name" :aria-label="t('billing.packages.name')" :placeholder="t('billing.packages.name')" required />
        <Input v-model="fee" inputmode="decimal" :aria-label="t('billing.packages.fee', { currency: tenantCurrency })" :placeholder="t('billing.packages.fee', { currency: tenantCurrency })" required />
      </div>
      <div v-for="(f, i) in extra" :key="i" class="grid grid-cols-[8rem_9rem_1fr_auto] gap-3">
        <NativeSelect v-model="f.currency" :aria-label="t('billing.packages.extraCurrency')">
          <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
        </NativeSelect>
        <NativeSelect v-model="f.interval" :aria-label="t('billing.packages.interval')">
          <option value="month">{{ t("billing.packages.monthly") }}</option>
          <option value="year">{{ t("billing.packages.yearly") }}</option>
        </NativeSelect>
        <Input v-model="f.amount" inputmode="decimal" :aria-label="t('billing.packages.extraFee', { n: i + 1 })" required />
        <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="extra.splice(i, 1)"><X class="size-4" /></Button>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" @click="addFee"><Plus class="size-4" />{{ t("billing.packages.addFee") }}</Button>
        <Button type="submit" :disabled="create.isPending.value || tenantCurrency === ''">{{ t("billing.packages.publish") }}</Button>
      </div>
    </form>
    <p v-if="formError" class="text-destructive text-sm" role="alert">{{ formError }}</p>
    <p v-if="create.error.value" class="text-destructive text-sm" role="alert">{{ errorText(create.error.value) }}</p>
  </Card>

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.tabs.packages") }}</h2>
    <p v-if="packages.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(packages.error.value) }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.packages.code") }}</TableHead>
          <TableHead>{{ t("billing.packages.name") }}</TableHead>
          <TableHead>{{ t("billing.packages.version") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.packages.fixedFee") }}</TableHead>
          <TableHead>{{ t("billing.packages.otherFees") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.packages.meters") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="p in packages.data.value ?? []" :key="p.plan.id">
          <TableCell class="mono font-medium">{{ p.plan.code }}</TableCell>
          <TableCell>{{ p.plan.name }}</TableCell>
          <TableCell>v{{ p.version?.version ?? "—" }}</TableCell>
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
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!packages.isPending.value && (packages.data.value ?? []).length === 0 && !packages.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.packages.empty") }}
    </p>
  </Card>
</template>
