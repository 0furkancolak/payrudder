<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { recordLink } from "@/features/related/link";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { LINK_KINDS, MAX_LINKS, type SupportLink } from "./api";

/**
 * The customers, invoices and subscriptions a ticket is about, as links to their pages (the same pages a sheet sends the reader to; a link of a ticket still being written
 * has no page yet and is plain text). With `editable` the reader can take one out and add one of an app of the ticket (`appId`): only the pointer is kept, so nothing
 * checks that the object exists, and a page that cannot find it says so.
 */
const props = defineProps<{ links: readonly SupportLink[]; appId?: string; editable?: boolean }>();
const emit = defineEmits<{ change: [links: SupportLink[]] }>();
const { t } = useI18n();
const kind = ref<SupportLink["kind"]>("customer");
const ref_ = ref("");
const environment = ref<SupportLink["environment"]>("live");
const canAdd = computed(() => !!props.appId && props.links.length < MAX_LINKS);
const same = (a: SupportLink, b: SupportLink) => a.kind === b.kind && a.ref === b.ref && a.app_id === b.app_id && a.environment === b.environment;
const label = (l: SupportLink) => `${t(`support.links.kind.${l.kind}`)} ${l.kind === "customer" || l.ref.length <= 12 ? l.ref : `${l.ref.slice(0, 8)}…`}`;

function add() {
  const value = ref_.value.trim();
  if (!value || !props.appId) return;
  const link: SupportLink = { kind: kind.value, ref: value, app_id: props.appId, environment: environment.value };
  if (!props.links.some((l) => same(l, link))) emit("change", [...props.links, link]);
  ref_.value = "";
}
</script>

<template>
  <div class="flex flex-col gap-2" data-testid="ticket-links">
    <ul v-if="links.length > 0" class="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
      <li v-for="l in links" :key="`${l.kind}:${l.app_id}:${l.environment}:${l.ref}`" class="bg-muted flex max-w-full items-center gap-1 rounded-full py-1 ps-3 pe-2 break-all" data-testid="ticket-link">
        <Link v-if="l.tenant_id" v-bind="recordLink(l.kind, l.ref, l.tenant_id)" class="underline" :title="l.ref">{{ label(l) }}</Link>
        <span v-else :title="l.ref">{{ label(l) }}</span>
        <span v-if="l.app_name" class="text-muted-foreground text-xs">· {{ l.app_name }} · {{ t(`support.links.env.${l.environment}`) }}</span>
        <button v-if="editable" type="button" class="hover:text-destructive rounded-full px-1" :aria-label="t('support.links.remove', { link: label(l) })" data-testid="ticket-link-remove" @click="emit('change', links.filter((x) => !same(x, l)))">×</button>
      </li>
    </ul>
    <p v-else-if="!editable" class="text-muted-foreground m-0 text-xs">{{ t("support.links.none") }}</p>
    <form v-if="editable" class="flex flex-wrap items-end gap-2" @submit.prevent="add">
      <label class="grid gap-1 text-xs">{{ t("support.links.kindLabel") }}
        <NativeSelect v-model="kind" :disabled="!canAdd" data-testid="link-kind">
          <option v-for="k in LINK_KINDS" :key="k" :value="k">{{ t(`support.links.kind.${k}`) }}</option>
        </NativeSelect>
      </label>
      <label class="grid gap-1 text-xs">{{ t("support.links.refLabel") }}
        <Input v-model="ref_" class="w-56" maxlength="200" :disabled="!canAdd" :placeholder="kind === 'customer' ? t('support.links.customerHint') : t('support.links.idHint')" data-testid="link-ref" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("support.links.envLabel") }}
        <NativeSelect v-model="environment" :disabled="!canAdd" data-testid="link-env">
          <option value="live">{{ t("support.links.env.live") }}</option>
          <option value="sandbox">{{ t("support.links.env.sandbox") }}</option>
        </NativeSelect>
      </label>
      <Button type="submit" variant="outline" size="sm" :disabled="!canAdd || ref_.trim() === ''" data-testid="link-add">{{ t("support.links.add") }}</Button>
    </form>
    <p v-if="editable && !appId" class="text-muted-foreground m-0 text-xs">{{ t("support.links.needsApp") }}</p>
  </div>
</template>
