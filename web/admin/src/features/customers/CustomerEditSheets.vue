<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { cleanAddress } from "@/shared/lib/address";
import { normalizePhone } from "@/shared/lib/phone";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { LocationFields } from "@/shared/ui/location-fields";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import type { ApiLocale } from "@/shared/lib/locale";
import { useFormErrors } from "@/shared/lib/formErrors";
import { taxFormRules } from "@/shared/lib/customerTax";
import { useSetCustomerAddress, useSetCustomerContact, useSetCustomerTax, type Address, type CustomerContact, type CustomerKind } from "./api";
import AddressLines from "./AddressLines.vue";
import CustomerProfileFields from "./CustomerProfileFields.vue";
import CustomerTaxFields from "./CustomerTaxFields.vue";
import type { Customer } from "./lib";

/**
 * The three edits of a customer, each in a sheet of its own: the name and e-mail, the billing address and the tax data e-invoices carry. The models hold the customer being
 * edited (null is closed); the sheet starts from that customer's current values.
 */
const props = defineProps<{ tenantId: string }>();
const contactOf = defineModel<Customer | null>("contact", { default: null });
const addressOf = defineModel<Customer | null>("address", { default: null });
const taxOf = defineModel<Customer | null>("tax", { default: null });
const { t } = useI18n();

const setContact = useSetCustomerContact(() => props.tenantId);
const editContact = ref<CustomerContact>({ name: "", email: "" });
const editLocale = ref("");
const editKind = ref("");
const editPhone = ref("");
const phoneBad = computed(() => normalizePhone(editPhone.value) === null);
const contactId = computed(() => contactOf.value?.external_id ?? "");
const contactOpen = computed({ get: () => contactOf.value !== null, set: (v) => !v && (contactOf.value = null) });
watch(contactOf, (c) => {
  if (!c) return;
  editContact.value = { name: c.name ?? "", email: c.email ?? "" };
  editLocale.value = c.locale ?? "";
  editKind.value = c.customer_type ?? "";
  editPhone.value = c.phone ?? "";
  setContact.reset();
});
function saveContact() {
  if (phoneBad.value) return;
  setContact.mutate(
    {
      externalId: contactId.value,
      contact: {
        name: editContact.value.name.trim(),
        email: editContact.value.email.trim(),
        locale: (editLocale.value || undefined) as ApiLocale | undefined,
        customer_type: (editKind.value || undefined) as CustomerKind | undefined,
        phone: normalizePhone(editPhone.value) ?? "", // empty clears the number
      },
    },
    { onSuccess: () => (contactOf.value = null) },
  );
}

const setAddress = useSetCustomerAddress(() => props.tenantId);
const editAddress = ref<Address>({});
/** The place fields carry the names the customer list uses for them. */
const placeLabels = computed(() => ({ country: t("billing.customers.country"), region: t("billing.customers.address.region"), city: t("billing.customers.address.city") }));
const addressId = computed(() => addressOf.value?.external_id ?? "");
const addressOpen = computed({ get: () => addressOf.value !== null, set: (v) => !v && (addressOf.value = null) });
watch(addressOf, (c) => {
  if (!c) return;
  editAddress.value = { ...(c.address ?? {}) };
  setAddress.reset();
});
function saveAddress() {
  setAddress.mutate({ externalId: addressId.value, address: cleanAddress(editAddress.value) }, { onSuccess: () => (addressOf.value = null) });
}

const setTax = useSetCustomerTax(() => props.tenantId);
const editTaxId = ref("");
const editTaxOffice = ref("");
const editRegistered = ref(false);
const taxId = computed(() => taxOf.value?.external_id ?? "");
// The fields, their names and which one is required follow the customer's country and type (docs/plans/05); a number of the wrong kind is only hinted at.
const taxForm = useFormErrors(() => ({
  taxId: taxFormRules(taxOf.value?.country, taxOf.value?.customer_type).taxId === "required" && editTaxId.value.trim() === "" ? t("common.validation.required") : undefined,
}));
const taxOpen = computed({ get: () => taxOf.value !== null, set: (v) => !v && (taxOf.value = null) });
watch(taxOf, (c) => {
  if (!c) return;
  editTaxId.value = c.tax_id ?? "";
  editTaxOffice.value = c.tax_office ?? "";
  editRegistered.value = c.e_invoice_registered ?? false;
  setTax.reset();
  taxForm.reset();
});
function saveTax() {
  if (!taxForm.attempt()) return;
  setTax.mutate(
    { externalId: taxId.value, tax: { tax_id: editTaxId.value.trim(), tax_office: editTaxOffice.value.trim(), e_invoice_registered: editRegistered.value } },
    { onSuccess: () => (taxOf.value = null) },
  );
}
</script>

<template>
  <Sheet v-model:open="contactOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.contact.editing", { id: contactId }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.contact.title") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="edit-contact" class="grid gap-3" @submit.prevent="saveContact">
          <Input v-model="editContact.name" required maxlength="200" :aria-label="t('billing.customers.contact.name')" :placeholder="t('billing.customers.contact.name')" />
          <Input v-model="editContact.email" required type="email" :aria-label="t('billing.customers.contact.email')" :placeholder="t('billing.customers.contact.email')" />
          <CustomerProfileFields v-model:locale="editLocale" v-model:kind="editKind" v-model:phone="editPhone" id-prefix="edit-contact" />
          <ErrorText :error="setContact.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="contactOf = null">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="edit-contact" :disabled="setContact.isPending.value || phoneBad">{{ t("billing.customers.contact.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="addressOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.address.editing", { id: addressId }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.address.title") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="edit-address" class="grid gap-3" @submit.prevent="saveAddress">
          <LocationFields v-model:country="editAddress.country" v-model:region="editAddress.region" v-model:city="editAddress.city" id-prefix="edit-customer" compact :labels="placeLabels" />
          <AddressLines v-model:line1="editAddress.line1" v-model:line2="editAddress.line2" v-model:postal-code="editAddress.postal_code" id-prefix="edit-address" :country="editAddress.country" />
          <ErrorText :error="setAddress.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="addressOf = null">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="edit-address" :disabled="setAddress.isPending.value">{{ t("billing.customers.address.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="taxOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("einvoice.customer.editing", { id: taxId }) }}</SheetTitle>
        <SheetDescription>{{ t("einvoice.customer.title") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="edit-tax" class="grid gap-3" novalidate @submit.prevent="saveTax">
          <CustomerTaxFields v-model:tax-id="editTaxId" v-model:tax-office="editTaxOffice" v-model:registered="editRegistered" id-prefix="edit-tax" :country="taxOf?.country" :kind="taxOf?.customer_type" :error="taxForm.errors.value.taxId" />
          <ErrorText :error="setTax.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="taxOf = null">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="edit-tax" :disabled="setTax.isPending.value">{{ t("einvoice.customer.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
