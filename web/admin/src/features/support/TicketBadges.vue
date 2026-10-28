<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { Badge } from "@/shared/ui/badge";
import type { SupportTicket } from "./api";

/** The status, the priority and, when the first answer is late, the SLA mark of a ticket. */
const props = defineProps<{ ticket: Pick<SupportTicket, "status" | "priority" | "sla_breached"> }>();
const { t } = useI18n();
const status = (s: string) => (s === "solved" || s === "closed" ? "success" : s === "pending" ? "outline" : "secondary");
const priority = (p: string) => (p === "urgent" ? "destructive" : p === "high" ? "outline" : "secondary");
</script>

<template>
  <span class="inline-flex flex-wrap items-center gap-1.5">
    <Badge :variant="status(props.ticket.status)" data-testid="support-status">{{ t(`support.status.${props.ticket.status}`) }}</Badge>
    <Badge :variant="priority(props.ticket.priority)" data-testid="support-priority">{{ t(`support.priority.${props.ticket.priority}`) }}</Badge>
    <Badge v-if="props.ticket.sla_breached" variant="destructive" data-testid="support-late">{{ t("support.late") }}</Badge>
  </span>
</template>
