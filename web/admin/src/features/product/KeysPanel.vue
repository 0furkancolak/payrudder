<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import { Ban, Check, Copy, Eye, KeyRound, RefreshCw } from "@lucide/vue";
import { Link } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { envOf, keyMask, liveState } from "@/shared/lib/environment";
import { useFormat } from "@/shared/lib/format";
import { copyText } from "@/shared/lib/clipboard";
import { firstError } from "@/shared/lib/errors";
import { FormField } from "@/shared/ui/form-field";
import { Button } from "@/shared/ui/button";
import UpgradeHint from "@/features/saas/UpgradeHint.vue";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { BulkBar, RowCheck } from "@/shared/ui/bulk";
import { useBulk } from "@/shared/lib/bulk";
import { SortSelect } from "@/shared/ui/sort-select";
import { newest, oldest, time } from "@/shared/lib/sortDefs";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions, type RowAction } from "@/shared/ui/row-actions";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { sheetName } from "@/shared/lib/sheets";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { useTableState } from "@/shared/lib/useTableState";
import { useApiKeys, useApp, useCreateApiKey, useMe, useRevokeApiKey, useRotateApiKey, type ApiKey, type Environment } from "./api";
import { ALL_PERMISSIONS, DEFAULT_KEY_ROLE, KEY_RESOURCES, KEY_ROLES, keyRequest, permissionCount, permissionsOfRole, roleOfKey, type KeyPermission, type KeyRole } from "./apiKeys";

/**
 * The API keys of one environment of an app. Sandbox keys are made in the sandbox view and live keys in the live view, never from the same place: the key's
 * environment is the page's, shown on every key and fixed in the form that makes one. A live key is behind the go-live gate (a confirmed address, an active
 * live environment). A new key has a role and permissions the gateway enforces; its secret is shown once, in a dialog, and nothing about it is stored in the
 * browser. The list shows the non-secret prefix only.
 */
const props = defineProps<{ appId: string; canManage: boolean; env: Environment }>();
const { t } = useI18n();
const fmt = useFormat();
const keys = useApiKeys(() => props.appId);
const create = useCreateApiKey(() => props.appId);
const revoke = useRevokeApiKey(() => props.appId);
const rotate = useRotateApiKey(() => props.appId);
const app = useApp(() => props.appId);
const me = useMe();

const live = computed(() => props.env === "live");
/** A live key is refused while the live environment is not active (the server says so too); the page points to the checklist instead. */
const liveBlocked = computed(() => live.value && app.data.value?.app !== undefined && liveState(app.data.value.app ?? undefined) !== "active");
/** A live key needs a verified address; until the profile has loaded the button stays usable and the server decides. */
const unverified = computed(() => live.value && me.data.value?.user?.email_verified === false);
const gate = computed(() => (unverified.value ? "needsVerified" : liveBlocked.value ? "liveInactive" : ""));

const isRevoked = (k: ApiKey) => k.status === "STATUS_REVOKED";
const list = computed(() =>
  (keys.data.value?.api_keys ?? [])
    .filter((k) => envOf(k.environment) === props.env)
    .slice()
    .sort((a, b) => Number(isRevoked(a)) - Number(isRevoked(b))),
);
const table = useTableState(list, {
  sheet: { kind: "key", id: (k) => k.id },
  searchFields: (k) => [k.prefix, k.id, roleOfKey(k)],
  sorts: () => [newest(t, (k) => time(k.create_time)), oldest(t, (k) => time(k.create_time)), { key: "used", label: t("common.sort.lastUsed"), by: (k) => time(k.last_used_time), dir: "desc" }],
  url: "f_", filters: { role: (k, v) => roleOfKey(k) === v, status: (k, v) => (isRevoked(k) ? "revoked" : "active") === v },
});
const when = (v?: string | null) => fmt.date(v, "day");
const keyEnv = (k: ApiKey): Environment => envOf(k.environment);
/** What the list may show of a key: its kind and prefix. The secret part is never known here after creation. */
const shown = (k: ApiKey) => `${keyMask(keyEnv(k), k.prefix)}_••••••••`;
const roleName = (r: KeyRole) => t(`product.keys.roles.${r}`);
const lastUsed = (k: ApiKey) => (k.last_used_time ? t("product.keys.lastUsed", { date: fmt.date(k.last_used_time, "day") }) : t("product.keys.neverUsed"));
const ends = (k: ApiKey) => (k.expire_time && !isRevoked(k) ? t("product.keys.ends", { date: fmt.date(k.expire_time, "day") }) : "");
const total = ALL_PERMISSIONS.length;

