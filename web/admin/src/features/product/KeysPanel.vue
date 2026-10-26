<script setup lang="ts">
import { Check, Copy, KeyRound, Plus, RefreshCw, Trash2 } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { envOf, keyMask } from "@/shared/lib/environment";
import { useFormat } from "@/shared/lib/format";
import { copyText } from "@/shared/lib/clipboard";
import { firstError } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Label } from "@/shared/ui/label";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { liveState } from "@/shared/lib/environment";
import { Card } from "@/shared/ui/card";
import { FilterSelect } from "@/shared/ui/filter-select";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions, type RowAction } from "@/shared/ui/row-actions";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Link } from "@tanstack/vue-router";
import { useApiKeys, useApp, useCreateApiKey, useMe, useRevokeApiKey, useRotateApiKey, type ApiKey, type Environment, type KeyScope } from "./api";

/**
 * The API keys of an app, both environments. A new key's secret is shown once, in a dialog, with a copy button; closing the dialog drops it from memory
 * and nothing about it is stored in the browser. The list shows the non-secret prefix only.
 */
const props = defineProps<{ appId: string; canManage: boolean }>();
const { t } = useI18n();
const fmt = useFormat();
const keys = useApiKeys(() => props.appId);
const create = useCreateApiKey(() => props.appId);
const revoke = useRevokeApiKey(() => props.appId);
const rotate = useRotateApiKey(() => props.appId);
const app = useApp(() => props.appId);
const me = useMe();
/** The scope a new key gets: the whole API by default, or only sending usage, or only reading. */
const scope = ref<KeyScope>("full");
/** A live key is refused while the live environment is not active (the server says so too); the button points to the checklist instead. */
const liveBlocked = computed(() => app.data.value?.app !== undefined && liveState(app.data.value.app) !== "active");
/** A live key needs a verified address; until the profile has loaded the button stays usable and the server decides. */
const unverified = computed(() => me.data.value?.user?.email_verified === false);

const list = computed(() => (keys.data.value?.api_keys ?? []).slice().sort((a, b) => Number(isRevoked(a)) - Number(isRevoked(b))));
const isRevoked = (k: ApiKey) => k.status === "STATUS_REVOKED";
const table = useTableState(list, {
  searchFields: (k) => [k.prefix, k.id, envOf(k.environment)],
  filters: { env: (k, v) => envOf(k.environment) === v, status: (k, v) => (isRevoked(k) ? "revoked" : "active") === v },
});
const keyEnv = (k: ApiKey): Environment => envOf(k.environment);
const when = (v?: string) => fmt.date(v, "day");
/** What the list may show of a key: its kind and prefix. The secret part is never known here after creation. */
const shown = (k: ApiKey) => `${keyMask(keyEnv(k), k.prefix)}_••••••••`;

// --- a new key: its plaintext lives in this ref until the dialog closes
const secret = ref<{ plaintext: string; env: Environment } | null>(null);
const copied = ref(false);
const failed = ref(false);
const reveal = computed({ get: () => secret.value !== null, set: (v) => { if (!v) secret.value = null; } });

function make(env: Environment) {
  copied.value = failed.value = false;
  create.mutate({ environment: env, scope: scope.value }, { onSuccess: (r) => (secret.value = { plaintext: r.plaintext ?? "", env }) });
}

async function copy() {
  if (!secret.value) return;
  copied.value = await copyText(secret.value.plaintext);
  failed.value = !copied.value;
}

// --- rotating: the new key replaces the old, which keeps working for the overlap so a deployment can switch without a gap
const OVERLAPS = [0, 1, 24, 72, 168];
const rotating = ref<ApiKey | null>(null);
const overlap = ref(24);
const rotateOpen = computed({ get: () => rotating.value !== null, set: (v) => { if (!v) rotating.value = null; } });
function doRotate() {
  const k = rotating.value;
  if (!k?.id) return;
  copied.value = failed.value = false;
  rotate.mutate({ keyId: k.id, overlapHours: overlap.value }, { onSuccess: (r) => { secret.value = { plaintext: r.plaintext ?? "", env: keyEnv(k) }; rotating.value = null; } });
}
const scopeOf = (k: ApiKey): KeyScope => (k.scope === "SCOPE_INGEST" ? "ingest" : k.scope === "SCOPE_READ" ? "read" : "full");
const lastUsed = (k: ApiKey) => (k.last_used_time ? t("product.keys.lastUsed", { date: fmt.date(k.last_used_time, "day") }) : t("product.keys.neverUsed"));
const ends = (k: ApiKey) => (k.expire_time && !isRevoked(k) ? t("product.keys.ends", { date: fmt.date(k.expire_time, "day") }) : "");

