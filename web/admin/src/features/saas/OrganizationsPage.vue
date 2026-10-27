<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { firstError } from "@/shared/lib/errors";
import { useFormat } from "@/shared/lib/format";
import { FormField } from "@/shared/ui/form-field";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useClearOverrides, useOrganizations, useSetOrganizationPlan } from "./api";
import { RESOURCES, isUnlimited, parseLimit, stateTone, type OrganizationRow } from "./lib";

/** The operator's view of the organisations: plan, state and usage of each, and the manual override (a plan without a checkout, limits of its own). */
const { t } = useI18n();
const fmt = useFormat();

const search = ref("");
const q = ref("");
const tokens = ref<string[]>([""]);
const token = computed(() => tokens.value[tokens.value.length - 1] ?? "");
const list = useOrganizations(q, token);
const rows = computed(() => list.data.value?.organizations ?? []);
const next = computed(() => list.data.value?.next_page_token ?? "");
const plans = ref<{ code: string; name: string }[]>([
  { code: "free", name: "Free" },
  { code: "pro", name: "Pro" },
  { code: "scale", name: "Scale" },
]);
function find() {
  q.value = search.value.trim();
  tokens.value = [""];
}
function forward() {
  if (next.value) tokens.value = [...tokens.value, next.value];
}
function back() {
  if (tokens.value.length > 1) tokens.value = tokens.value.slice(0, -1);
}

const chosen = ref<OrganizationRow | null>(null);
const open = computed({ get: () => chosen.value !== null, set: (v) => { if (!v) chosen.value = null; } });
const planCode = ref("");
const limits = ref<Record<string, string>>({});
const bad = ref(false);
const set = useSetOrganizationPlan();
const clear = useClearOverrides();
watch(chosen, (row) => {
  planCode.value = row?.plan_code ?? "";
  limits.value = {};
  bad.value = false;
  set.reset();
  clear.reset();
});
// The plans on offer come from the first organisation's plan page in the platform tenant; the three seeded codes are the fallback above.
const planOptions = computed(() => {
  const codes = new Set(plans.value.map((p) => p.code));
  const extra = rows.value.filter((r) => r.plan_code && !codes.has(r.plan_code)).map((r) => ({ code: r.plan_code ?? "", name: r.plan_name || r.plan_code || "" }));
  return [...plans.value, ...extra];
});

function save() {
  const row = chosen.value;
  if (!row?.account_id) return;
  const out: Record<string, number> = {};
  bad.value = false;
  for (const r of RESOURCES) {
    const v = parseLimit(limits.value[r] ?? "");
    if (v === null) {
      bad.value = true;
      return;
    }
    if (v !== undefined) out[r] = v;
  }
  set.mutate(
    { accountId: row.account_id, ...(planCode.value && planCode.value !== row.plan_code ? { plan_code: planCode.value } : {}), ...(Object.keys(out).length ? { limits: out } : {}) },
    {
      onSuccess: () => {
        toast.success(t("saas.admin.saved"));
        chosen.value = null;
      },
    },
  );
}
function clearAll() {
  const row = chosen.value;
  if (!row?.account_id) return;
  clear.mutate(row.account_id, {
    onSuccess: () => {
      toast.success(t("saas.admin.cleared"));
      chosen.value = null;
    },
  });
}
const events = (r: OrganizationRow) => `${fmt.count(r.events_used)} / ${isUnlimited(r.events_limit) ? t("saas.unlimited") : fmt.count(r.events_limit)}`;
</script>

