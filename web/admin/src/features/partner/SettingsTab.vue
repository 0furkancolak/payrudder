<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Textarea } from "@/shared/ui/textarea";
import { useUpdateAffiliation } from "./api";
import type { Affiliation } from "./logic";

/** The partner's name and how they want to be paid. The seller sees these when they pay. */
const props = defineProps<{ affiliation: Affiliation }>();
const { t } = useI18n();
const update = useUpdateAffiliation(() => props.affiliation.id ?? "");
const name = ref("");
const method = ref<"manual" | "bank_transfer">("manual");
const details = ref("");
const saved = ref(false);

watch(
  () => props.affiliation,
  (a) => {
    name.value = a.name ?? "";
    method.value = a.payout_method ?? "manual";
    details.value = a.payout_details ?? "";
  },
  { immediate: true },
);

function submit() {
  saved.value = false;
  update.mutate({ name: name.value.trim(), payout_method: method.value, payout_details: details.value.trim() }, { onSuccess: () => (saved.value = true) });
}
</script>

<template>
  <Card>
    <form class="grid max-w-xl gap-3" :aria-label="t('partner.tabs.settings')" @submit.prevent="submit">
      <div class="grid gap-1.5">
        <Label for="ps-name">{{ t("partner.settings.name") }}</Label>
        <Input id="ps-name" v-model="name" autocomplete="name" maxlength="200" />
      </div>
      <div class="grid gap-1.5">
        <Label for="ps-method">{{ t("affiliates.payoutMethod") }}</Label>
        <NativeSelect id="ps-method" v-model="method">
          <option value="manual">{{ t("affiliates.methods.manual") }}</option>
          <option value="bank_transfer">{{ t("affiliates.methods.bank_transfer") }}</option>
        </NativeSelect>
      </div>
      <div class="grid gap-1.5">
        <Label for="ps-details">{{ t("affiliates.payoutDetails") }}</Label>
        <Textarea id="ps-details" v-model="details" rows="4" maxlength="1000" />
        <p class="text-muted-foreground m-0 text-xs">{{ t("partner.settings.detailsHint") }}</p>
      </div>
      <ErrorText :error="update.error.value" />
      <p v-if="saved && !update.isPending.value" class="text-success m-0 text-sm" role="status">{{ t("partner.settings.saved") }}</p>
      <div><Button type="submit" :disabled="update.isPending.value">{{ t("affiliates.common.save") }}</Button></div>
    </form>
  </Card>
</template>
