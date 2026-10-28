<script setup lang="ts">
import { ChevronLeft, ChevronRight } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { PAGE_SIZES, pageRange } from "@/shared/lib/paging";
import { cn } from "@/shared/lib/utils";
import { NativeSelect } from "@/shared/ui/native-select";

/**
 * The bar under a table: how many rows a page shows, how many there are, and the way between pages. With `pages` it shows numbered pages (the list is
 * held in full); without it, it is a token-paged list, which can only go to the previous and the next page (`hasPrev`, `hasNext`). The size picker stays
 * while there are rows, so a person who chose a big size can go back to a small one.
 */
const props = defineProps<{ page: number; pages?: number; total?: number; hasPrev?: boolean; hasNext?: boolean; size?: number; sizes?: readonly number[] }>();
const emit = defineEmits<{ "update:page": [number]; "update:size": [number]; prev: []; next: [] }>();
const { t } = useI18n();

const numbered = computed(() => props.pages !== undefined);
const prevOk = computed(() => (numbered.value ? props.page > 1 : !!props.hasPrev));
const nextOk = computed(() => (numbered.value ? props.page < (props.pages ?? 1) : !!props.hasNext));
const items = computed(() => (numbered.value ? pageRange(props.page, props.pages ?? 1) : []));
const showSize = computed(() => props.size !== undefined && (props.total === undefined || props.total > 0));
const sizeModel = computed({ get: () => props.size ?? PAGE_SIZES[0], set: (v) => emit("update:size", Number(v)) });
const jump = ref("");

function go(p: number) {
  if (numbered.value) emit("update:page", p);
}
function step(dir: -1 | 1) {
  if (numbered.value) emit("update:page", props.page + dir);
  else if (dir < 0) emit("prev");
  else emit("next");
}
function submitJump() {
  const p = Number.parseInt(jump.value, 10);
  if (p >= 1 && p <= (props.pages ?? 1)) go(p);
  jump.value = "";
}
const pill = "inline-flex h-8 min-w-8 items-center justify-center rounded-full px-2.5 text-xs font-medium transition-colors focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none";
</script>

<template>
  <div v-if="showSize || prevOk || nextOk || (pages ?? 1) > 1" data-testid="pagination" class="flex flex-col gap-3 px-1 pt-3 pb-2 select-none sm:px-3 sm:pt-4 md:flex-row md:items-center md:justify-between">
    <div v-if="showSize" class="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]">
      <label :for="'ps-' + $.uid">{{ t("pagination.perPage") }}</label>
      <NativeSelect :id="'ps-' + $.uid" v-model="sizeModel" class="bg-muted h-8 w-20 rounded-full border-0 text-xs">
        <option v-for="s in sizes ?? PAGE_SIZES" :key="s" :value="s">{{ s }}</option>
      </NativeSelect>
      <template v-if="total !== undefined">
        <span aria-hidden="true" class="text-muted-foreground/40 max-md:hidden">/</span>
        <span>{{ t("pagination.total", { count: total }) }}</span>
      </template>
    </div>

    <nav v-if="prevOk || nextOk || (pages ?? 1) > 1" :aria-label="t('pagination.goTo')" class="flex items-center gap-1 md:justify-end">
      <button type="button" :disabled="!prevOk" :class="cn(pill, 'text-muted-foreground hover:bg-muted gap-1 disabled:opacity-40')" @click="step(-1)">
        <ChevronLeft class="size-3.5 rtl:rotate-180" aria-hidden="true" />{{ t("pagination.prev") }}
      </button>
      <template v-if="numbered">
        <template v-for="(it, i) in items" :key="i">
          <span v-if="it === 'ellipsis'" class="text-muted-foreground px-1 text-xs" aria-hidden="true">…</span>
          <button v-else type="button" :aria-current="it === page ? 'page' : undefined" :class="cn(pill, it === page ? 'bg-primary text-primary-foreground' : 'text-foreground hover:bg-muted')" @click="go(it)">{{ it }}</button>
        </template>
        <form v-if="(pages ?? 1) > 7" class="ms-1 flex items-center" @submit.prevent="submitJump">
          <input v-model="jump" type="number" min="1" :max="pages" :aria-label="t('pagination.goTo')" :placeholder="t('pagination.page', { page: '…', pages: pages ?? 1 })" class="bg-muted h-8 w-24 rounded-full border-0 px-3 text-center text-xs" />
        </form>
      </template>
      <span v-else class="text-muted-foreground px-2 text-xs">{{ t("pagination.pageOnly", { page }) }}</span>
      <button type="button" :disabled="!nextOk" :class="cn(pill, 'text-muted-foreground hover:bg-muted gap-1 disabled:opacity-40')" @click="step(1)">
        {{ t("pagination.next") }}<ChevronRight class="size-3.5 rtl:rotate-180" aria-hidden="true" />
      </button>
    </nav>
  </div>
</template>
