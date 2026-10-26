<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { Plus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { keyMask } from "@/shared/lib/environment";
import { useFormat } from "@/shared/lib/format";
import { useTenant } from "@/features/tenants";
import { type Currency } from "@/shared/lib/currencies";
import { firstError } from "@/shared/lib/errors";
import { label } from "@/shared/lib/labels";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { environmentWord, isValidSlug, useApps, useCreateApiKey, useCreateApp, useRevokeApiKey, type CreatedKey } from "./api";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";

const { t, te } = useI18n();
const fmt = useFormat();
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
const table = useTableState(list, {
  searchFields: (a) => [a.name, a.slug, ...(a.tenants ?? []).map((e) => e.id)],
  filters: { env: (a, v) => (a.tenants ?? []).some((e) => environmentWord(e) === v) },
});
const when = (v?: string) => fmt.date(v, "day", "—");

// --- a new app
const name = ref("");
const slug = ref("");
const currency = ref<Currency | "">("");
const timezone = ref("");
const graceHours = ref("");
const creating = ref(false);
watch(creating, (v) => {
  if (v) {
    name.value = slug.value = timezone.value = graceHours.value = "";
    currency.value = "";
    createApp.reset();
  }
});
const slugOk = computed(() => isValidSlug(slug.value.trim()));
function submit() {
  if (currency.value === "" || !slugOk.value) return;
  createApp.mutate(
    { name: name.value.trim(), slug: slug.value.trim(), currency: currency.value, timezone: timezone.value.trim(), graceHours: Number(graceHours.value) },
    { onSuccess: () => { name.value = slug.value = timezone.value = graceHours.value = ""; creating.value = false; } },
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
const failure = computed(() => firstError(createKey, revokeKey));
const sessionOf = (tenant?: string) => session.value.filter((k) => k.tenantId === tenant);
</script>

<template>
  <Card class="mb-6 p-3">
    <div class="flex flex-wrap items-start justify-between gap-3 px-3 pt-2">
      <div>
        <h2 class="text-lg font-semibold">{{ t("apps.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("apps.immutable") }}</p>
      </div>
      <Button data-testid="apps-new" @click="creating = true"><Plus class="size-4" />{{ t("apps.new") }}</Button>
    </div>
    <ErrorText :error="apps.error.value" class="px-3" />
    <ErrorText :error="failure" class="px-3" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" class="pt-3" @clear="table.clear()">
      <NativeSelect v-model="table.filters.value.env" class="w-44" :aria-label="t('apps.environments')">
        <option value="">{{ t("apps.allEnvironments") }}</option>
        <option v-for="e in ['sandbox', 'live']" :key="e" :value="e">{{ label(t, te, "environment", e) }}</option>
      </NativeSelect>
    </DataToolbar>
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
        <TableRow v-for="a in table.items.value" :key="a.id">
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
    <p v-else-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <Sheet v-model:open="creating">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("apps.new") }}</SheetTitle>
        <SheetDescription>{{ t("apps.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-app-form" class="grid gap-4" role="form" :aria-label="t('apps.formLabel')" @submit.prevent="submit">
          <div class="flex flex-col gap-1.5">
            <Label for="app-name">{{ t("apps.name") }}</Label>
            <Input id="app-name" v-model="name" :aria-label="t('apps.name')" :placeholder="t('apps.name')" required />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="app-slug">{{ t("apps.slug") }}</Label>
            <Input id="app-slug" v-model="slug" maxlength="60" :aria-label="t('apps.slug')" :placeholder="t('apps.slugPlaceholder')" :aria-invalid="!slugOk" />
          </div>
          <CurrencySelect v-model="currency" :aria-label="t('apps.currency')" />
          <TimezoneSelect v-model="timezone" :aria-label="t('apps.timezone')" />
          <div class="flex flex-col gap-1.5">
            <Label for="app-grace">{{ t("apps.grace") }}</Label>
            <Input id="app-grace" v-model="graceHours" type="number" min="0" max="168" :aria-label="t('apps.grace')" :placeholder="t('apps.gracePlaceholder')" required />
          </div>
          <p v-if="!slugOk" class="text-destructive text-sm" role="alert">{{ t("apps.badSlug") }}</p>
          <ErrorText :error="createApp.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="creating = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-app-form" :disabled="createApp.isPending.value || !accountId || !slugOk">{{ t("apps.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

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
          <span class="mono text-xs">{{ keyMask(k.environment, k.prefix) }}…</span>
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
