<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useTenant } from "@/features/tenants";
import { CURRENCIES, type Currency } from "@/shared/lib/currencies";
import { useErrorText } from "@/shared/lib/errors";
import { label } from "@/shared/lib/labels";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { environmentWord, isValidSlug, useApps, useCreateApiKey, useCreateApp, useRevokeApiKey, type CreatedKey } from "./api";

const { t, te, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);
/** Apps belong to an account; the tenant this tab is opened from says which. */
const accountId = computed(() => tenant.data.value?.account_id ?? "");
const apps = useApps(accountId);
const createApp = useCreateApp(accountId);
const createKey = useCreateApiKey();
const revokeKey = useRevokeApiKey();

const list = computed(() => apps.data.value?.apps ?? []);
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "—");

// --- a new app
const name = ref("");
const slug = ref("");
const currency = ref<Currency | "">("");
const timezone = ref("");
const graceHours = ref("");
const slugOk = computed(() => isValidSlug(slug.value.trim()));
function submit() {
  if (currency.value === "" || !slugOk.value) return;
  createApp.mutate(
    { name: name.value.trim(), slug: slug.value.trim(), currency: currency.value, timezone: timezone.value.trim(), graceHours: Number(graceHours.value) },
    { onSuccess: () => (name.value = slug.value = timezone.value = graceHours.value = "") },
  );
}

// --- API keys. The API cannot list keys, so the page shows those made here (their plaintext only until dismissed) and revokes by id.
const keysOf = ref("");
const keysApp = computed(() => list.value.find((a) => a.id === keysOf.value));
const session = ref<CreatedKey[]>([]);
const secret = ref<CreatedKey | null>(null);
const revokeId = ref("");
const revoked = ref("");

