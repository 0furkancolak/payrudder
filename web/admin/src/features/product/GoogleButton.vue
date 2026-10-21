<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

/**
 * "Continue with Google". It follows Google's branding rules for the button: the multicolour "G" mark unchanged on a white (light) or #131314 (dark) face
 * with Google's grey outline, a height of at least 40 px, the label in the button's own language, and nothing else added to the mark. It is a plain link,
 * not a script: the browser leaves for the gateway, which sends it on to Google, and comes back to /signin/complete with a session.
 */
const { t, locale } = useI18n();
const href = computed(() => `/gw/auth/google/start?locale=${encodeURIComponent(locale.value)}`);
</script>

<template>
  <a class="google-button" :href="href" data-testid="google-button">
    <svg class="google-button-mark" viewBox="0 0 48 48" aria-hidden="true" focusable="false">
      <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
      <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
      <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
      <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
    </svg>
    <span>{{ t("product.signIn.google") }}</span>
  </a>
</template>

<style>
.google-button {
  --gb-face: #ffffff;
  --gb-line: #747775;
  --gb-text: #1f1f1f;
  --gb-hover: rgb(48 48 48 / 0.08);
  display: flex;
  min-height: 2.75rem;
  width: 100%;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  border: 1px solid var(--gb-line);
  border-radius: 0.75rem;
  background: var(--gb-face);
  padding: 0 1rem;
  color: var(--gb-text);
  font-family: Roboto, var(--font-sans);
  font-size: 0.875rem;
  font-weight: 500;
  letter-spacing: 0.01em;
  text-decoration: none;
  transition: background-color 0.15s;
}
.google-button:hover {
  background-image: linear-gradient(var(--gb-hover), var(--gb-hover));
}
.google-button:focus-visible {
  outline: 2px solid var(--ring);
  outline-offset: 2px;
}
.google-button-mark {
  width: 1.25rem;
  height: 1.25rem;
  flex: none;
}
:root[data-theme="dark"] .google-button {
  --gb-face: #131314;
  --gb-line: #8e918f;
  --gb-text: #e3e3e3;
  --gb-hover: rgb(232 234 237 / 0.1);
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) .google-button {
    --gb-face: #131314;
    --gb-line: #8e918f;
    --gb-text: #e3e3e3;
    --gb-hover: rgb(232 234 237 / 0.1);
  }
}
</style>
