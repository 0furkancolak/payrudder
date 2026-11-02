<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";

/**
 * Asks for a current code of the authenticator app, or an unused recovery code, before a change to two-factor sign-in (turning it off, making new recovery
 * codes). The box is emptied each time the dialog opens, and the code goes nowhere but to `submit`.
 */
defineProps<{ title: string; body: string; submit: string; destructive?: boolean; pending: boolean; error?: unknown }>();
const open = defineModel<boolean>("open", { required: true });
const emit = defineEmits<{ submit: [code: string] }>();
const { t } = useI18n();
const code = ref("");
watch(open, (o) => {
  if (o) code.value = "";
});
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md" data-testid="code-prompt">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>{{ body }}</DialogDescription>
      </DialogHeader>
      <form class="grid gap-3" @submit.prevent="emit('submit', code.trim())">
        <FormField for="tf-prompt-code" :label="t('product.twoFactor.prompt.label')" :hint="t('product.twoFactor.prompt.hint')">
          <Input id="tf-prompt-code" v-model="code" class="mono" autocomplete="one-time-code" autocapitalize="none" spellcheck="false" required />
        </FormField>
        <ErrorText :error="error" box />
        <DialogFooter>
          <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" :variant="destructive ? 'destructive' : 'default'" :disabled="pending || code.trim() === ''" data-testid="code-prompt-submit">{{ submit }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
