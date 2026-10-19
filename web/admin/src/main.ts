import { VueQueryPlugin } from "@tanstack/vue-query";
import { createApp } from "vue";
import App from "./app/App.vue";
import { applyLocale, detectLocale, i18n } from "./app/i18n";
import { queryClient } from "./app/query";
import "./app/styles.css";

applyLocale(detectLocale());
createApp(App).use(i18n).use(VueQueryPlugin, { queryClient }).mount("#app");
