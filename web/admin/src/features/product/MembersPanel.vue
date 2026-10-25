<script setup lang="ts">
import { Mail, Send, UserMinus } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useChangeRole, useInvitations, useInviteMember, useRemoveMember, useResendInvitation, useRevokeInvitation, type MyAccount, type Role } from "./api";

/**
 * The people of an account: who is a member and in which role, the invitations still open, and an invitation form. What a role may do is decided by the
 * server (owners handle everything, administrators handle administrators and viewers, viewers only read and leave); this panel offers only what the caller's
 * role allows, so no button leads to a refusal that could have been foreseen.
 */
const props = defineProps<{ account: MyAccount | undefined; myId: string; loading: boolean }>();
const emit = defineEmits<{ left: [] }>();
const { t } = useI18n();
const fmt = useFormat();

const myRole = computed(() => (props.account?.role ?? "viewer") as Role);
const isOwner = computed(() => myRole.value === "owner");
const canManage = computed(() => myRole.value === "owner" || myRole.value === "admin");
const members = computed(() => props.account?.members ?? []);
const roleChoices = computed<Role[]>(() => (isOwner.value ? ["owner", "admin", "viewer"] : ["admin", "viewer"]));

const invitations = useInvitations(canManage);
const invite = useInviteMember();
const revokeInvite = useRevokeInvitation();
const resendInvite = useResendInvitation();
const changeRole = useChangeRole();
const remove = useRemoveMember();

/** An administrator may not touch owners; an owner may touch anybody. */
const mayHandle = (role: string | undefined) => canManage.value && (role !== "owner" || isOwner.value);
const nameOf = (m: { display_name?: string; email?: string }) => m.display_name || m.email || "";
const when = (v?: string) => fmt.date(v, "day");
const expired = (v?: string) => (v ? new Date(v).getTime() < Date.now() : false);

const email = ref("");
const role = ref<Role>("viewer");
const sentTo = ref("");
function submitInvite() {
  const address = email.value.trim();
  if (address === "") return;
  invite.mutate({ email: address, role: role.value }, { onSuccess: () => { sentTo.value = address; email.value = ""; } });
}

// Removing and leaving ask first.
const removing = ref<{ id: string; name: string; self: boolean } | null>(null);
const confirm = computed({ get: () => removing.value !== null, set: (v) => { if (!v) removing.value = null; } });
function doRemove() {
  const target = removing.value;
  if (!target) return;
  remove.mutate(target.id, {
    onSuccess: () => {
      removing.value = null;
      if (target.self) emit("left");
    },
  });
}

const error = computed(() => firstError(invite, revokeInvite, resendInvite, changeRole, remove, invitations));
</script>

