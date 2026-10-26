<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Textarea } from "@/shared/ui/textarea";
import { useCreateAffiliate, type NewAffiliate } from "./api";

/** Adds a partner by hand. The partner is active at once unless "leave pending" is ticked. */
const props = defineProps<{ tenantId: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const create = useCreateAffiliate(() => props.tenantId);

const email = ref("");
const name = ref("");
const code = ref("");
const method = ref<"manual" | "bank_transfer">("manual");
const details = ref("");
const pending = ref(false);

watch(open, (isOpen) => {
  if (!isOpen) return;
  email.value = name.value = code.value = details.value = "";
  method.value = "manual";
  pending.value = false;
  create.reset();
});

function submit() {
  const body: NewAffiliate = { email: email.value.trim(), payout_method: method.value };
  if (name.value.trim()) body.name = name.value.trim();
  if (code.value.trim()) body.code = code.value.trim();
  if (details.value.trim()) body.payout_details = details.value.trim();
  if (pending.value) body.pending = true;
  create.mutate(body, {
    onSuccess: (a) => {
      toast.success(t("affiliates.add.done", { code: a.code ?? "" }));
      open.value = false;
    },
  });
}
</script>

<template>
  <Sheet v-model:open="open" name="add-affiliate">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("affiliates.add.title") }}</SheetTitle>
        <SheetDescription>{{ t("affiliates.add.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
      <form id="af-add" class="grid gap-3" @submit.prevent="submit">
        <div class="grid gap-1.5">
          <Label for="af-email">{{ t("affiliates.add.email") }}</Label>
          <Input id="af-email" v-model="email" type="email" inputmode="email" autocomplete="off" required dir="ltr" />
        </div>
        <div class="grid gap-1.5">
          <Label for="af-name">{{ t("affiliates.add.name") }}</Label>
          <Input id="af-name" v-model="name" autocomplete="off" maxlength="120" />
        </div>
        <div class="grid gap-1.5">
          <Label for="af-code">{{ t("affiliates.add.code") }}</Label>
          <Input id="af-code" v-model="code" autocomplete="off" maxlength="32" pattern="[A-Za-z0-9_\-]{3,32}" dir="ltr" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.add.codeHint") }}</p>
        </div>
        <div class="grid gap-1.5">
          <Label for="af-method">{{ t("affiliates.payoutMethod") }}</Label>
          <NativeSelect id="af-method" v-model="method">
            <option value="manual">{{ t("affiliates.methods.manual") }}</option>
            <option value="bank_transfer">{{ t("affiliates.methods.bank_transfer") }}</option>
          </NativeSelect>
        </div>
        <div class="grid gap-1.5">
          <Label for="af-details">{{ t("affiliates.payoutDetails") }}</Label>
          <Textarea id="af-details" v-model="details" rows="3" maxlength="1000" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.payoutDetailsHint") }}</p>
        </div>
        <div class="flex items-center gap-2">
          <Checkbox id="af-pending" :model-value="pending" @update:model-value="pending = $event === true" />
          <Label for="af-pending">{{ t("affiliates.add.pending") }}</Label>
        </div>
        <ErrorText :error="create.error.value" />
      </form>
      </SheetBody>
      <SheetFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("affiliates.common.cancel") }}</Button>
          <Button type="submit" form="af-add" :disabled="create.isPending.value">{{ t("affiliates.add.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
