<script setup lang="ts">
import { Outlet } from "@tanstack/vue-router";
import AppNav from "./AppNav.vue";
</script>

<template>
  <div class="mx-auto min-h-screen max-w-[1240px] sm:p-6">
    <div class="bg-background min-h-[calc(100vh-3rem)] px-4 pt-4 pb-16 sm:rounded-[2rem] sm:px-8 sm:pt-6">
      <AppNav />
      <main class="mt-6"><Outlet /></main>
    </div>
  </div>
</template>
