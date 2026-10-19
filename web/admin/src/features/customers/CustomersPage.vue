<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { CURRENCIES } from "@/shared/lib/currencies";
import { useCreateCustomer, useCustomers } from "./api";

const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const customers = useCustomers(tenantId);
const create = useCreateCustomer(tenantId);

const externalId = ref("");
const name = ref("");
const email = ref("");
const country = ref("");
const currency = ref("");

function submit() {
  create.mutate(
    {
      external_id: externalId.value.trim(),
      name: name.value.trim(),
      email: email.value.trim(),
      country: country.value.trim().toUpperCase() || undefined,
      billing_currency: currency.value || undefined,
    },
    {
      onSuccess: () => {
        externalId.value = name.value = email.value = country.value = currency.value = "";
      },
    },
  );
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.customers.new") }}</h2>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5" role="form" :aria-label="t('billing.customers.formLabel')" @submit.prevent="submit">
      <Input v-model="externalId" :aria-label="t('billing.customers.externalId')" :placeholder="t('billing.customers.externalId')" maxlength="128" required />
      <Input v-model="name" :aria-label="t('billing.customers.name')" :placeholder="t('billing.customers.name')" required />
      <Input v-model="email" type="email" :aria-label="t('billing.customers.email')" :placeholder="t('billing.customers.email')" required />
      <Input v-model="country" :aria-label="t('billing.customers.country')" :placeholder="t('billing.customers.countryPlaceholder')" maxlength="2" pattern="[A-Za-z]{2}" />
      <NativeSelect v-model="currency" :aria-label="t('billing.customers.currency')">
        <option value="">{{ t("billing.customers.tenantCurrency") }}</option>
        <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
      <Button type="submit" class="sm:col-span-2 lg:col-span-5" :disabled="create.isPending.value">{{ t("billing.customers.create") }}</Button>
    </form>
    <p v-if="create.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(create.error.value).message }}</p>
  </Card>

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.tabs.customers") }}</h2>
    <p v-if="customers.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(customers.error.value).message }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.customers.externalId") }}</TableHead>
          <TableHead>{{ t("billing.customers.name") }}</TableHead>
          <TableHead>{{ t("billing.customers.email") }}</TableHead>
          <TableHead>{{ t("billing.customers.country") }}</TableHead>
          <TableHead>{{ t("billing.customers.currency") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="c in customers.data.value?.customers ?? []" :key="c.id">
          <TableCell class="font-medium">{{ c.external_id }}</TableCell>
          <TableCell>{{ c.name }}</TableCell>
          <TableCell>{{ c.email }}</TableCell>
          <TableCell>{{ c.country || "—" }}</TableCell>
          <TableCell>{{ c.billing_currency || "—" }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!customers.isPending.value && (customers.data.value?.customers ?? []).length === 0 && !customers.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.customers.empty") }}
    </p>
  </Card>
</template>
