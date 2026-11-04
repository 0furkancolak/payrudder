<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { askConfirm } from "@/shared/lib/confirm";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Textarea } from "@/shared/ui/textarea";
import { STATUS_CATEGORIES, type StatusCategory } from "@/features/tenants/api";
import { useBanState, useSetBan, type BanSubject } from "./api";

/**
 * The operator's ban of an account (every live tenant of it behaves as banned; its people can still sign in) or of a person (no sign-in, signed out everywhere; their
 * apps stay as they are). A ban and its lifting need a reason; where a ban needs a second operator the first request is shown as waiting. The person is looked up by
 * sign-in address first, so a typo is not a ban of somebody else.
 */
const props = defineProps<{ kind: "account" | "person"; accountId?: string; email?: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();

const typed = ref(props.email ?? "");
const looked = ref("");
const subject = computed<BanSubject | null>(() => {
  if (props.kind === "account") return props.accountId ? { kind: "account", id: props.accountId } : null;
  return looked.value ? { kind: "person", email: looked.value } : null;
});
watch(
  () => [props.accountId, props.email],
  () => {
    typed.value = props.email ?? "";
    looked.value = "";
    category.value = "";
    reason.value = "";
  },
);
const state = useBanState(subject);
const set = useSetBan();
const banned = computed(() => state.data.value?.banned === true);
const events = computed(() => state.data.value?.events ?? []);
const category = ref<StatusCategory | "">("");
const reason = ref("");
const missingReason = computed(() => reason.value.trim() === "");
// The field is only called wrong once the person has left it empty, not straight after a success cleared it.
const touched = ref(false);

async function apply() {
  const s = subject.value;
  if (!s) return;
  if (missingReason.value) {
    touched.value = true;
    return;
  }
  const target = banned.value ? "lift" : "ban";
  const name = s.kind === "account" ? t("saas.admin.ban.thisAccount") : s.email;
  if (!(await askConfirm({ body: t(`saas.admin.ban.confirm.${s.kind}.${target}`, { name }), destructive: target === "ban" }))) return;
  set.mutate(
    { subject: s, banned: !banned.value, reason: reason.value.trim(), category: category.value || undefined },
    {
      onSuccess: () => {
        toast.success(t(`saas.admin.ban.done.${target}`));
        reason.value = "";
        category.value = "";
        touched.value = false;
      },
    },
  );
}
</script>

<template>
  <section class="mt-6 flex flex-col gap-3 border-t pt-4" :data-testid="`ban-${kind}`">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-medium">{{ t(`saas.admin.ban.${kind}.title`) }}</h3>
      <StatusPill v-if="subject && state.data.value" :tone="banned ? 'danger' : 'success'" :data-testid="`ban-${kind}-state`">{{ t(banned ? "saas.admin.ban.banned" : "saas.admin.ban.notBanned") }}</StatusPill>
    </div>
    <p class="text-muted-foreground text-xs">{{ t(`saas.admin.ban.${kind}.hint`) }}</p>
    <form v-if="kind === 'person'" class="flex items-end gap-2" @submit.prevent="looked = typed.trim()">
      <FormField class="flex-1" for="ban-person-email" :label="t('saas.admin.ban.email')">
        <Input id="ban-person-email" v-model="typed" type="email" autocomplete="off" />
      </FormField>
      <Button type="submit" variant="outline" :disabled="typed.trim() === ''" data-testid="ban-person-lookup">{{ t("saas.admin.ban.lookup") }}</Button>
    </form>
    <ErrorText :error="state.error.value" box />
    <template v-if="subject && state.data.value">
      <form class="flex flex-col gap-3" @submit.prevent="apply">
        <FormField :for="`ban-${kind}-category`" :label="t('tenant.status.category')">
          <NativeSelect :id="`ban-${kind}-category`" v-model="category">
            <option value="">{{ t("tenant.status.categoryNone") }}</option>
            <option v-for="c in STATUS_CATEGORIES" :key="c" :value="c">{{ t(`tenant.status.categories.${c}`) }}</option>
          </NativeSelect>
        </FormField>
        <FormField :for="`ban-${kind}-reason`" :label="t('tenant.status.reason')" :hint="t('saas.admin.ban.reasonRequired')">
          <Textarea :id="`ban-${kind}-reason`" v-model="reason" maxlength="500" :aria-invalid="(touched && missingReason) || undefined" @blur="touched = true" />
        </FormField>
        <ErrorText :error="set.error.value" box />
        <div>
          <Button type="submit" :variant="banned ? 'outline' : 'destructive'" :disabled="!canWrite || missingReason || set.isPending.value" :data-testid="`ban-${kind}-apply`">{{ t(banned ? "saas.admin.ban.lift" : "saas.admin.ban.ban") }}</Button>
        </div>
      </form>
      <ul v-if="events.length" class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm" :data-testid="`ban-${kind}-history`">
        <li v-for="e in events" :key="e.id" class="flex flex-col gap-0.5 py-2">
          <span class="font-medium">{{ t(`saas.admin.ban.kinds.${e.kind}`) }}<template v-if="e.category"> · {{ t(`tenant.status.categories.${e.category}`) }}</template></span>
          <span v-if="e.reason" class="text-muted-foreground break-words" dir="auto">{{ e.reason }}</span>
          <span class="text-muted-foreground text-xs">{{ fmt.date(e.create_time, "dateTime") }} · {{ t("tenant.status.by", { actor: e.actor }) }}<template v-if="e.approved_by"> · {{ t("tenant.status.approvedBy", { actor: e.approved_by }) }}</template></span>
        </li>
      </ul>
    </template>
  </section>
</template>