function actionsOf(k: ApiKey): RowAction[] {
  if (!props.canManage || isRevoked(k)) return [];
  return [
    ...(k.expire_time ? [] : [{ key: `rotate-${k.id}`, label: t("product.keys.rotate"), icon: RefreshCw, onSelect: () => (rotating.value = k) }]),
    { key: `revoke-${k.id}`, label: t("product.keys.revoke"), icon: Trash2, destructive: true, separatorBefore: !k.expire_time, onSelect: () => (revoking.value = k) },
  ];
}

// --- revoking asks first
const revoking = ref<ApiKey | null>(null);
const confirm = computed({ get: () => revoking.value !== null, set: (v) => { if (!v) revoking.value = null; } });
function doRevoke() {
  const id = revoking.value?.id;
  if (id) revoke.mutate(id, { onSuccess: () => (revoking.value = null) });
}
</script>

<template>
  <Card class="p-3" role="region" :aria-label="t('product.keys.title')">
    <p class="text-muted-foreground px-3 text-xs">{{ t(`product.keys.scopeHelp.${scope}`) }}</p>
    <p v-if="canManage && unverified" class="text-muted-foreground px-3 text-xs" data-testid="live-needs-verified">{{ t("product.keys.needsVerified") }}</p>
    <p v-else-if="canManage && liveBlocked" class="text-muted-foreground px-3 text-xs" data-testid="live-inactive">
      {{ t("product.keys.liveInactive") }}
      <Link :to="`/app/apps/${appId}/go-live`" :search="{ env: 'live' } as never" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.goLive.continue") }}</Link>
    </p>
    <ErrorText :error="firstError(create, revoke, keys)" class="bg-destructive-soft mx-3 rounded-2xl px-4 py-3" />

    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <FilterSelect v-model="table.filters.value.env" :label="t('product.keys.filterEnv')" :options="[{ value: '', label: t('product.keys.allEnvs') }, ...['sandbox', 'live'].map((e) => ({ value: e, label: t(`product.env.${e}`) }))]" />
      <FilterSelect v-model="table.filters.value.status" :label="t('product.keys.filterStatus')" :options="[{ value: '', label: t('product.keys.allStatuses') }, { value: 'active', label: t('product.keys.active') }, { value: 'revoked', label: t('product.keys.revoked') }]" />
      <template v-if="canManage" #actions>
        <NativeSelect v-model="scope" class="w-44" :aria-label="t('product.keys.scope')" data-testid="key-scope">
          <option v-for="s in ['full', 'ingest', 'read']" :key="s" :value="s">{{ t(`product.keys.scopes.${s}`) }}</option>
        </NativeSelect>
        <Button variant="outline" :disabled="create.isPending.value" data-testid="new-sandbox-key" @click="make('sandbox')"><Plus class="size-4" />{{ t("product.keys.newSandbox") }}</Button>
        <Button :disabled="create.isPending.value || unverified || liveBlocked" :title="unverified ? t('product.keys.needsVerified') : liveBlocked ? t('product.keys.liveInactive') : undefined" data-testid="new-live-key" @click="make('live')"><Plus class="size-4" />{{ t("product.keys.newLive") }}</Button>
      </template>
    </DataToolbar>
    <Table v-if="table.items.value.length > 0">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("product.keys.title") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("product.keys.scopeHead") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("product.keys.filterEnv") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("product.keys.filterStatus") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="k in table.items.value" :key="k.id">
          <TableCell>
            <div class="flex items-center gap-3">
              <span class="bg-brand-soft text-brand-strong flex size-8 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><KeyRound class="size-4" /></span>
              <div class="min-w-0">
                <p class="mono text-sm break-all" :class="isRevoked(k) && 'text-muted-foreground line-through'">{{ shown(k) }}</p>
                <p class="text-muted-foreground text-xs">{{ t("product.keys.created", { date: when(k.create_time) }) }} · <span :data-testid="`last-used-${k.id}`">{{ lastUsed(k) }}</span></p>
                <p v-if="ends(k)" class="text-warning text-xs" data-testid="key-ends">{{ ends(k) }}</p>
              </div>
            </div>
          </TableCell>
          <TableCell class="max-md:hidden"><StatusPill tone="neutral" :data-testid="`scope-${k.id}`">{{ t(`product.keys.scopes.${scopeOf(k)}`) }}</StatusPill></TableCell>
          <TableCell class="max-md:hidden"><StatusPill :tone="keyEnv(k) === 'live' ? 'info' : 'neutral'">{{ t(`product.env.${keyEnv(k)}`) }}</StatusPill></TableCell>
          <TableCell class="max-md:hidden"><StatusPill :tone="toneOf(isRevoked(k) ? 'revoked' : 'active')">{{ t(isRevoked(k) ? "product.keys.revoked" : "product.keys.active") }}</StatusPill></TableCell>
          <TableCell><RowActions :actions="actionsOf(k)" :label="`${t('common.moreActions')} ${shown(k)}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-else-if="list.length > 0" class="text-muted-foreground py-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <p v-else-if="!keys.isPending.value" class="text-muted-foreground py-4 text-center text-sm">{{ t("product.keys.empty") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

    <Dialog v-model:open="reveal">
      <DialogContent class="sm:max-w-lg" @interact-outside.prevent>
        <DialogHeader>
          <DialogTitle>{{ t("product.keys.revealTitle") }}</DialogTitle>
          <DialogDescription>{{ t("product.keys.revealBody") }}</DialogDescription>
        </DialogHeader>
        <div class="bg-muted flex items-center gap-2 rounded-2xl p-3">
          <code class="min-w-0 flex-1 text-sm break-all select-all" data-testid="new-key">{{ secret?.plaintext }}</code>
          <Button size="sm" variant="outline" @click="copy"><Check v-if="copied" /><Copy v-else />{{ copied ? t("product.keys.copied") : t("product.keys.copy") }}</Button>
        </div>
        <p v-if="failed" class="text-warning text-sm" role="status">{{ t("product.keys.copyFailed") }}</p>
        <DialogFooter><Button @click="reveal = false">{{ t("product.keys.saved") }}</Button></DialogFooter>
      </DialogContent>
    </Dialog>

    <Sheet v-model:open="rotateOpen">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("product.keys.rotateTitle") }}</SheetTitle>
          <SheetDescription>{{ t("product.keys.rotateBody") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="rotate-key" class="flex flex-col gap-4" @submit.prevent="doRotate">
            <p class="mono bg-muted rounded-2xl p-3 text-sm">{{ rotating ? shown(rotating) : "" }}</p>
            <div class="flex flex-col gap-1.5">
              <Label for="rotate-overlap">{{ t("product.keys.overlap") }}</Label>
              <NativeSelect id="rotate-overlap" v-model="overlap" data-testid="rotate-overlap">
                <option v-for="h in OVERLAPS" :key="h" :value="h">{{ h === 0 ? t("product.keys.overlapNow") : t("product.keys.overlapHours", { n: h }) }}</option>
              </NativeSelect>
            </div>
            <ErrorText :error="rotate.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />
          </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="rotateOpen = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="rotate-key" :disabled="rotate.isPending.value" data-testid="rotate-confirm">{{ t("product.keys.rotate") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>

    <Dialog v-model:open="confirm">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t("product.keys.revokeTitle") }}</DialogTitle>
          <DialogDescription>{{ t("product.keys.revokeBody") }}</DialogDescription>
        </DialogHeader>
        <p class="mono bg-muted rounded-2xl p-3 text-sm">{{ revoking ? shown(revoking) : "" }}</p>
        <DialogFooter>
          <Button variant="ghost" @click="confirm = false">{{ t("common.cancel") }}</Button>
          <Button variant="destructive" :disabled="revoke.isPending.value" @click="doRevoke">{{ t("product.keys.revoke") }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
</template>
