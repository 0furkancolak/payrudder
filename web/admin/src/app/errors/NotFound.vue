<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Compass } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { isPerson } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";

/** The router's `defaultNotFoundComponent`: an address no page answers to, with the way back to where the person's pages start. */
const { t } = useI18n();
const home = computed(() => (isPerson.value ? "/app" : "/signin"));
</script>

<template>
  <div class="mx-auto flex w-full max-w-md flex-col items-center gap-3 py-16 text-center" data-testid="not-found">
    <span class="bg-muted text-muted-foreground flex size-12 items-center justify-center rounded-full"><Compass class="size-6" aria-hidden="true" /></span>
    <h1 class="text-xl font-semibold">{{ t("errorState.notFoundTitle") }}</h1>
    <p class="text-muted-foreground text-sm">{{ t("errorState.notFoundBody") }}</p>
    <Button as-child class="mt-2"><Link :to="home">{{ t("errorState.home") }}</Link></Button>
  </div>
</template>
