<script setup lang="ts">
import { FileText, Loader2, Upload, X } from "@lucide/vue";
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { CURRENCIES, FALLBACK_CURRENCY, type Currency } from "@/shared/lib/currencies";
import { ymd } from "@/shared/lib/dates";
import { useErrorText } from "@/shared/lib/errors";
import { minorDigits, minorToMajor, parseMajor } from "@/shared/lib/money";
import { LOW_CONFIDENCE, type ParsedReceipt } from "@/shared/lib/receipt/parse";
import { releaseOcr, scanFile } from "@/shared/lib/receipt/scan";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { CATEGORIES, fetchInboxFile, uploadReceipt, useResolveInboxItem, useSaveExpense, type Category, type ExpenseRule, type InboxItem } from "./api";
import { canAutoApprove, matchRule, taxInside } from "./rules";

/**
 * Many receipts at once. Files are dropped (or picked) into a queue and read one by one in the browser (the text layer of a PDF, OCR for images); each becomes a
 * row of a review table whose fields a person confirms, edits or skips; the batch is then saved: every file is stored as the attachment of its expense.
 *
 * With `inbox` items (invoices that arrived by mail) the files are fetched from the server and scanned the same way; a row a recurring-vendor rule approves (the
 * vendor matches and the amount is within the rule's tolerance) is saved without waiting, the rest wait for review. Nothing is read on the server.
 */
const props = defineProps<{ tenantId: string; defaultCurrency: string; apps: { id: string; name: string }[]; rules: ExpenseRule[]; inbox?: InboxItem[] }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const errorText = useErrorText();
const save = useSaveExpense(() => props.tenantId);
const resolve = useResolveInboxItem(() => props.tenantId);

type State = "queued" | "scanning" | "ready" | "failed" | "saving" | "saved";
interface Row {
  key: number;
  file: File;
  inboxId?: string;
  appId: string;
  state: State;
  /** Why the scan or the save failed (a message to show). */
  note: string;
  /** Saved without a person by a rule. */
  auto: boolean;
  include: boolean;
  vendor: string;
  date: string;
  amount: string;
  currency: string;
  tax: string;
  category: Category;
  /** The fields whose reading was not trusted. */
  doubt: Set<string>;
}

const rows = ref<Row[]>([]);
let seq = 0;
const dragging = ref(false);
const picker = ref<HTMLInputElement | null>(null);
const busy = ref(false);
const currencies = computed(() => [...new Set<string>([props.defaultCurrency, ...CURRENCIES])]);

function addFiles(files: Iterable<File>, inbox?: InboxItem) {
  for (const file of files) {
    rows.value.push(
      reactive({
        key: ++seq, file, inboxId: inbox?.id, appId: inbox?.app_id ?? "", state: "queued" as State, note: "", auto: false, include: true, vendor: "", date: ymd(new Date()),
        amount: "", currency: props.defaultCurrency || FALLBACK_CURRENCY, tax: "", category: "other" as Category, doubt: new Set<string>(),
      }) as Row,
    );
  }
  void pump();
}
function onPick(e: Event) {
  const input = e.target as HTMLInputElement;
  addFiles(Array.from(input.files ?? []));
  input.value = "";
}
function onDrop(e: DragEvent) {
  dragging.value = false;
  addFiles(Array.from(e.dataTransfer?.files ?? []));
}

