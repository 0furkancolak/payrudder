<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed, useId } from "vue";
import { useI18n } from "vue-i18n";
import { ALL_APPS } from "@/shared/lib/scope";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { scopeTarget } from "./scopeNav";
import { useShell } from "./useShell";

/**
 * The "App" field at the top of every create and edit form: it says which app the form writes to and starts on the app the page is about. A form's
 * other fields (customers, packages, currencies) belong to that app, so choosing another app moves the page to it (the form stays open in the address)
 * instead of leaving a half-changed form behind. "All apps" is never offered: a form writes to one app.
 */
const { t } = useI18n();
const navigate = useNavigate();
const shell = useShell();
const id = useId();

const apps = computed(() => shell.apps.data.value?.apps ?? []);
const scope = computed(() => shell.scope.value);
/** Only where there is an app to name: not for the operator's pages, not before the apps arrived, and not on the view of every app. */
const shown = computed(() => apps.value.length > 0 && !!scope.value && scope.value !== ALL_APPS && !shell.inOperatorArea.value);

function pick(value: string | number | undefined) {
  const to = String(value ?? "");
  if (!to || to === scope.value) return;
  const env = shell.env.value ?? "live";
  const target = scopeTarget(shell.path.value, to, apps.value, env);
  // The form that is open stays open in the other app.
  const sheet = shell.search.value.sheet;
  void navigate({ to: target.to, search: { ...target.search, ...(typeof sheet === "string" && sheet ? { sheet } : {}) } as never });
}
</script>

<template>
  <div v-if="shown" class="grid gap-1.5" data-testid="app-field">
    <Label :for="id">{{ t("scope.app") }}</Label>
    <NativeSelect :id="id" :model-value="scope ?? ''" class="w-full" @update:model-value="pick">
      <option v-for="a in apps" :key="a.id" :value="a.id">{{ a.name }}</option>
    </NativeSelect>
  </div>
</template>
