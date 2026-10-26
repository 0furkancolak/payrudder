<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { Button } from "@/shared/ui/button";
import { CopyField } from "@/shared/ui/copy-field";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useCanWrite } from "@/shared/lib/access";
import { useLinkPromotionCode } from "./api";
import type { Affiliate } from "./logic";

/** The partner's public referral link to copy, and the promotion code that brings a customer to the partner when they type it at a checkout. */
const props = defineProps<{ tenantId: string; affiliate: Affiliate | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const canWrite = useCanWrite();
const link = useLinkPromotionCode(() => props.tenantId);
const code = ref("");

watch(open, (isOpen) => {
  if (!isOpen) return;
  code.value = "";
  link.reset();
});

function submit() {
  link.mutate(
    { id: props.affiliate?.id ?? "", code: code.value.trim() },
    {
      onSuccess: () => {
        toast.success(t("affiliates.link.done", { code: code.value.trim() }));
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("affiliates.link.title", { code: affiliate?.code ?? "" }) }}</SheetTitle>
        <SheetDescription>{{ t("affiliates.link.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
      <div class="grid gap-1.5">
        <Label>{{ t("affiliates.link.referralLink") }}</Label>
        <CopyField v-if="affiliate?.link" :value="affiliate.link" :label="t('affiliates.link.referralLink')" :copy-label="t('affiliates.link.copy')" testid="referral-link" />
        <p v-else class="text-muted-foreground text-sm">{{ t("affiliates.link.noLink", { code: affiliate?.code ?? "" }) }}</p>
      </div>
      <form id="af-link" class="grid gap-3" @submit.prevent="submit">
        <div class="grid gap-1.5">
          <Label for="al-code">{{ t("affiliates.link.promotionCode") }}</Label>
          <Input id="al-code" v-model="code" autocomplete="off" required dir="ltr" :disabled="!canWrite" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.link.promotionHint") }}</p>
        </div>
        <ErrorText :error="link.error.value" />
      </form>
      </SheetBody>
      <SheetFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("affiliates.common.close") }}</Button>
          <Button type="submit" form="af-link" :disabled="!canWrite || link.isPending.value">{{ t("affiliates.link.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
