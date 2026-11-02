<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { firstError } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { StatusPill } from "@/shared/ui/status-pill";
import { useMailIdentity } from "./api";
import { identityTone, IDENTITY_CHECKS } from "./identity";

/**
 * The sender identity of an app's own e-mail provider: what the domain of the address mail is sent from publishes in DNS (SPF, DKIM, DMARC). Three lookups, nothing
 * changed; the DKIM selector is the one the provider publishes its key under, and is not guessed. It does not say whether the records name the provider that sends, nor
 * whether the provider has verified the domain (the sheet says so).
 */
const props = defineProps<{ appId: string }>();
const { t, te } = useI18n();
const identity = useMailIdentity(() => props.appId);
const selector = ref("");
const result = computed(() => identity.data.value);

const check = () => identity.mutate(selector.value.trim().toLowerCase());
const detail = (d?: string) => (d && te(`product.integrations.mail.identity.detail.${d}`) ? t(`product.integrations.mail.identity.detail.${d}`) : "");
</script>

<template>
  <section class="grid gap-3 rounded-2xl border p-4" data-testid="mail-identity" :aria-label="t('product.integrations.mail.identity.title')">
    <div>
      <h3 class="text-sm font-semibold">{{ t("product.integrations.mail.identity.title") }}</h3>
      <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.identity.intro") }}</p>
    </div>
    <form class="flex flex-wrap items-end gap-2" @submit.prevent="check">
      <div class="grid min-w-0 flex-1 gap-1">
        <label class="text-sm" for="mail-dkim-selector">{{ t("product.integrations.mail.identity.selector") }}</label>
        <Input id="mail-dkim-selector" v-model="selector" class="mono" spellcheck="false" autocomplete="off" data-testid="mail-identity-selector" />
        <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.identity.selectorHelp") }}</p>
      </div>
      <Button type="submit" variant="outline" :disabled="identity.isPending.value" data-testid="mail-identity-check">{{ identity.isPending.value ? t("product.integrations.mail.identity.checking") : t("product.integrations.mail.identity.check") }}</Button>
    </form>
    <ErrorText :error="firstError(identity)" />
    <div v-if="result" class="grid gap-2" data-testid="mail-identity-result">
      <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.identity.domain", { domain: result.domain }) }}</p>
      <ul class="divide-y">
        <li v-for="c in IDENTITY_CHECKS" :key="c" class="grid gap-1 py-2" :data-testid="`mail-identity-${c}`">
          <div class="flex flex-wrap items-center gap-2">
            <span class="w-16 text-sm font-medium">{{ t(`product.integrations.mail.identity.names.${c}`) }}</span>
            <StatusPill :tone="identityTone(result[c].status)">{{ t(`product.integrations.mail.identity.status.${result[c].status}`) }}</StatusPill>
            <span v-if="detail(result[c].detail)" class="text-muted-foreground text-xs">{{ detail(result[c].detail) }}</span>
          </div>
          <code v-if="result[c].record" class="mono text-muted-foreground text-xs break-all">{{ result[c].record }}</code>
        </li>
      </ul>
      <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.identity.note") }}</p>
    </div>
  </section>
</template>