// --- a new key: the role and permissions are chosen in a sheet, the environment is the page's
const making = ref(false);
const role = ref<KeyRole>(DEFAULT_KEY_ROLE);
const chosen = ref<Set<KeyPermission>>(new Set());
watch(making, (open) => {
  if (!open) return;
  role.value = DEFAULT_KEY_ROLE;
  chosen.value = new Set();
  create.reset();
});
/** The permissions shown ticked: a fixed role's own, or the ones chosen for a custom key. */
const ticked = computed<ReadonlySet<KeyPermission>>(() => {
  const fixed = permissionsOfRole(role.value);
  return fixed ? new Set(fixed) : chosen.value;
});
const custom = computed(() => role.value === "custom");
function toggle(p: KeyPermission, on: boolean) {
  const next = new Set(chosen.value);
  if (on) next.add(p);
  else next.delete(p);
  chosen.value = next;
}
const request = computed(() => keyRequest(role.value, chosen.value));

// --- a new key's plaintext lives in this ref until the dialog closes
const secret = ref<{ plaintext: string; env: Environment } | null>(null);
const copied = ref(false);
const failed = ref(false);
const reveal = computed({ get: () => secret.value !== null, set: (v) => { if (!v) secret.value = null; } });

function make() {
  const body = request.value;
  if (!body) return;
  copied.value = failed.value = false;
  create.mutate({ environment: props.env, ...body }, { onSuccess: (r) => { secret.value = { plaintext: r.plaintext ?? "", env: props.env }; making.value = false; } });
}

async function copy() {
  if (!secret.value) return;
  copied.value = await copyText(secret.value.plaintext);
  failed.value = !copied.value;
}

// --- what a key may do, read-only
const viewing = ref<ApiKey | null>(null);
const viewOpen = computed({ get: () => viewing.value !== null, set: (v) => { if (!v) viewing.value = null; } });
const viewed = computed<ReadonlySet<string>>(() => (viewing.value ? new Set(roleOfKey(viewing.value) === "admin" ? ALL_PERMISSIONS : (viewing.value.permissions ?? [])) : new Set()));

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

// Viewing and rotating a key are in the address with its id, found among the keys of the environment.
const keyOf = (id: string) => list.value.find((k) => k.id === id);
useSheetTarget("view-key", viewOpen, viewing, { id: (k) => k.id ?? "", find: keyOf });
useSheetTarget("rotate-key", rotateOpen, rotating, { id: (k) => k.id ?? "", find: (id) => (props.canManage ? keyOf(id) : undefined) });

function actionsOf(k: ApiKey): RowAction[] {
  const view: RowAction = { key: `view-${k.id}`, label: t("product.keys.viewPermissions"), icon: Eye, onSelect: () => (viewing.value = k) };
  if (!props.canManage || isRevoked(k)) return [view];
  return [
    view,
    ...(k.expire_time ? [] : [{ key: `rotate-${k.id}`, label: t("product.keys.rotate"), icon: RefreshCw, onSelect: () => (rotating.value = k) }]),
    { key: `revoke-${k.id}`, label: t("product.keys.revoke"), icon: Ban, destructive: true, separatorBefore: true, onSelect: () => (revoking.value = k) },
  ];
}

// --- revoking several at once: the ticked keys that are not revoked yet, after one question
const bulk = useBulk<ApiKey>({ selected: () => table.selectedRows.value, untick: (k) => table.tick(k, false), name: (k) => keyMask(keyEnv(k), k.prefix) });
const bulkRevoke = () =>
  bulk.run((k) => revoke.mutateAsync(k.id ?? ""), {
    rows: (all) => all.filter((k) => !isRevoked(k)),
    confirm: { title: t("product.keys.bulk.revokeTitle"), body: t("product.keys.bulk.confirmRevoke"), confirmLabel: t("product.keys.revoke") },
  });

// --- revoking asks first
const revoking = ref<ApiKey | null>(null);
const confirm = computed({ get: () => revoking.value !== null, set: (v) => { if (!v) revoking.value = null; } });
function doRevoke() {
  const id = revoking.value?.id;
  if (id) revoke.mutate(id, { onSuccess: () => (revoking.value = null) });
}
</script>