<template>
  <section class="surface flex flex-col gap-5 p-6" :aria-label="t('product.members.title')">
    <div>
      <h2 class="text-lg font-semibold tracking-tight">{{ t("product.members.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t(canManage ? "product.members.manageNote" : "product.members.readNote") }}</p>
    </div>

    <ErrorText :error="error" class="bg-destructive-soft rounded-2xl px-4 py-3" />

    <Skeleton v-if="loading" class="h-20 rounded-2xl" />
    <ul v-else class="divide-border/70 divide-y" :aria-label="t('product.members.title')">
      <li v-for="m in members" :key="m.user_id" class="flex flex-wrap items-center gap-x-4 gap-y-2 py-3" :data-testid="`member-${m.email}`">
        <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-xl text-sm font-semibold" aria-hidden="true">{{ nameOf(m).slice(0, 1).toUpperCase() || "?" }}</span>
        <div class="min-w-0 flex-1 basis-44">
          <p class="truncate text-sm font-medium">{{ nameOf(m) }}<span v-if="m.user_id === myId" class="text-muted-foreground font-normal"> · {{ t("product.members.you") }}</span></p>
          <p v-if="m.display_name" class="text-muted-foreground truncate text-xs">{{ m.email }}</p>
        </div>
        <NativeSelect
          v-if="mayHandle(m.role) && m.user_id !== myId"
          class="w-36"
          :model-value="m.role"
          :aria-label="t('product.members.roleOf', { name: nameOf(m) })"
          :disabled="changeRole.isPending.value"
          @update:model-value="(v) => changeRole.mutate({ userId: m.user_id ?? '', role: String(v) as Role })"
        >
          <option v-for="r in roleChoices" :key="r" :value="r">{{ t(`product.role.${r}`) }}</option>
        </NativeSelect>
        <StatusPill v-else :tone="m.role === 'owner' ? 'info' : 'neutral'">{{ t(`product.role.${m.role}`) }}</StatusPill>
        <Button v-if="m.user_id === myId" variant="ghost" size="sm" @click="removing = { id: m.user_id ?? '', name: nameOf(m), self: true }">
          <UserMinus />{{ t("product.members.leave") }}
        </Button>
        <Button
          v-else-if="mayHandle(m.role)"
          variant="ghost"
          size="sm"
          :aria-label="t('product.members.removeNamed', { name: nameOf(m) })"
          @click="removing = { id: m.user_id ?? '', name: nameOf(m), self: false }"
        >
          <UserMinus />{{ t("product.members.remove") }}
        </Button>
      </li>
    </ul>

    <template v-if="canManage">
      <form class="border-border/70 flex flex-col gap-3 border-t pt-5" :aria-label="t('product.members.inviteTitle')" @submit.prevent="submitInvite">
        <h3 class="text-base font-semibold tracking-tight">{{ t("product.members.inviteTitle") }}</h3>
        <div class="flex flex-wrap items-end gap-3">
          <div class="flex min-w-56 flex-1 flex-col gap-1.5">
            <Label for="inv-email">{{ t("product.auth.email") }}</Label>
            <Input id="inv-email" v-model="email" type="email" inputmode="email" autocomplete="off" required />
          </div>
          <div class="flex w-40 flex-col gap-1.5">
            <Label for="inv-role">{{ t("product.members.role") }}</Label>
            <NativeSelect id="inv-role" v-model="role"><option v-for="r in roleChoices" :key="r" :value="r">{{ t(`product.role.${r}`) }}</option></NativeSelect>
          </div>
          <Button type="submit" :disabled="invite.isPending.value || email.trim() === ''"><Send />{{ t("product.members.invite") }}</Button>
        </div>
        <p v-if="sentTo" class="text-success text-sm" role="status">{{ t("product.members.invited", { email: sentTo }) }}</p>
      </form>

      <div v-if="(invitations.data.value?.invitations ?? []).length > 0" class="flex flex-col gap-2">
        <h3 class="text-base font-semibold tracking-tight">{{ t("product.members.pending") }}</h3>
        <ul class="divide-border/70 divide-y" :aria-label="t('product.members.pending')">
          <li v-for="i in invitations.data.value?.invitations ?? []" :key="i.id" class="flex flex-wrap items-center gap-x-4 gap-y-2 py-3" :data-testid="`invite-${i.email}`">
            <span class="bg-muted text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Mail class="size-4" /></span>
            <div class="min-w-0 flex-1 basis-44">
              <p class="truncate text-sm font-medium">{{ i.email }}</p>
              <p class="text-muted-foreground text-xs">{{ expired(i.expire_time) ? t("product.members.expiredOn", { date: when(i.expire_time) }) : t("product.members.expiresOn", { date: when(i.expire_time) }) }}</p>
            </div>
            <StatusPill v-if="expired(i.expire_time)" tone="warning">{{ t("product.members.expired") }}</StatusPill>
            <StatusPill :tone="i.role === 'owner' ? 'info' : 'neutral'">{{ t(`product.role.${i.role}`) }}</StatusPill>
            <template v-if="mayHandle(i.role)">
              <Button variant="outline" size="sm" :disabled="resendInvite.isPending.value" :aria-label="t('product.members.resendNamed', { email: i.email })" @click="resendInvite.mutate(i.id ?? '')">{{ t("product.members.resend") }}</Button>
              <Button variant="ghost" size="sm" :disabled="revokeInvite.isPending.value" :aria-label="t('product.members.cancelNamed', { email: i.email })" @click="revokeInvite.mutate(i.id ?? '')">{{ t("product.members.cancel") }}</Button>
            </template>
          </li>
        </ul>
      </div>
    </template>

    <Dialog v-model:open="confirm">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ removing?.self ? t("product.members.leaveTitle") : t("product.members.removeTitle") }}</DialogTitle>
          <DialogDescription>{{ removing?.self ? t("product.members.leaveBody") : t("product.members.removeBody", { name: removing?.name ?? "" }) }}</DialogDescription>
        </DialogHeader>
        <ErrorText :error="remove.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />
        <DialogFooter>
          <Button variant="ghost" @click="confirm = false">{{ t("common.cancel") }}</Button>
          <Button variant="destructive" :disabled="remove.isPending.value" @click="doRemove">{{ removing?.self ? t("product.members.leave") : t("product.members.remove") }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
