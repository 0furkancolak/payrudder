<script setup lang="ts">
import { Outlet } from "@tanstack/vue-router";
import { operatorToken } from "@/shared/lib/session";
import AppNav from "./AppNav.vue";
</script>

<template>
  <div class="min-h-screen md:flex">
    <AppNav />
    <main :class="['min-w-0 flex-1 px-4 py-6 md:px-10 md:py-10', operatorToken === null && 'mx-auto max-w-5xl pt-20']"><Outlet /></main>
  </div>
</template>
