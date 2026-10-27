<script setup lang="ts">
import { Mail, Plus, RefreshCw, Send, UserMinus, X } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { firstError } from "@/shared/lib/errors";
import { FormField } from "@/shared/ui/form-field";
import { ErrorText } from "@/shared/ui/error-text";
import UpgradeHint from "@/features/saas/UpgradeHint.vue";
import { Button } from "@/shared/ui/button";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Card } from "@/shared/ui/card";
import { FilterSelect } from "@/shared/ui/filter-select";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
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
/** The only owner cannot leave (the server refuses with identity.member.last_owner): the way out is to make somebody else an owner first, and the page says so. */
const soleOwner = computed(() => isOwner.value && members.value.filter((m) => m.role === "owner").length === 1);
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
const when = (v?: string | null) => fmt.date(v, "day");
const expired = (v?: string | null) => (v ? new Date(v).getTime() < Date.now() : false);

const table = useTableState(members, {
  sheet: { kind: "member", id: (m) => m.user_id }, searchFields: (m) => [m.display_name, m.email],
  url: true, filters: { role: (m, v) => m.role === v },
});

const inviting = ref(false);
const email = ref("");
const role = ref<Role>("viewer");
const sentTo = ref("");
watch(inviting, (v) => {
  if (v) {
    email.value = "";
    role.value = "viewer";
    invite.reset();
  }
});
function submitInvite() {
  const address = email.value.trim();
  if (address === "") return;
  invite.mutate({ email: address, role: role.value }, { onSuccess: () => { sentTo.value = address; email.value = ""; inviting.value = false; } });
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

const error = computed(() => firstError(revokeInvite, resendInvite, changeRole, remove, invitations));
</script>

<template>
  <p class="text-muted-foreground px-1 text-sm">{{ t(canManage ? "product.members.manageNote" : "product.members.readNote") }}</p>

  <Card class="p-3" role="region" :aria-label="t('product.members.title')">
    <p v-if="sentTo" class="text-success px-3 pb-2 text-sm" role="status">{{ t("product.members.invited", { email: sentTo }) }}</p>
    <ErrorText :error="error" class="bg-destructive-soft mx-3 mb-3 rounded-2xl px-4 py-3" />
    <Skeleton v-if="loading" class="mx-3 h-20 rounded-2xl" />
    <template v-else>
      <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
        <FilterSelect v-model="table.filters.value.role" multiple :label="t('product.members.role')" :options="[{ value: '', label: t('product.members.allRoles') }, ...['owner', 'admin', 'viewer'].map((r) => ({ value: r, label: t(`product.role.${r}`) }))]" />
        <template v-if="canManage" #actions>
          <Button data-testid="invite-open" @click="inviting = true"><Plus class="size-4" />{{ t("product.members.inviteTitle") }}</Button>
        </template>
      </DataToolbar>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("product.members.title") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("product.auth.email") }}</TableHead>
            <TableHead>{{ t("product.members.role") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="m in table.items.value" :key="m.user_id" :data-testid="`member-${m.email}`">
            <TableCell>
              <div class="flex items-center gap-3">
                <span class="bg-brand-soft text-brand-strong flex size-8 shrink-0 items-center justify-center rounded-xl text-sm font-semibold max-sm:hidden" aria-hidden="true">{{ nameOf(m).slice(0, 1).toUpperCase() || "?" }}</span>
                <span class="truncate text-sm font-medium">{{ nameOf(m) }}<span v-if="m.user_id === myId" class="text-muted-foreground font-normal"> · {{ t("product.members.you") }}</span></span>
              </div>
            </TableCell>
            <TableCell class="text-muted-foreground max-md:hidden">{{ m.email }}</TableCell>
            <TableCell>
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
            </TableCell>
            <TableCell>
              <RowActions
                v-if="m.user_id === myId && !soleOwner"
                :actions="[{ key: 'leave', label: t('product.members.leave'), icon: UserMinus, onSelect: () => (removing = { id: m.user_id ?? '', name: nameOf(m), self: true }) }]"
              />
              <RowActions
                v-else-if="mayHandle(m.role)"
                :actions="[{ key: 'remove', label: t('product.members.remove'), icon: UserMinus, destructive: true, onSelect: () => (removing = { id: m.user_id ?? '', name: nameOf(m), self: false }) }]"
              />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="soleOwner" class="text-muted-foreground px-3 pt-3 text-xs" data-testid="sole-owner-note">{{ t("product.members.soleOwner") }}</p>
      <p v-if="members.length > 0 && table.total.value === 0" class="text-muted-foreground py-4 text-center text-sm">{{ t("common.noMatches") }}</p>
      <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
    </template>
  </Card>

  <Card v-if="canManage && (invitations.data.value?.invitations ?? []).length > 0" class="p-3" role="region" :aria-label="t('product.members.pending')">
    <h2 class="px-3 pt-2 text-base font-semibold tracking-tight">{{ t("product.members.pending") }}</h2>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("product.auth.email") }}</TableHead>
          <TableHead>{{ t("product.members.role") }}</TableHead>
          <TableHead>{{ t("product.members.expiresHead") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="i in invitations.data.value?.invitations ?? []" :key="i.id" :data-testid="`invite-${i.email}`">
          <TableCell>
            <div class="flex items-center gap-3">
              <span class="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Mail class="size-4" /></span>
              <span class="truncate text-sm font-medium">{{ i.email }}</span>
            </div>
          </TableCell>
          <TableCell><StatusPill :tone="i.role === 'owner' ? 'info' : 'neutral'">{{ t(`product.role.${i.role}`) }}</StatusPill></TableCell>
          <TableCell class="text-muted-foreground text-xs">
            {{ expired(i.expire_time) ? t("product.members.expiredOn", { date: when(i.expire_time) }) : t("product.members.expiresOn", { date: when(i.expire_time) }) }}
            <StatusPill v-if="expired(i.expire_time)" tone="warning" class="ms-2">{{ t("product.members.expired") }}</StatusPill>
          </TableCell>
          <TableCell>
            <RowActions
              v-if="mayHandle(i.role)"
              :actions="[
                { key: 'resend', label: t('product.members.resend'), icon: RefreshCw, disabled: resendInvite.isPending.value, onSelect: () => resendInvite.mutate(i.id ?? '') },
                { key: 'cancel', label: t('product.members.cancel'), icon: X, destructive: true, separatorBefore: true, disabled: revokeInvite.isPending.value, onSelect: () => revokeInvite.mutate(i.id ?? '') },
              ]"
              :label="`${t('common.moreActions')} ${i.email}`"
            />
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>

    <Sheet v-model:open="inviting" name="invite-member">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("product.members.inviteTitle") }}</SheetTitle>
          <SheetDescription>{{ t("product.members.manageNote") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="invite-member" class="flex flex-col gap-4" :aria-label="t('product.members.inviteTitle')" @submit.prevent="submitInvite">
            <FormField for="inv-email" :label="t('product.auth.email')">
              <Input id="inv-email" v-model="email" type="email" inputmode="email" autocomplete="off" required />
            </FormField>
            <FormField for="inv-role" :label="t('product.members.role')">
              <NativeSelect id="inv-role" v-model="role"><option v-for="r in roleChoices" :key="r" :value="r">{{ t(`product.role.${r}`) }}</option></NativeSelect>
            </FormField>
            <ErrorText :error="invite.error.value" box />
            <UpgradeHint :error="invite.error.value" />
          </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="inviting = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="invite-member" :disabled="invite.isPending.value || email.trim() === ''"><Send />{{ t("product.members.invite") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>

    <Dialog v-model:open="confirm">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ removing?.self ? t("product.members.leaveTitle") : t("product.members.removeTitle") }}</DialogTitle>
          <DialogDescription>{{ removing?.self ? t("product.members.leaveBody") : t("product.members.removeBody", { name: removing?.name ?? "" }) }}</DialogDescription>
        </DialogHeader>
        <ErrorText :error="remove.error.value" box />
        <DialogFooter>
          <Button variant="ghost" @click="confirm = false">{{ t("common.cancel") }}</Button>
          <Button variant="destructive" :disabled="remove.isPending.value" @click="doRemove">{{ removing?.self ? t("product.members.leave") : t("product.members.remove") }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
</template>
