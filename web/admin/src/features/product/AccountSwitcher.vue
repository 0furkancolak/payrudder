<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { activeAccount } from "@/shared/lib/session";
import { NativeSelect } from "@/shared/ui/native-select";
import { useMe, useSwitchAccount } from "./api";

/**
 * Which account the person works in, for a person who belongs to more than one (an invitation adds one). With a single account it shows its name and
 * nothing to choose. The choice is kept for this tab only and sent as ?account_id= on the account routes; the gateway checks it against the session.
 */
const { t } = useI18n();
const me = useMe();
const switchTo = useSwitchAccount();

const memberships = computed(() => me.data.value?.memberships ?? []);
const current = computed(() => {
  const chosen = memberships.value.find((m) => m.account_id === activeAccount.value);
  return (chosen ?? memberships.value[0])?.account_id ?? "";
});
</script>

<template>
  <NativeSelect v-if="memberships.length > 1" class="w-56" :model-value="current" :aria-label="t('product.account.switch')" @update:model-value="(v) => switchTo(String(v))">
    <option v-for="m in memberships" :key="m.account_id" :value="m.account_id">{{ m.account_name }}</option>
  </NativeSelect>
</template>
