<script setup lang="ts">
import { useParams, useRouter } from "@tanstack/vue-router";
import { Plus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { maskEmail } from "@/shared/lib/money";
import { tenantBase } from "@/shared/lib/session";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import CustomerLinksCard from "@/features/paymentlinks/CustomerLinksCard.vue";
import { addressLine, useCreateCustomer, useCustomers, useSetCustomerAddress, useSetCustomerTax, type Address, type CustomerTax } from "./api";
import { CurrencySelect } from "@/shared/ui/search-select";

const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const customers = useCustomers(tenantId);
const router = useRouter();
const portalFor = ref("");
function paymentLink(externalId?: string) {
  // A person reaches their tenants under /app/t, the operator under /tenants.
  void router.navigate({ to: `${tenantBase()}/$id/payment-links`, params: { id: tenantId.value }, search: { customer: externalId ?? "" } as never });
}
const create = useCreateCustomer(tenantId);

const list = computed(() => customers.data.value?.customers ?? []);
const table = useTableState(list, {
  searchFields: (c) => [c.external_id, c.name, c.email, c.tax_id],
  filters: { country: (c, v) => (c.country ?? "") === v, currency: (c, v) => (c.billing_currency ?? "") === v },
});
const distinct = (pick: (c: (typeof list.value)[number]) => string | undefined) => computed(() => [...new Set(list.value.map((c) => pick(c) ?? "").filter(Boolean))].sort());
const countries = distinct((c) => c.country);
const currencies = distinct((c) => c.billing_currency);

const creating = ref(false);
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
const addressOpen = computed({ get: () => editing.value !== "", set: (v) => { if (!v) editing.value = ""; } });
function edit(c: { external_id?: string; address?: Address }) {
  editing.value = c.external_id ?? "";
  editAddress.value = { ...(c.address ?? {}) };
}
function saveAddress() {
  const a = trimmed(editAddress.value);
  if (a.country) a.country = a.country.toUpperCase();
  setAddress.mutate({ externalId: editing.value, address: a }, { onSuccess: () => (editing.value = "") });
}

// --- editing the tax data (for e-invoices) of a customer in the list
const setTax = useSetCustomerTax(tenantId);
const editingTax = ref("");
const editTax = ref<CustomerTax>({});
const taxOpen = computed({ get: () => editingTax.value !== "", set: (v) => { if (!v) editingTax.value = ""; } });
function editTaxOf(c: { external_id?: string } & CustomerTax) {
  editingTax.value = c.external_id ?? "";
  editTax.value = { tax_id: c.tax_id ?? "", tax_office: c.tax_office ?? "", e_invoice_registered: c.e_invoice_registered ?? false };
}
function saveTax() {
  setTax.mutate(
    { externalId: editingTax.value, tax: { tax_id: (editTax.value.tax_id ?? "").trim(), tax_office: (editTax.value.tax_office ?? "").trim(), e_invoice_registered: !!editTax.value.e_invoice_registered } },
    { onSuccess: () => (editingTax.value = "") },
  );
}

// A sheet that was closed opens clean: the last values and error are not shown again.
watch(creating, (v) => {
  if (v) {
    externalId.value = name.value = email.value = country.value = currency.value = "";
    address.value = {};
    create.reset();
  }
});
watch(editing, (v) => v && setAddress.reset());
watch(editingTax, (v) => v && setTax.reset());

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
        creating.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="creating">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.new") }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.address.title") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-customer" class="grid gap-3" role="form" :aria-label="t('billing.customers.formLabel')" @submit.prevent="submit">
          <Input v-model="externalId" :aria-label="t('billing.customers.externalId')" :placeholder="t('billing.customers.externalId')" maxlength="128" required />
          <Input v-model="name" :aria-label="t('billing.customers.name')" :placeholder="t('billing.customers.name')" required />
          <Input v-model="email" type="email" :aria-label="t('billing.customers.email')" :placeholder="t('billing.customers.email')" required />
          <Input v-model="country" :aria-label="t('billing.customers.country')" :placeholder="t('billing.customers.countryPlaceholder')" maxlength="2" pattern="[A-Za-z]{2}" />
          <CurrencySelect v-model="currency" :aria-label="t('billing.customers.currency')" :blank="t('billing.customers.tenantCurrency')" />
          <fieldset class="grid gap-3">
            <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.customers.address.title") }}</legend>
            <Input v-model="address.line1" maxlength="120" :aria-label="t('billing.customers.address.line1')" :placeholder="t('billing.customers.address.line1')" />
            <Input v-model="address.line2" maxlength="120" :aria-label="t('billing.customers.address.line2')" :placeholder="t('billing.customers.address.line2')" />
            <Input v-model="address.city" maxlength="120" :aria-label="t('billing.customers.address.city')" :placeholder="t('billing.customers.address.city')" />
            <Input v-model="address.region" maxlength="120" :aria-label="t('billing.customers.address.region')" :placeholder="t('billing.customers.address.region')" />
            <Input v-model="address.postal_code" maxlength="20" :aria-label="t('billing.customers.address.postalCode')" :placeholder="t('billing.customers.address.postalCode')" />
          </fieldset>
          <ErrorText :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="creating = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-customer" :disabled="create.isPending.value">{{ t("billing.customers.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="addressOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.address.editing", { id: editing }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.address.title") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="edit-address" class="grid gap-3" @submit.prevent="saveAddress">
          <Input v-model="editAddress.line1" maxlength="120" :aria-label="t('billing.customers.address.line1')" :placeholder="t('billing.customers.address.line1')" />
          <Input v-model="editAddress.line2" maxlength="120" :aria-label="t('billing.customers.address.line2')" :placeholder="t('billing.customers.address.line2')" />
          <Input v-model="editAddress.city" maxlength="120" :aria-label="t('billing.customers.address.city')" :placeholder="t('billing.customers.address.city')" />
          <Input v-model="editAddress.region" maxlength="120" :aria-label="t('billing.customers.address.region')" :placeholder="t('billing.customers.address.region')" />
          <Input v-model="editAddress.postal_code" maxlength="20" :aria-label="t('billing.customers.address.postalCode')" :placeholder="t('billing.customers.address.postalCode')" />
          <Input v-model="editAddress.country" maxlength="2" pattern="[A-Za-z]{2}" :aria-label="t('billing.customers.country')" :placeholder="t('billing.customers.countryPlaceholder')" />
          <ErrorText :error="setAddress.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="editing = ''">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="edit-address" :disabled="setAddress.isPending.value">{{ t("billing.customers.address.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="taxOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("einvoice.customer.editing", { id: editingTax }) }}</SheetTitle>
        <SheetDescription>{{ t("einvoice.customer.title") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="edit-tax" class="grid gap-3" @submit.prevent="saveTax">
          <Input v-model="editTax.tax_id" maxlength="40" :aria-label="t('einvoice.customer.taxId')" :placeholder="t('einvoice.customer.taxId')" />
          <Input v-model="editTax.tax_office" maxlength="120" :aria-label="t('einvoice.customer.taxOffice')" :placeholder="t('einvoice.customer.taxOffice')" />
          <label class="flex items-center gap-2 text-sm"><input v-model="editTax.e_invoice_registered" type="checkbox" :aria-label="t('einvoice.customer.registered')" />{{ t("einvoice.customer.registered") }}</label>
          <ErrorText :error="setTax.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="editingTax = ''">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="edit-tax" :disabled="setTax.isPending.value">{{ t("einvoice.customer.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
  <CustomerLinksCard v-if="portalFor" :tenant-id="tenantId" :customer="portalFor" @close="portalFor = ''" />

  <Card class="p-3">
    <div class="flex flex-wrap items-center justify-between gap-3 px-3 pt-2 pb-3">
      <h2 class="text-lg font-semibold">{{ t("billing.tabs.customers") }}</h2>
      <Button data-testid="customers-new" @click="creating = true"><Plus class="size-4" />{{ t("billing.customers.new") }}</Button>
    </div>
    <ErrorText :error="customers.error.value" class="px-3" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <NativeSelect v-model="table.filters.value.country" class="w-40" :aria-label="t('billing.customers.country')">
        <option value="">{{ t("billing.customers.allCountries") }}</option>
        <option v-for="c in countries" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
      <NativeSelect v-model="table.filters.value.currency" class="w-40" :aria-label="t('billing.customers.currency')">
        <option value="">{{ t("billing.customers.allCurrencies") }}</option>
        <option v-for="c in currencies" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.customers.externalId") }}</TableHead>
          <TableHead>{{ t("billing.customers.name") }}</TableHead>
          <TableHead>{{ t("billing.customers.email") }}</TableHead>
          <TableHead>{{ t("billing.customers.country") }}</TableHead>
          <TableHead>{{ t("billing.customers.currency") }}</TableHead>
          <TableHead>{{ t("billing.customers.address.title") }}</TableHead>
          <TableHead>{{ t("einvoice.customer.title") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.paymentLinks.links") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="c in table.items.value" :key="c.id">
          <TableCell class="font-medium">{{ c.external_id }}</TableCell>
          <TableCell>{{ c.name }}</TableCell>
          <TableCell>{{ maskEmail(c.email) }}</TableCell>
          <TableCell>{{ c.country || "—" }}</TableCell>
          <TableCell>{{ c.billing_currency || "—" }}</TableCell>
          <TableCell class="max-w-64 whitespace-normal">
            <span class="text-sm">{{ addressLine(c.address) || "—" }}</span>
            <Button size="sm" variant="ghost" @click="edit(c)">{{ t("billing.customers.address.edit") }}</Button>
          </TableCell>
          <TableCell class="max-w-56 whitespace-normal">
            <span class="text-sm">{{ [c.tax_id, c.tax_office].filter(Boolean).join(" · ") || "—" }}</span>
            <Button size="sm" variant="ghost" @click="editTaxOf(c)">{{ t("einvoice.customer.edit") }}</Button>
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
    <p v-else-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
