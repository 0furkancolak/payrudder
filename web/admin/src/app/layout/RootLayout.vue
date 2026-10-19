<script setup lang="ts">
import { Outlet } from "@tanstack/vue-router";
import AppNav from "./AppNav.vue";
</script>

<template>
  <AppNav />
  <main><Outlet /></main>
</template>
