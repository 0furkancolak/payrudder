<script setup lang="ts">
import { useParams, useRouter } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { CURRENCIES } from "@/shared/lib/currencies";
import CustomerLinksCard from "@/features/paymentlinks/CustomerLinksCard.vue";
import { addressLine, useCreateCustomer, useCustomers, useSetCustomerAddress, type Address } from "./api";

const { t } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const customers = useCustomers(tenantId);
const router = useRouter();
const portalFor = ref("");
function paymentLink(externalId?: string) {
  void router.navigate({ to: "/tenants/$id/payment-links", params: { id: tenantId.value }, search: { customer: externalId ?? "" } as never });
}
const create = useCreateCustomer(tenantId);

const externalId = ref("");
const name = ref("");
const email = ref("");
const country = ref("");
const currency = ref("");
const address = ref<Address>({});
const trimmed = (a: Address): Address => Object.fromEntries(Object.entries(a).map(([k, v]) => [k, (v ?? "").trim()]).filter(([, v]) => v !== ""));

// --- editing the address of a customer in the list
const setAddress = useSetCustomerAddress(tenantId);
const editing = ref("");
const editAddress = ref<Address>({});
function edit(c: { external_id?: string; address?: Address }) {
  editing.value = c.external_id ?? "";
  editAddress.value = { ...(c.address ?? {}) };
}
function saveAddress() {
  const a = trimmed(editAddress.value);
  if (a.country) a.country = a.country.toUpperCase();
  setAddress.mutate({ externalId: editing.value, address: a }, { onSuccess: () => (editing.value = "") });
}

function submit() {
  create.mutate(
    {
      external_id: externalId.value.trim(),
      name: name.value.trim(),
      email: email.value.trim(),
      country: country.value.trim().toUpperCase() || undefined,
      billing_currency: currency.value || undefined,
      address: Object.keys(trimmed(address.value)).length ? { ...trimmed(address.value), ...(country.value.trim() ? { country: country.value.trim().toUpperCase() } : {}) } : undefined,
    },
    {
      onSuccess: () => {
        externalId.value = name.value = email.value = country.value = currency.value = "";
        address.value = {};
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
      <fieldset class="grid gap-3 sm:col-span-2 sm:grid-cols-2 lg:col-span-5 lg:grid-cols-5">
        <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.customers.address.title") }}</legend>
        <Input v-model="address.line1" maxlength="120" :aria-label="t('billing.customers.address.line1')" :placeholder="t('billing.customers.address.line1')" />
        <Input v-model="address.line2" maxlength="120" :aria-label="t('billing.customers.address.line2')" :placeholder="t('billing.customers.address.line2')" />
        <Input v-model="address.city" maxlength="120" :aria-label="t('billing.customers.address.city')" :placeholder="t('billing.customers.address.city')" />
        <Input v-model="address.region" maxlength="120" :aria-label="t('billing.customers.address.region')" :placeholder="t('billing.customers.address.region')" />
        <Input v-model="address.postal_code" maxlength="20" :aria-label="t('billing.customers.address.postalCode')" :placeholder="t('billing.customers.address.postalCode')" />
      </fieldset>
      <Button type="submit" class="sm:col-span-2 lg:col-span-5" :disabled="create.isPending.value">{{ t("billing.customers.create") }}</Button>
    </form>
    <p v-if="create.error.value" class="text-destructive text-sm" role="alert">{{ errorText(create.error.value) }}</p>
  </Card>

  <Card v-if="editing" class="mb-6" role="region" :aria-label="t('billing.customers.address.editing', { id: editing })">
    <h2 class="text-lg font-semibold">{{ t("billing.customers.address.editing", { id: editing }) }}</h2>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" @submit.prevent="saveAddress">
      <Input v-model="editAddress.line1" maxlength="120" :aria-label="t('billing.customers.address.line1')" :placeholder="t('billing.customers.address.line1')" />
      <Input v-model="editAddress.line2" maxlength="120" :aria-label="t('billing.customers.address.line2')" :placeholder="t('billing.customers.address.line2')" />
      <Input v-model="editAddress.city" maxlength="120" :aria-label="t('billing.customers.address.city')" :placeholder="t('billing.customers.address.city')" />
      <Input v-model="editAddress.region" maxlength="120" :aria-label="t('billing.customers.address.region')" :placeholder="t('billing.customers.address.region')" />
      <Input v-model="editAddress.postal_code" maxlength="20" :aria-label="t('billing.customers.address.postalCode')" :placeholder="t('billing.customers.address.postalCode')" />
      <Input v-model="editAddress.country" maxlength="2" pattern="[A-Za-z]{2}" :aria-label="t('billing.customers.country')" :placeholder="t('billing.customers.countryPlaceholder')" />
      <div class="flex gap-2">
        <Button type="submit" :disabled="setAddress.isPending.value">{{ t("billing.customers.address.save") }}</Button>
        <Button type="button" variant="outline" @click="editing = ''">{{ t("billing.customers.address.cancel") }}</Button>
      </div>
    </form>
    <p v-if="setAddress.error.value" class="text-destructive text-sm" role="alert">{{ errorText(setAddress.error.value) }}</p>
  </Card>

  <CustomerLinksCard v-if="portalFor" :tenant-id="tenantId" :customer="portalFor" @close="portalFor = ''" />

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.tabs.customers") }}</h2>
    <p v-if="customers.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(customers.error.value) }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.customers.externalId") }}</TableHead>
          <TableHead>{{ t("billing.customers.name") }}</TableHead>
          <TableHead>{{ t("billing.customers.email") }}</TableHead>
          <TableHead>{{ t("billing.customers.country") }}</TableHead>
          <TableHead>{{ t("billing.customers.currency") }}</TableHead>
          <TableHead>{{ t("billing.customers.address.title") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.paymentLinks.links") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="c in customers.data.value?.customers ?? []" :key="c.id">
          <TableCell class="font-medium">{{ c.external_id }}</TableCell>
          <TableCell>{{ c.name }}</TableCell>
          <TableCell>{{ c.email }}</TableCell>
          <TableCell>{{ c.country || "—" }}</TableCell>
          <TableCell>{{ c.billing_currency || "—" }}</TableCell>
          <TableCell class="max-w-64">
            <span class="text-sm">{{ addressLine(c.address) || "—" }}</span>
            <Button size="sm" variant="ghost" @click="edit(c)">{{ t("billing.customers.address.edit") }}</Button>
          </TableCell>
          <TableCell class="w-40 text-end">
<div class="flex flex-col items-end gap-1">
            <Button size="sm" variant="outline" :aria-label="t('billing.paymentLinks.portal.open') + ' ' + c.external_id" @click="portalFor = c.external_id ?? ''">{{ t("billing.paymentLinks.portal.open") }}</Button>
            <Button size="sm" variant="outline" :aria-label="t('billing.paymentLinks.paymentLink') + ' ' + c.external_id" @click="paymentLink(c.external_id)">{{ t("billing.paymentLinks.paymentLink") }}</Button>
          </div>
</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!customers.isPending.value && (customers.data.value?.customers ?? []).length === 0 && !customers.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.customers.empty") }}
    </p>
  </Card>
</template>