function fill(row: Row, p: ParsedReceipt) {
  const doubt = (name: string, f?: { confidence: number }) => {
    if (!f || f.confidence < LOW_CONFIDENCE) row.doubt.add(name);
  };
  row.vendor = p.vendor?.value ?? "";
  doubt("vendor", p.vendor);
  if (p.date) row.date = p.date.value;
  doubt("date", p.date);
  row.currency = p.currency?.value ?? row.currency;
  doubt("currency", p.currency);
  row.amount = p.amountMinor ? minorToMajor(String(p.amountMinor.value), row.currency) : "";
  doubt("amount", p.amountMinor);
  row.tax = p.taxMinor ? minorToMajor(String(p.taxMinor.value), row.currency) : "";
  const rule = matchRule(props.rules, row.vendor, row.appId);
  if (rule) {
    row.category = (rule.category ?? "other") as Category;
    if (!p.currency || p.currency.confidence < LOW_CONFIDENCE) {
      row.currency = rule.currency ?? row.currency;
      row.doubt.delete("currency");
    }
    if (!row.tax && rule.tax_bps && p.amountMinor) row.tax = minorToMajor(String(taxInside(p.amountMinor.value, rule.tax_bps)), row.currency);
  }
  return rule;
}

let pumping = false;
async function pump() {
  if (pumping) return;
  pumping = true;
  busy.value = true;
  try {
    for (;;) {
      const row = rows.value.find((r) => r.state === "queued");
      if (!row) break;
      row.state = "scanning";
      try {
        const res = await scanFile(row.file, { defaultCurrency: (props.defaultCurrency || FALLBACK_CURRENCY) as Currency });
        const rule = fill(row, res.parsed);
        row.state = "ready";
        if (row.inboxId && canAutoApprove(rule, res.parsed) && rowValid(row)) {
          row.auto = true;
          await saveRow(row);
        }
      } catch (e) {
        row.state = "failed";
        row.note = t(`billing.accounting.scan.errors.${e instanceof Error && ["unsupported", "too_large"].includes(e.message) ? e.message : "unreadable"}`);
        row.include = false;
      }
    }
  } finally {
    pumping = false;
    busy.value = false;
    await releaseOcr();
  }
}

// An inbox item is fetched and queued once; the rows of items that were dismissed or accepted in the meantime are not there.
const queuedInbox = new Set<string>();
watch(
  () => [open.value, props.inbox] as const,
  async ([isOpen, items]) => {
    if (!isOpen || !items) return;
    for (const item of items) {
      if (!item.id || queuedInbox.has(item.id)) continue;
      queuedInbox.add(item.id);
      try {
        addFiles([await fetchInboxFile(props.tenantId, item)], item);
      } catch (e) {
        toast.error(errorText(e));
      }
    }
  },
  { immediate: true },
);

watch(open, (isOpen) => {
  if (isOpen) return;
  rows.value = rows.value.filter((r) => r.state === "scanning");
  queuedInbox.clear();
});
onBeforeUnmount(() => void releaseOcr());

function rowValid(r: Row): boolean {
  const minor = parseMajor(r.amount, r.currency);
  return r.vendor.trim() !== "" && minor !== null && minor !== "0" && /^\d{4}-\d{2}-\d{2}$/.test(r.date) && (r.tax.trim() === "" || parseMajor(r.tax, r.currency) !== null);
}

async function saveRow(r: Row) {
  r.state = "saving";
  r.note = "";
  try {
    const minor = parseMajor(r.amount, r.currency) as string;
    const attachment = await uploadReceipt(props.tenantId, r.file);
    const created = (await save.mutateAsync({
      body: {
        vendor: r.vendor.trim(), category: r.category, amount_minor: minor, currency: r.currency, incurred_on: r.date, recurring: "none", app_id: r.appId || undefined,
        tax_minor: r.tax.trim() === "" ? undefined : (parseMajor(r.tax, r.currency) as string), attachment_asset_id: attachment,
      },
    })) as { id?: string };
    if (r.inboxId) await resolve.mutateAsync({ id: r.inboxId, status: "accepted", expenseId: created.id });
    r.state = "saved";
  } catch (e) {
    r.state = "failed";
    r.note = errorText(e);
  }
}

