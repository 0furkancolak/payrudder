<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { ApiError } from "@/shared/api/client";
import { Card } from "@/shared/ui/card";
import { Combobox, type ComboboxOption } from "@/shared/ui/combobox";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { FilterSelect } from "@/shared/ui/filter-select";
import { PageHeader } from "@/shared/ui/page-header";

/**
 * The shared controls side by side, for the operator to try them and for the console's tests to drive them: the combobox in its modes (one value, several,
 * creatable, loading from an API, failing), the multi-value filter chip and the date range. It is not linked from the menu (/playground/components).
 */
const { t } = useI18n();

const FRUIT = ["Apple", "Apricot", "Banana", "Blackberry", "Blueberry", "Cherry", "Coconut", "Cranberry", "Date", "Elderberry", "Fig", "Grape", "Guava", "Kiwi", "Lemon", "Lime", "Mango", "Melon", "Orange", "Papaya", "Peach", "Pear", "Plum", "Quince", "Raspberry"];
const options: ComboboxOption[] = FRUIT.map((f) => ({ value: f.toLowerCase(), label: f }));

const single = ref("");
const several = ref<string[]>([]);
const tags = ref<string[]>([]);
const remote = ref("");
const remoteMany = ref<string[]>([]);
const failing = ref("");
const status = ref<string[]>([]);
const range = ref<DateRange>({ from: "", to: "" });

/** A stand-in for an API: answers after a short delay with the fruit that contains the text. */
const calls = ref(0);
function load(text: string, signal: AbortSignal): Promise<ComboboxOption[]> {
  calls.value++;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => resolve(options.filter((o) => o.label.toLowerCase().includes(text.trim().toLowerCase()))), 1000);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(new DOMException("aborted", "AbortError"));
    });
  });
}

/** Fails the first time it is asked after the page opens, then answers: what the retry button of the list is for. */
let failed = false;
async function loadFailing(text: string): Promise<ComboboxOption[]> {
  if (!failed) {
    failed = true;
    throw new ApiError("The list could not be loaded", "UNAVAILABLE", "common.unavailable");
  }
  return options.filter((o) => o.label.toLowerCase().includes(text.trim().toLowerCase()));
}
</script>

<template>
  <div>
    <PageHeader :title="t('playground.components.title')" :description="t('playground.components.intro')" />
    <Card class="mb-4 grid gap-5 p-6 md:grid-cols-2">
      <div class="grid gap-1.5">
        <label class="text-sm" for="cb-single">{{ t("playground.components.single") }}</label>
        <Combobox id="cb-single" v-model="single" :aria-label="t('playground.components.single')" :options="options" :placeholder="t('playground.components.pick')" />
        <p class="text-muted-foreground text-xs" data-testid="cb-single-value">{{ single }}</p>
      </div>
      <div class="grid gap-1.5">
        <label class="text-sm" for="cb-multi">{{ t("playground.components.multiple") }}</label>
        <Combobox id="cb-multi" v-model="several" :aria-label="t('playground.components.multiple')" multiple :options="options" :placeholder="t('playground.components.pick')" />
        <p class="text-muted-foreground text-xs" data-testid="cb-multi-value">{{ several.join(",") }}</p>
      </div>
      <div class="grid gap-1.5">
        <label class="text-sm" for="cb-tags">{{ t("playground.components.creatable") }}</label>
        <Combobox id="cb-tags" v-model="tags" :aria-label="t('playground.components.creatable')" multiple creatable :options="options" :placeholder="t('playground.components.pickOrType')" />
        <p class="text-muted-foreground text-xs" data-testid="cb-tags-value">{{ tags.join(",") }}</p>
      </div>
      <div class="grid gap-1.5">
        <label class="text-sm" for="cb-remote">{{ t("playground.components.remote") }}</label>
        <Combobox id="cb-remote" v-model="remote" :aria-label="t('playground.components.remote')" :load="load" :placeholder="t('playground.components.pick')" />
        <p class="text-muted-foreground text-xs"><span data-testid="cb-remote-value">{{ remote }}</span> · {{ t("playground.components.requests", { n: calls }) }}<span class="sr-only" data-testid="cb-remote-calls">{{ calls }}</span></p>
      </div>
      <div class="grid gap-1.5">
        <label class="text-sm" for="cb-remote-many">{{ t("playground.components.remoteMany") }}</label>
        <Combobox id="cb-remote-many" v-model="remoteMany" :aria-label="t('playground.components.remoteMany')" multiple creatable :load="load" :placeholder="t('playground.components.pick')" />
        <p class="text-muted-foreground text-xs" data-testid="cb-remote-many-value">{{ remoteMany.join(",") }}</p>
      </div>
      <div class="grid gap-1.5">
        <label class="text-sm" for="cb-failing">{{ t("playground.components.failing") }}</label>
        <Combobox id="cb-failing" v-model="failing" :aria-label="t('playground.components.failing')" :load="loadFailing" :placeholder="t('playground.components.pick')" />
        <p class="text-muted-foreground text-xs" data-testid="cb-failing-value">{{ failing }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2 md:col-span-2">
        <FilterSelect
          v-model="status"
          multiple
          :label="t('playground.components.filter')"
          :options="[{ value: '', label: t('playground.components.any') }, { value: 'open', label: 'Open' }, { value: 'paid', label: 'Paid' }, { value: 'void', label: 'Void' }]"
        />
        <DateRangePicker v-model="range" />
        <span class="text-muted-foreground text-xs" data-testid="cb-filter-value">{{ status.join(",") }}</span>
        <span class="text-muted-foreground text-xs" data-testid="cb-range-value">{{ range.from }}…{{ range.to }}</span>
      </div>
    </Card>
  </div>
</template>