function makeKey(tenant: string, environment: string) {
  createKey.mutate(tenant, {
    onSuccess: (r) => {
      const key: CreatedKey = { id: r.api_key?.id ?? "", tenantId: tenant, prefix: r.api_key?.prefix ?? "", plaintext: r.plaintext ?? "", environment, revoked: false };
      session.value = [key, ...session.value];
      secret.value = key;
      revoked.value = "";
    },
  });
}
function revoke(id: string) {
  if (!id || !window.confirm(t("apps.keys.confirmRevoke"))) return;
  revokeKey.mutate(id, {
    onSuccess: () => {
      session.value = session.value.map((k) => (k.id === id ? { ...k, revoked: true } : k));
      if (secret.value?.id === id) secret.value = null;
      revoked.value = id;
      revokeId.value = "";
    },
  });
}
const failure = computed(() => {
  for (const m of [createApp, createKey, revokeKey]) if (m.error.value) return errorText(m.error.value);
  return "";
});
const sessionOf = (tenant?: string) => session.value.filter((k) => k.tenantId === tenant);
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("apps.new") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("apps.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" role="form" :aria-label="t('apps.formLabel')" @submit.prevent="submit">
      <Input v-model="name" :aria-label="t('apps.name')" :placeholder="t('apps.name')" required />
      <Input v-model="slug" maxlength="60" :aria-label="t('apps.slug')" :placeholder="t('apps.slugPlaceholder')" :aria-invalid="!slugOk" />
      <NativeSelect v-model="currency" :aria-label="t('apps.currency')" required>
        <option value="" disabled>{{ t("apps.currencyPlaceholder") }}</option>
        <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
      <Input v-model="timezone" :aria-label="t('apps.timezone')" :placeholder="t('apps.timezonePlaceholder')" required />
      <Input v-model="graceHours" type="number" min="0" max="168" :aria-label="t('apps.grace')" :placeholder="t('apps.gracePlaceholder')" required />
      <Button type="submit" :disabled="createApp.isPending.value || !accountId || !slugOk">{{ t("apps.create") }}</Button>
    </form>
    <p v-if="!slugOk" class="text-destructive text-sm" role="alert">{{ t("apps.badSlug") }}</p>
    <p v-if="failure" class="text-destructive text-sm" role="alert">{{ failure }}</p>
    <p v-if="apps.error.value" class="text-destructive text-sm" role="alert">{{ errorText(apps.error.value) }}</p>
  </Card>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("apps.title") }}</h2>
    <p class="text-muted-foreground px-3 text-sm">{{ t("apps.immutable") }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("apps.name") }}</TableHead>
          <TableHead>{{ t("apps.slug") }}</TableHead>
          <TableHead>{{ t("apps.environments") }}</TableHead>
          <TableHead>{{ t("apps.created") }}</TableHead>
          <TableHead class="text-end">{{ t("apps.actions") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="a in list" :key="a.id">
          <TableCell class="font-medium">{{ a.name }}</TableCell>
          <TableCell class="mono text-xs">{{ a.slug }}</TableCell>
          <TableCell class="space-y-1">
            <div v-for="e in a.tenants ?? []" :key="e.id" class="flex flex-wrap items-center gap-2 text-xs">
              <Badge :variant="environmentWord(e) === 'live' ? 'success' : 'secondary'">{{ label(t, te, "environment", environmentWord(e)) }}</Badge>
              <Link to="/tenants/$id" :params="{ id: e.id ?? '' }" class="mono underline">{{ e.id }}</Link>
              <span class="text-muted-foreground">{{ e.currency }} · {{ e.timezone }}</span>
            </div>
          </TableCell>
          <TableCell class="text-xs">{{ when(a.create_time) }}</TableCell>
          <TableCell class="text-end">
            <Button size="sm" variant="outline" @click="keysOf = a.id ?? ''">{{ t("apps.keys.open") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!apps.isPending.value && list.length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("apps.empty") }}</p>
  </Card>

  <Card v-if="keysApp" class="mb-6" role="region" :aria-label="t('apps.keys.title', { app: keysApp.name })">
    <h2 class="text-lg font-semibold">{{ t("apps.keys.title", { app: keysApp.name }) }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("apps.keys.hint") }}</p>
    <div v-if="secret" class="border-warning/50 bg-warning/10 rounded-md border p-3" role="status">
      <p class="text-sm font-medium">{{ t("apps.keys.secretOnce") }}</p>
      <p class="mono break-all text-sm" data-testid="api-key-secret">{{ secret.plaintext }}</p>
      <Button size="sm" variant="outline" class="mt-2" @click="secret = null">{{ t("apps.keys.dismiss") }}</Button>
    </div>
    <div v-for="e in keysApp.tenants ?? []" :key="e.id" class="grid gap-2 rounded-md border p-3">
      <div class="flex flex-wrap items-center gap-2">
        <Badge :variant="environmentWord(e) === 'live' ? 'success' : 'secondary'">{{ label(t, te, "environment", environmentWord(e)) }}</Badge>
        <span class="mono text-xs">{{ e.id }}</span>
        <Button size="sm" class="ms-auto" :disabled="createKey.isPending.value" :aria-label="t('apps.keys.createFor', { env: label(t, te, 'environment', environmentWord(e)) })" @click="makeKey(e.id ?? '', environmentWord(e))">
          {{ t("apps.keys.create") }}
        </Button>
      </div>
      <ul v-if="sessionOf(e.id).length" class="grid gap-1 text-sm">
        <li v-for="k in sessionOf(e.id)" :key="k.id" class="flex flex-wrap items-center gap-2">
          <span class="mono text-xs">qk_{{ k.environment === "live" ? "live" : "test" }}_{{ k.prefix }}…</span>
          <span class="mono text-muted-foreground text-xs">{{ k.id }}</span>
          <Badge v-if="k.revoked" variant="destructive">{{ t("apps.keys.revoked") }}</Badge>
          <Button v-else size="sm" variant="destructive" class="ms-auto" :disabled="revokeKey.isPending.value" @click="revoke(k.id)">{{ t("apps.keys.revoke") }}</Button>
        </li>
      </ul>
    </div>
    <form class="flex flex-wrap items-center gap-2" :aria-label="t('apps.keys.revokeForm')" @submit.prevent="revoke(revokeId.trim())">
      <Input v-model="revokeId" class="max-w-md" :aria-label="t('apps.keys.revokeId')" :placeholder="t('apps.keys.revokeId')" />
      <Button type="submit" variant="outline" :disabled="revokeKey.isPending.value || revokeId.trim() === ''">{{ t("apps.keys.revokeById") }}</Button>
    </form>
    <p v-if="revoked" class="text-success text-sm" role="status">{{ t("apps.keys.revokedDone", { id: revoked }) }}</p>
  </Card>
</template>
