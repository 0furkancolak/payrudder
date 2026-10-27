<script setup lang="ts">
import { FileText } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { liveTenant } from "@/shared/lib/environment";
import { ADDRESS_MAX, countryProfile } from "@/shared/lib/address";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { LocationFields } from "@/shared/ui/location-fields";
import { Skeleton } from "@/shared/ui/skeleton";
import { useApp, useSaveBillingProfile } from "./api";
import { emptyProfile, fromTenant, normalizeTaxId, profileBody, taxIdRequired, validateProfile, type ProfileField, type ProfileForm } from "./billingProfile";

/**
 * The seller's billing profile of the live environment: the legal name, tax id and address printed on live invoices, and the "legal profile" item of the
 * go-live checklist. Only the owner edits it; everybody else in the account sees what is saved. The server checks the same rules again.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const app = useApp(() => props.appId);
const live = computed(() => liveTenant(app.data.value?.app ?? undefined));
const isOwner = computed(() => app.data.value?.role === "owner");
const save = useSaveBillingProfile(() => props.appId);

const form = ref<ProfileForm>(emptyProfile());
const saved = ref(false);
/** Problems are shown once the owner has tried to save, so an empty form does not open covered in red. */
const tried = ref(false);

function load() {
  form.value = fromTenant(live.value);
}
// The saved profile fills the form when it arrives, and again after a save refreshes it.
watch(() => live.value, load, { immediate: true });

// A place picked from a list types nothing into the form, so the "saved" note goes away here too.
watch(
  () => [form.value.country, form.value.region, form.value.city],
  (now, before) => {
    if (before && live.value && now.some((v, i) => v !== before[i]) && !save.isPending.value) saved.value = false;
  },
);

const problems = computed(() => validateProfile(form.value));
const profile = computed(() => countryProfile(form.value.country.trim().toUpperCase()));
const needsTaxId = computed(() => taxIdRequired(form.value.country));
const MAX: Record<ProfileField, number> = { legalName: 200, taxId: 20, taxOffice: 200, country: 2, line1: ADDRESS_MAX.line1, line2: ADDRESS_MAX.line2, city: ADDRESS_MAX.city, region: ADDRESS_MAX.region, postalCode: ADDRESS_MAX.postal_code };
const problemText = (f: ProfileField): string => {
  const p = tried.value ? problems.value[f] : undefined;
  if (!p) return "";
  if (p === "required" || p === "taxIdFormat") return t(`product.goLive.profile.problems.${p}`);
  return t(`address.problems.${p}`, { max: MAX[f] });
};
const placeProblems = computed(() => ({ country: problemText("country"), region: problemText("region"), city: problemText("city") }));

function submit() {
  tried.value = true;
  saved.value = false;
  if (Object.keys(problems.value).length > 0 || !isOwner.value) return;
  save.mutate(profileBody(form.value), { onSuccess: () => (saved.value = true) });
}
const taxIdPreview = computed(() => normalizeTaxId(form.value.taxId));
</script>

<template>
  <section id="billing-profile" class="surface flex flex-col gap-4 p-6" :aria-label="t('product.goLive.profile.title')" data-testid="billing-profile">
    <div class="flex items-start gap-3">
      <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-2xl" aria-hidden="true"><FileText class="size-5" /></span>
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.goLive.profile.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.goLive.profile.intro") }}</p>
      </div>
    </div>

    <Skeleton v-if="app.isPending.value" class="h-40 rounded-2xl" />
    <p v-else-if="!live" class="text-muted-foreground text-sm">{{ t("product.goLive.noLive") }}</p>
    <form v-else class="grid gap-3" :aria-label="t('product.goLive.profile.title')" novalidate @submit.prevent="submit" @input="saved = false">
      <p v-if="!isOwner" class="text-muted-foreground text-xs" data-testid="profile-owner-only">{{ t("product.goLive.profile.ownerOnly") }}</p>
      <fieldset class="grid gap-3" :disabled="!isOwner || save.isPending.value">
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="grid gap-1">
            <label class="text-sm" for="bp-legal-name">{{ t("product.goLive.profile.legalName") }}</label>
            <Input id="bp-legal-name" v-model="form.legalName" autocomplete="organization" :aria-invalid="!!(tried && problems.legalName)" />
            <p v-if="problemText('legalName')" class="text-destructive text-xs" role="alert">{{ problemText("legalName") }}</p>
          </div>
        </div>
        <LocationFields v-model:country="form.country" v-model:region="form.region" v-model:city="form.city" id-prefix="bp" :problems="placeProblems" />

        <div class="grid gap-3 sm:grid-cols-2">
          <div class="grid gap-1">
            <label class="text-sm" for="bp-tax-id">{{ t("product.goLive.profile.taxId") }}</label>
            <Input id="bp-tax-id" v-model="form.taxId" class="mono" spellcheck="false" autocomplete="off" :aria-invalid="!!(tried && problems.taxId)" />
            <p v-if="problemText('taxId')" class="text-destructive text-xs" role="alert">{{ problemText("taxId") }}</p>
            <p v-else class="text-muted-foreground text-xs">
              {{ needsTaxId ? t("product.goLive.profile.taxIdRequired", { country: form.country.toUpperCase() }) : t("product.goLive.profile.taxIdOptional") }}
              <span v-if="taxIdPreview && taxIdPreview !== form.taxId" class="mono">({{ taxIdPreview }})</span>
            </p>
          </div>
          <div class="grid gap-1">
            <label class="text-sm" for="bp-tax-office">{{ t("product.goLive.profile.taxOffice") }}</label>
            <Input id="bp-tax-office" v-model="form.taxOffice" :aria-invalid="!!(tried && problems.taxOffice)" />
          </div>
        </div>

        <div class="grid gap-1">
          <label class="text-sm" for="bp-line1">{{ t("address.labels.line1") }}</label>
          <Input id="bp-line1" v-model="form.line1" autocomplete="address-line1" :aria-invalid="!!(tried && problems.line1)" />
          <p v-if="problemText('line1')" class="text-destructive text-xs" role="alert">{{ problemText("line1") }}</p>
        </div>
        <div class="grid gap-1">
          <label class="text-sm" for="bp-line2">{{ t("address.labels.line2") }}</label>
          <Input id="bp-line2" v-model="form.line2" autocomplete="address-line2" :aria-invalid="!!(tried && problems.line2)" />
          <p v-if="problemText('line2')" class="text-destructive text-xs" role="alert">{{ problemText("line2") }}</p>
        </div>
        <div class="grid gap-1 sm:max-w-[50%]">
          <label class="text-sm" for="bp-postal">{{ t(`address.labels.${profile.postalCode}`) }}</label>
          <Input id="bp-postal" v-model="form.postalCode" autocomplete="postal-code" :placeholder="profile.example" :aria-invalid="!!(tried && problems.postalCode)" />
          <p v-if="problemText('postalCode')" class="text-destructive text-xs" role="alert">{{ problemText("postalCode") }}</p>
        </div>
      </fieldset>
      <div class="flex flex-wrap items-center gap-3">
        <Button type="submit" :disabled="!isOwner || save.isPending.value" data-testid="profile-save">{{ t("product.goLive.profile.save") }}</Button>
        <span v-if="saved" class="text-success text-sm" role="status" data-testid="profile-saved">{{ t("product.goLive.profile.saved") }}</span>
      </div>
      <ErrorText :error="save.error.value" />
    </form>
  </section>
</template>