<template>
  <section class="flex flex-col gap-6" data-testid="organisations-page">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("saas.admin.title") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("saas.admin.intro") }}</p>
    </header>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="find">
      <div class="flex min-w-60 flex-1 flex-col gap-1.5">
        <Label for="org-search">{{ t("saas.admin.search") }}</Label>
        <Input id="org-search" v-model="search" type="search" autocomplete="off" />
      </div>
      <Button type="submit" variant="outline">{{ t("saas.admin.find") }}</Button>
    </form>
    <ErrorText :error="firstError(list)" box />
    <Skeleton v-if="list.isPending.value" class="h-40 rounded-2xl" />
    <div v-else class="surface overflow-x-auto p-2">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t("saas.admin.organisation") }}</TableHead>
            <TableHead>{{ t("saas.admin.plan") }}</TableHead>
            <TableHead class="text-end">{{ t("saas.resource.apps") }}</TableHead>
            <TableHead class="text-end">{{ t("saas.resource.members") }}</TableHead>
            <TableHead class="text-end">{{ t("saas.resource.api_keys") }}</TableHead>
            <TableHead class="text-end">{{ t("saas.resource.events") }}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-if="rows.length === 0">
            <TableCell colspan="7" class="text-muted-foreground">{{ t("saas.admin.empty") }}</TableCell>
          </TableRow>
          <TableRow v-for="r in rows" :key="r.account_id" :data-testid="`org-${r.account_id}`">
            <TableCell>
              <div class="font-medium">{{ r.name }}</div>
              <div class="text-muted-foreground text-xs">{{ r.owner_email }}</div>
            </TableCell>
            <TableCell>
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium">{{ r.plan_name || r.plan_code }}</span>
                <StatusPill :tone="stateTone(r.state)">{{ t(`saas.state.${r.state || 'none'}`) }}</StatusPill>
                <StatusPill v-if="r.overridden" tone="warning">{{ t("saas.overridden") }}</StatusPill>
              </div>
            </TableCell>
            <TableCell class="text-end">{{ fmt.count(r.apps) }}</TableCell>
            <TableCell class="text-end">{{ fmt.count(r.members) }}</TableCell>
            <TableCell class="text-end">{{ fmt.count(r.api_keys) }}</TableCell>
            <TableCell class="text-end">{{ events(r) }}</TableCell>
            <TableCell class="text-end"><Button size="sm" variant="outline" :data-testid="`manage-${r.account_id}`" @click="chosen = r">{{ t("saas.admin.manage") }}</Button></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <div class="flex justify-end gap-2 px-2 pt-2">
        <Button variant="ghost" size="sm" :disabled="tokens.length <= 1" @click="back">{{ t("pagination.prev") }}</Button>
        <Button variant="ghost" size="sm" :disabled="!next" @click="forward">{{ t("pagination.next") }}</Button>
      </div>
    </div>

    <Sheet v-model:open="open" name="organisation-plan">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ chosen?.name }}</SheetTitle>
          <SheetDescription>{{ t("saas.admin.sheetIntro") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="org-plan" class="flex flex-col gap-4" @submit.prevent="save">
            <FormField for="org-plan-code" :label="t('saas.admin.plan')">
              <NativeSelect id="org-plan-code" v-model="planCode">
                <option v-for="p in planOptions" :key="p.code" :value="p.code">{{ p.name }}</option>
              </NativeSelect>
            </FormField>
            <p class="text-muted-foreground text-xs">{{ t("saas.admin.limitsHint") }}</p>
            <div v-for="r in RESOURCES" :key="r" class="flex flex-col gap-1.5">
              <Label :for="`org-limit-${r}`">{{ t(`saas.resource.${r}`) }}</Label>
              <Input :id="`org-limit-${r}`" v-model="limits[r]" inputmode="numeric" autocomplete="off" :placeholder="t('saas.admin.keep')" />
            </div>
            <ErrorText v-if="bad" :message="t('saas.admin.badLimit')" />
            <ErrorText :error="firstError(set, clear)" box />
          </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" :disabled="clear.isPending.value || !chosen?.overridden" data-testid="org-clear" @click="clearAll">{{ t("saas.admin.clear") }}</Button>
          <Button type="submit" form="org-plan" :disabled="set.isPending.value" data-testid="org-save">{{ t("saas.admin.save") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </section>
</template>