const toSave = computed(() => rows.value.filter((r) => r.include && (r.state === "ready" || r.state === "failed") && rowValid(r)));
const waiting = computed(() => rows.value.filter((r) => r.state === "ready" && r.include && !rowValid(r)).length);
const saved = computed(() => rows.value.filter((r) => r.state === "saved").length);
const saving = ref(false);
async function saveAll() {
  saving.value = true;
  const batch = toSave.value.slice();
  for (const r of batch) await saveRow(r);
  saving.value = false;
  const ok = batch.filter((r) => r.state === "saved").length;
  if (ok) toast.success(t("billing.accounting.scan.savedCount", { count: ok }));
  if (ok === batch.length && !rows.value.some((r) => r.state === "ready" || r.state === "scanning" || r.state === "queued")) open.value = false;
}

/** Skips a row: a file from the inbox is dismissed there too, so that it does not come back. */
async function skip(r: Row) {
  rows.value = rows.value.filter((x) => x.key !== r.key);
  if (r.inboxId) {
    try {
      await resolve.mutateAsync({ id: r.inboxId, status: "dismissed" });
    } catch (e) {
      toast.error(errorText(e));
    }
  }
}

const stateLabel = (r: Row) => t(`billing.accounting.scan.state.${r.auto && r.state === "saved" ? "auto" : r.state}`);
const doubtful = (r: Row, f: string) => r.doubt.has(f);
const digits = (r: Row) => minorDigits(r.currency);
</script>

