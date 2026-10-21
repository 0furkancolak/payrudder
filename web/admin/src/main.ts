import { VueQueryPlugin } from "@tanstack/vue-query";
import { createApp } from "vue";
import App from "./app/App.vue";
import { applyLocale, detectLocale, i18n } from "./app/i18n";
import { applyTheme } from "@/shared/lib/theme";
import { startPrefs } from "@/shared/lib/prefs";
import { queryClient } from "./app/query";
import "./app/styles.css";

applyTheme();
startPrefs();
const app = createApp(App).use(i18n).use(VueQueryPlugin, { queryClient });
// Mount once the language in use is loaded, so nothing flashes in English first.
void applyLocale(detectLocale()).then(() => app.mount("#app"));
