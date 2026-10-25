<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { PROVIDERS } from "@/shared/lib/currencies";
import { usePackages } from "@/features/packages";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCatalogue, useSyncCatalogue } from "./api";
import type { Environment } from "@/shared/lib/environment";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const errorText = useErrorText();
const tenantId = computed(() => props.tenantId);
const packages = usePackages(tenantId);
const provider = ref("");
const environment = ref<Environment>("live");
const plan = ref("");
const catalogue = useCatalogue(tenantId, provider, environment);
const sync = useSyncCatalogue(tenantId, provider, environment);
const summary = computed(() => sync.data.value);
const skipped = computed(() => Object.entries(summary.value?.skipped ?? {}));
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.providers.catalogue.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.providers.catalogue.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto]" role="form" :aria-label="t('billing.providers.catalogue.title')" @submit.prevent="sync.mutate(plan)">
      <NativeSelect v-model="provider" :aria-label="t('billing.providers.provider')" required>
        <option value="" disabled>{{ t("billing.providers.provider") }}</option>
        <option v-for="p in PROVIDERS.filter((x) => x.runsSubscriptions)" :key="p.name" :value="p.name">{{ p.name }}</option>
      </NativeSelect>
      <NativeSelect v-model="environment" :aria-label="t('billing.providers.environment')">
        <option value="live">live</option>
        <option value="sandbox">sandbox</option>
      </NativeSelect>
      <NativeSelect v-model="plan" :aria-label="t('billing.providers.catalogue.package')" required>
        <option value="" disabled>{{ t("billing.providers.catalogue.package") }}</option>
        <option v-for="p in packages.data.value ?? []" :key="p.plan.id" :value="p.plan.code">{{ p.plan.code }}</option>
      </NativeSelect>
      <Button type="submit" :disabled="sync.isPending.value || provider === '' || plan === ''">{{ t("billing.providers.catalogue.sync") }}</Button>
    </form>
    <p v-if="sync.error.value" class="text-destructive text-sm" role="alert">{{ errorText(sync.error.value) }}</p>
    <div v-if="summary" class="bg-secondary/50 grid gap-1 rounded-2xl px-4 py-3 text-sm" role="status">
      <p>{{ t("billing.providers.catalogue.result", { created: Object.keys(summary.created ?? {}).length, existing: Object.keys(summary.existing ?? {}).length }) }}</p>
      <p v-for="[key, why] in skipped" :key="key" class="text-muted-foreground">{{ t("billing.providers.catalogue.skipped", { key, why }) }}</p>
    </div>
    <Table v-if="(catalogue.data.value?.objects ?? []).length > 0">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.providers.catalogue.kind") }}</TableHead>
          <TableHead>{{ t("billing.providers.catalogue.key") }}</TableHead>
          <TableHead>{{ t("billing.providers.reference") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="o in catalogue.data.value?.objects ?? []" :key="`${o.kind}-${o.key}`">
          <TableCell><Badge variant="secondary">{{ o.kind }}</Badge></TableCell>
          <TableCell class="mono text-xs">{{ o.key }}</TableCell>
          <TableCell class="mono text-xs">{{ o.ref }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-else-if="provider !== '' && !catalogue.isPending.value" class="text-muted-foreground text-sm">{{ t("billing.providers.catalogue.empty") }}</p>
  </Card>
</template>