<template>
  <Sheet v-model:open="open" :name="inbox ? 'review-inbox' : 'scan-receipt'">
    <SheetContent class="sm:max-w-5xl">
      <SheetHeader>
        <SheetTitle>{{ t("billing.accounting.scan.title") }}</SheetTitle>
        <SheetDescription>{{ t("billing.accounting.scan.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody class="gap-4">
        <div
          class="border-input flex flex-col items-center gap-2 rounded-2xl border border-dashed p-6 text-center transition-colors"
          :class="dragging ? 'bg-accent border-primary' : 'bg-muted/40'"
          data-testid="scan-dropzone"
          @dragover.prevent="dragging = true"
          @dragleave="dragging = false"
          @drop.prevent="onDrop"
        >
          <Upload class="text-muted-foreground size-6" aria-hidden="true" />
          <p class="text-sm font-medium">{{ t("billing.accounting.scan.drop") }}</p>
          <p class="text-muted-foreground text-xs">{{ t("billing.accounting.scan.types") }}</p>
          <Button type="button" variant="outline" size="sm" @click="picker?.click()">{{ t("billing.accounting.scan.choose") }}</Button>
          <input ref="picker" type="file" class="sr-only" multiple accept="application/pdf,image/png,image/jpeg,.pdf,.png,.jpg,.jpeg" data-testid="scan-input" :aria-label="t('billing.accounting.scan.choose')" @change="onPick" />
        </div>
        <p class="text-muted-foreground text-xs">{{ t("billing.accounting.scan.privacy") }}</p>

        <p v-if="!rows.length" class="text-muted-foreground py-6 text-center text-sm">{{ t("billing.accounting.scan.empty") }}</p>

        <ul v-else class="flex flex-col gap-3" data-testid="scan-rows">
          <li v-for="r in rows" :key="r.key" class="border-border/70 flex flex-col gap-3 rounded-2xl border p-3" :data-state="r.state" data-testid="scan-row">
            <div class="flex items-center gap-2">
              <Checkbox v-if="r.state === 'ready' || r.state === 'failed'" :model-value="r.include" :aria-label="t('billing.accounting.scan.include')" @update:model-value="r.include = $event === true" />
              <FileText class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
              <span class="min-w-0 flex-1 truncate text-sm font-medium" dir="auto">{{ r.file.name }}</span>
              <span class="text-muted-foreground flex items-center gap-1 text-xs" role="status">
                <Loader2 v-if="r.state === 'scanning' || r.state === 'saving'" class="size-3.5 animate-spin" aria-hidden="true" />
                {{ stateLabel(r) }}
              </span>
              <Button v-if="r.state !== 'saved'" type="button" variant="ghost" size="icon" class="size-7" :aria-label="t('billing.accounting.scan.skip')" @click="skip(r)"><X class="size-4" aria-hidden="true" /></Button>
            </div>
            <p v-if="r.note" class="text-destructive text-xs" role="alert">{{ r.note }}</p>
            <div v-if="r.state === 'ready' || r.state === 'failed' || r.state === 'saving'" class="grid gap-2 sm:grid-cols-6">
              <label class="grid gap-1 text-xs sm:col-span-2">
                <span class="text-muted-foreground">{{ t("billing.accounting.form.vendor") }}</span>
                <Input v-model="r.vendor" maxlength="120" autocomplete="off" :aria-invalid="doubtful(r, 'vendor') || undefined" :title="doubtful(r, 'vendor') ? t('billing.accounting.scan.check') : undefined" data-field="vendor" />
              </label>
              <label class="grid gap-1 text-xs">
                <span class="text-muted-foreground">{{ t("billing.accounting.form.date") }}</span>
                <Input v-model="r.date" type="date" :aria-invalid="doubtful(r, 'date') || undefined" :title="doubtful(r, 'date') ? t('billing.accounting.scan.check') : undefined" data-field="date" />
              </label>
              <label class="grid gap-1 text-xs">
                <span class="text-muted-foreground">{{ t("billing.accounting.form.amount") }}</span>
                <Input v-model="r.amount" inputmode="decimal" autocomplete="off" :placeholder="digits(r) ? '0.' + '0'.repeat(digits(r)) : '0'" :aria-invalid="doubtful(r, 'amount') || !rowValid(r) || undefined" :title="doubtful(r, 'amount') ? t('billing.accounting.scan.check') : undefined" data-field="amount" />
              </label>
              <label class="grid gap-1 text-xs">
                <span class="text-muted-foreground">{{ t("billing.accounting.form.currency") }}</span>
                <NativeSelect v-model="r.currency" data-field="currency">
                  <option v-for="c in currencies" :key="c" :value="c">{{ c }}</option>
                </NativeSelect>
              </label>
              <label class="grid gap-1 text-xs">
                <span class="text-muted-foreground">{{ t("billing.accounting.scan.tax") }}</span>
                <Input v-model="r.tax" inputmode="decimal" autocomplete="off" data-field="tax" />
              </label>
              <label class="grid gap-1 text-xs sm:col-span-2">
                <span class="text-muted-foreground">{{ t("billing.accounting.form.category") }}</span>
                <NativeSelect v-model="r.category" data-field="category">
                  <option v-for="c in CATEGORIES" :key="c" :value="c">{{ t(`billing.accounting.category.${c}`) }}</option>
                </NativeSelect>
              </label>
              <label v-if="apps.length" class="grid gap-1 text-xs sm:col-span-2">
                <span class="text-muted-foreground">{{ t("billing.accounting.form.app") }}</span>
                <NativeSelect v-model="r.appId">
                  <option value="">{{ t("billing.accounting.form.wholeTenant") }}</option>
                  <option v-for="a in apps" :key="a.id" :value="a.id">{{ a.name }}</option>
                </NativeSelect>
              </label>
            </div>
          </li>
        </ul>
      </SheetBody>
      <SheetFooter>
        <span class="text-muted-foreground me-auto text-xs" role="status">
          <template v-if="busy">{{ t("billing.accounting.scan.reading") }}</template>
          <template v-else-if="waiting">{{ t("billing.accounting.scan.incomplete", { count: waiting }) }}</template>
          <template v-else-if="saved">{{ t("billing.accounting.scan.savedCount", { count: saved }) }}</template>
        </span>
        <Button type="button" variant="outline" @click="open = false">{{ t("billing.accounting.form.cancel") }}</Button>
        <Button type="button" :disabled="saving || toSave.length === 0" data-testid="scan-save" @click="saveAll">{{ t("billing.accounting.scan.save", { count: toSave.length }) }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