<template>
  <Card class="p-3" role="region" :aria-label="t('product.keys.title')" :data-env="env">
    <p class="text-muted-foreground px-3 text-sm" data-testid="keys-env-note">{{ t(`product.keys.envNote.${env}`) }}</p>
    <p class="px-3 text-xs">
      <Link v-if="live" :to="`/app/apps/${appId}/keys`" :search="{ env: 'sandbox' } as never" class="text-primary font-medium underline-offset-4 hover:underline" data-testid="keys-to-sandbox">{{ t("product.keys.toSandbox") }}</Link>
      <Link v-else :to="`/app/apps/${appId}/keys`" :search="{ env: 'live' } as never" class="text-primary font-medium underline-offset-4 hover:underline" data-testid="keys-to-live">{{ t("product.keys.toLive") }}</Link>
    </p>
    <p v-if="canManage && gate === 'needsVerified'" class="text-muted-foreground px-3 text-xs" data-testid="live-needs-verified">{{ t("product.keys.needsVerified") }}</p>
    <p v-else-if="canManage && gate === 'liveInactive'" class="text-muted-foreground px-3 text-xs" data-testid="live-inactive">
      {{ t("product.keys.liveInactive") }}
      <Link :to="`/app/apps/${appId}/go-live`" :search="{ env: 'live' } as never" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.goLive.continue") }}</Link>
    </p>
    <ErrorText :error="firstError(create, revoke, keys)" class="bg-destructive-soft mx-3 rounded-2xl px-4 py-3" />
    <UpgradeHint :error="create.error.value" class="mx-3" />

    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      <FilterSelect v-model="table.filters.value.role" multiple :label="t('product.keys.role')" :options="[{ value: '', label: t('product.keys.allRoles') }, ...KEY_ROLES.map((r) => ({ value: r, label: roleName(r) }))]" />
      <FilterSelect v-model="table.filters.value.status" multiple :label="t('product.keys.filterStatus')" :options="[{ value: '', label: t('product.keys.allStatuses') }, { value: 'active', label: t('product.keys.active') }, { value: 'revoked', label: t('product.keys.revoked') }]" />
      <HeaderAction v-if="canManage" :disabled="!!gate" :title="gate ? t(`product.keys.${gate}`) : undefined" :test-id="live ? 'new-live-key' : 'new-sandbox-key'" :label="t(live ? 'product.keys.newLive' : 'product.keys.newSandbox')" @click="making = true" />
    </DataToolbar>
    <BulkBar v-if="canManage" :count="table.selectedRows.value.length" @clear="table.clearSelection()">
      <Button type="button" size="sm" variant="outline" :disabled="bulk.busy.value" data-testid="bulk-revoke" @click="bulkRevoke">{{ t("product.keys.bulk.revoke") }}</Button>
    </BulkBar>
    <p v-if="bulk.note.value" class="text-muted-foreground px-4 pb-2 text-xs" role="status" data-testid="bulk-note">{{ bulk.note.value }}</p>
    <Table v-if="table.items.value.length > 0">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead v-if="canManage" class="w-10"><RowCheck :label="t('common.bulk.selectPage')" :checked="table.allOnPage.value" :indeterminate="table.someOnPage.value && !table.allOnPage.value" @change="table.togglePage()" /></TableHead>
          <TableHead>{{ t("product.keys.title") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("product.keys.role") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("product.keys.filterEnv") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("product.keys.filterStatus") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="k in table.items.value" :key="k.id">
          <TableCell v-if="canManage"><RowCheck :label="`${t('common.bulk.selectRow')} ${shown(k)}`" :checked="table.isTicked(k)" @change="(on) => table.tick(k, on)" /></TableCell>
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
          <TableCell class="max-md:hidden">
            <StatusPill tone="neutral" :data-testid="`role-${k.id}`">{{ roleName(roleOfKey(k)) }}</StatusPill>
            <p class="text-muted-foreground mt-1 text-xs">{{ t("product.keys.permissionCount", { n: permissionCount(k), total }) }}</p>
          </TableCell>
          <TableCell class="max-md:hidden"><StatusPill :tone="keyEnv(k) === 'live' ? 'info' : 'neutral'" :data-testid="`env-${k.id}`">{{ t(`product.env.${keyEnv(k)}`) }}</StatusPill></TableCell>
          <TableCell class="max-md:hidden"><StatusPill :tone="toneOf(isRevoked(k) ? 'revoked' : 'active')">{{ t(isRevoked(k) ? "product.keys.revoked" : "product.keys.active") }}</StatusPill></TableCell>
          <TableCell><RowActions :actions="actionsOf(k)" :label="`${t('common.moreActions')} ${shown(k)}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-else-if="list.length > 0" class="text-muted-foreground py-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <p v-else-if="!keys.isPending.value" class="text-muted-foreground py-4 text-center text-sm">{{ t(`product.keys.empty.${env}`) }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <Sheet v-model:open="making" :name="sheetName('new-key', env)">
    <SheetContent class="sm:max-w-xl" data-testid="new-key-sheet">
      <SheetHeader>
        <SheetTitle>{{ t(live ? "product.keys.createTitleLive" : "product.keys.createTitleSandbox") }}</SheetTitle>
        <SheetDescription>{{ t(`product.keys.createBody.${env}`) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="new-key" class="flex flex-col gap-4" @submit.prevent="make">
          <div class="flex items-center gap-2 text-sm">
            <span class="text-muted-foreground">{{ t("product.keys.filterEnv") }}</span>
            <StatusPill :tone="live ? 'info' : 'neutral'" data-testid="new-key-env">{{ t(`product.env.${env}`) }}</StatusPill>
          </div>
          <FormField for="key-role" :label="t('product.keys.role')">
            <NativeSelect id="key-role" v-model="role" data-testid="key-role">
              <option v-for="r in KEY_ROLES" :key="r" :value="r">{{ roleName(r) }}</option>
            </NativeSelect>
            <p class="text-muted-foreground text-xs" data-testid="key-role-help">{{ t(`product.keys.roleHelp.${role}`) }}</p>
          </FormField>
          <fieldset class="flex flex-col gap-1">
            <legend class="mb-1 text-sm font-medium">{{ t("product.keys.permissions") }}</legend>
            <p class="text-muted-foreground mb-1 text-xs">{{ custom ? t("product.keys.permissionsCustom") : t("product.keys.permissionsFixed") }}</p>
            <div class="grid grid-cols-[1fr_auto_auto] items-center gap-x-4 gap-y-1.5 text-sm" role="group" :aria-label="t('product.keys.permissions')">
              <template v-for="r in KEY_RESOURCES" :key="r.resource">
                <span>{{ t(`product.keys.resources.${r.resource}`) }}</span>
                <label v-if="r.read" class="flex items-center gap-1.5">
                  <Checkbox :model-value="ticked.has(r.read)" :disabled="!custom" :data-testid="`perm-${r.read}`" :aria-label="`${t(`product.keys.resources.${r.resource}`)}: ${t('product.keys.read')}`" @update:model-value="toggle(r.read, $event === true)" />
                  <span class="text-muted-foreground text-xs">{{ t("product.keys.read") }}</span>
                </label>
                <span v-else></span>
                <label v-if="r.write" class="flex items-center gap-1.5">
                  <Checkbox :model-value="ticked.has(r.write)" :disabled="!custom" :data-testid="`perm-${r.write}`" :aria-label="`${t(`product.keys.resources.${r.resource}`)}: ${t('product.keys.write')}`" @update:model-value="toggle(r.write, $event === true)" />
                  <span class="text-muted-foreground text-xs">{{ t("product.keys.write") }}</span>
                </label>
                <span v-else></span>
              </template>
            </div>
          </fieldset>
          <p v-if="custom && !request" class="text-warning text-xs" role="status" data-testid="key-needs-permission">{{ t("product.keys.customNeedsOne") }}</p>
          <ErrorText :error="create.error.value" box />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="making = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="new-key" :disabled="create.isPending.value || !request" data-testid="create-key">{{ t("product.keys.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

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

  <Sheet v-model:open="viewOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("product.keys.permissionsTitle") }}</SheetTitle>
        <SheetDescription>{{ viewing ? `${roleName(roleOfKey(viewing))} · ${t(`product.env.${keyEnv(viewing)}`)}` : "" }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <p class="mono bg-muted rounded-2xl p-3 text-sm">{{ viewing ? shown(viewing) : "" }}</p>
        <ul class="flex flex-col gap-1.5 text-sm" data-testid="key-permissions">
          <template v-for="r in KEY_RESOURCES" :key="r.resource">
            <li v-for="p in [r.read, r.write].filter((x) => x !== undefined)" :key="p" class="flex items-center justify-between gap-3" :data-granted="viewed.has(p as string)">
              <span>{{ t(`product.keys.resources.${r.resource}`) }} <span class="text-muted-foreground text-xs">{{ (p as string).endsWith(":read") ? t("product.keys.read") : t("product.keys.write") }}</span></span>
              <StatusPill :tone="viewed.has(p as string) ? 'success' : 'neutral'">{{ viewed.has(p as string) ? t("product.keys.allowed") : t("product.keys.notAllowed") }}</StatusPill>
            </li>
          </template>
        </ul>
      </SheetBody>
      <SheetFooter><Button variant="ghost" @click="viewOpen = false">{{ t("common.close") }}</Button></SheetFooter>
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="rotateOpen">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("product.keys.rotateTitle") }}</SheetTitle>
        <SheetDescription>{{ t("product.keys.rotateBody") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="rotate-key" class="flex flex-col gap-4" @submit.prevent="doRotate">
          <p class="mono bg-muted rounded-2xl p-3 text-sm">{{ rotating ? shown(rotating) : "" }}</p>
          <FormField for="rotate-overlap" :label="t('product.keys.overlap')">
            <NativeSelect id="rotate-overlap" v-model="overlap" data-testid="rotate-overlap">
              <option v-for="h in OVERLAPS" :key="h" :value="h">{{ h === 0 ? t("product.keys.overlapNow") : t("product.keys.overlapHours", { n: h }) }}</option>
            </NativeSelect>
          </FormField>
          <ErrorText :error="rotate.error.value" box />
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
