<script setup lang="ts">
import { History, TriangleAlert } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useContractSettings, useContractVersions } from "./api";
import HistorySheet from "./HistorySheet.vue";
import { AUDIENCES, KINDS, currentVersions, languageNames, parseSeriesKey, seriesKey, type Audience, type Kind } from "./lib";
import VersionSheet from "./VersionSheet.vue";

/**
 * What the seller presents to its customers: for each agreement (distance sales, pre-information, terms, privacy) and audience (consumer, business) the
 * version customers see now, and the way to publish the next one or read the history. A text that still carries the starter notice is said so, on the
 * page and on its row, until a version without it is published.
 */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const versions = useContractVersions(() => props.tenantId);
const settings = useContractSettings(() => props.tenantId);

type Series = { kind: Kind; audience: Audience };
const current = computed(() => currentVersions(versions.data.value));
const rows = computed(() => KINDS.flatMap((kind) => AUDIENCES.map((audience) => ({ kind, audience, key: seriesKey(kind, audience), version: current.value.get(seriesKey(kind, audience)) }))));
/** "Distance sales contract (Consumer)". */
const seriesName = (kind: Kind, audience: Audience): string => `${t(`contracts.kinds.${kind}`)} (${t(`contracts.audiences.${audience}`)})`;
const anyPublished = computed(() => current.value.size > 0);
const templates = computed(() => rows.value.filter((r) => r.version?.has_template_notice));

// The editor and the history are sheets named in the address (`publish-agreement:<kind>~<audience>`), so a reload brings them back once the versions are in.
const editing = ref<Series | null>(null);
const reading = ref<Series | null>(null);
const openOf = (target: typeof editing) =>
  computed({
    get: () => target.value !== null,
    set: (v) => {
      if (!v) target.value = null;
    },
  });
const editingOpen = openOf(editing);
const readingOpen = openOf(reading);
const find = (key: string) => (versions.isSuccess.value ? parseSeriesKey(key) : undefined);
useSheetTarget("publish-agreement", editingOpen, editing, { id: (s) => seriesKey(s.kind, s.audience), find });
useSheetTarget("agreement-history", readingOpen, reading, { id: (s) => seriesKey(s.kind, s.audience), find });
</script>

<template>
  <div class="flex flex-col gap-4">
    <div v-if="templates.length > 0" class="bg-warning-soft text-warning flex items-start gap-3 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="contracts-template-banner">
      <TriangleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <div class="flex flex-col gap-1">
        <span class="font-medium">{{ t("contracts.agreements.templateTitle") }}</span>
        <span>{{ t("contracts.agreements.templateBody") }}</span>
        <span class="text-xs">{{ templates.map((r) => seriesName(r.kind, r.audience)).join(", ") }}</span>
      </div>
    </div>

    <Card class="gap-0 p-3" data-testid="contracts-agreements">
      <p class="text-muted-foreground px-3 pt-2 pb-3 text-sm">{{ t("contracts.agreements.intro") }}</p>
      <ErrorState :error="versions.error.value" :retrying="versions.isFetching.value" compact @retry="versions.refetch()" />
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("contracts.agreements.columns.agreement") }}</TableHead>
            <TableHead>{{ t("contracts.agreements.columns.version") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("contracts.agreements.columns.languages") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("contracts.agreements.columns.published") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="versions.isPending.value">
            <TableRow v-for="n in 4" :key="n" class="hover:bg-transparent">
              <TableCell v-for="c in 5" :key="c"><Skeleton class="h-4 w-24" /></TableCell>
            </TableRow>
          </template>
          <template v-else>
            <TableRow v-for="r in rows" :key="r.key" :data-testid="`agreement-${r.key}`" :data-published="r.version !== undefined">
              <TableCell class="min-w-0">
                <div class="flex flex-col gap-1">
                  <span class="flex flex-wrap items-center gap-2 font-medium">
                    {{ t(`contracts.kinds.${r.kind}`) }}
                    <StatusPill v-if="settings.data.value?.required_kinds.includes(r.kind)" tone="info" data-testid="agreement-required">{{ t("contracts.agreements.required") }}</StatusPill>
                  </span>
                  <span class="text-muted-foreground text-xs">{{ t(`contracts.audiences.${r.audience}`) }}<template v-if="r.audience === 'business' && !r.version"> · {{ t("contracts.agreements.fallback") }}</template></span>
                </div>
              </TableCell>
              <TableCell>
                <div v-if="r.version" class="flex flex-wrap items-center gap-1.5">
                  <span class="font-medium tabular-nums" data-testid="agreement-number">{{ t("contracts.history.version", { n: r.version.number }) }}</span>
                  <StatusPill :tone="r.version.material ? 'info' : 'neutral'">{{ r.version.material ? t("contracts.agreements.material") : t("contracts.agreements.minor") }}</StatusPill>
                  <StatusPill v-if="r.version.has_template_notice" tone="warning" data-testid="agreement-template">{{ t("contracts.agreements.templateBadge") }}</StatusPill>
                </div>
                <span v-else class="text-muted-foreground">{{ t("contracts.agreements.none") }}</span>
              </TableCell>
              <TableCell class="text-muted-foreground max-lg:hidden">{{ r.version ? languageNames(r.version) : "—" }}</TableCell>
              <TableCell class="text-muted-foreground max-md:hidden">
                <template v-if="r.version">{{ fmt.date(r.version.create_time, "day") }}<template v-if="r.version.author"> · {{ t("contracts.agreements.by", { author: r.version.author }) }}</template></template>
                <template v-else>—</template>
              </TableCell>
              <TableCell class="text-end">
                <div class="flex justify-end gap-1.5">
                  <Button v-if="r.version" size="sm" variant="ghost" :aria-label="`${t('contracts.agreements.history')}: ${seriesName(r.kind, r.audience)}`" :data-testid="`agreement-history-${r.key}`" @click="reading = { kind: r.kind, audience: r.audience }"><History aria-hidden="true" />{{ t("contracts.agreements.history") }}</Button>
                  <Button v-if="canWrite" size="sm" :variant="r.version ? 'outline' : 'default'" :data-testid="`agreement-publish-${r.key}`" @click="editing = { kind: r.kind, audience: r.audience }">{{ r.version ? t("contracts.agreements.newVersion") : t("contracts.agreements.publishFirst") }}</Button>
                </div>
              </TableCell>
            </TableRow>
          </template>
        </TableBody>
      </Table>
      <p v-if="!versions.isPending.value && !anyPublished" class="text-muted-foreground px-3 pt-3 text-sm" data-testid="contracts-empty">{{ t("contracts.agreements.empty") }}</p>
    </Card>

    <VersionSheet v-model:open="editingOpen" :tenant-id="tenantId" :series="editing" :versions="versions.data.value ?? []" :settings="settings.data.value" :loaded="versions.isSuccess.value" />
    <HistorySheet v-model:open="readingOpen" :series="reading" :versions="versions.data.value ?? []" />
  </div>
</template>
