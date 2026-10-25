import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createI18n } from "vue-i18n";
import { describe, expect, it } from "vitest";
import { ApiError, toApiError } from "@/shared/api/client";
import ar from "@/locales/ar.json";
import de from "@/locales/de.json";
import en from "@/locales/en.json";
import es from "@/locales/es.json";
import fr from "@/locales/fr.json";
import tr from "@/locales/tr.json";
import { errorText } from "./errors";

const repo = (path: string) =>
  fileURLToPath(new URL(`../../../../../${path}`, import.meta.url));

/**
 * The keys a client can receive: every key in docs/error-keys.md (generated from the Go code by `go test ./pkg/errkey`, which fails when the list and
 * the code disagree) and the `common.*` defaults the gateway sends for an error without a key of its own (pkg/errkey `Default`). Reading both files
 * makes this test follow the backend: a new key fails here until every language has a translation.
 */
function backendKeys(): string[] {
  const doc = readFileSync(repo("docs/error-keys.md"), "utf8");
  const listed = [...doc.matchAll(/^- `([a-z0-9_]+(?:\.[a-z0-9_]+)+)`$/gm)].map(
    (m) => m[1] as string,
  );
  const source = readFileSync(repo("pkg/errkey/errkey.go"), "utf8");
  const defaults = [...source.matchAll(/return "(common\.[a-z0-9_]+)"/g)].map(
    (m) => m[1] as string,
  );
  return [...new Set([...listed, ...defaults])].sort();
}

type Tree = { [key: string]: string | Tree };
const translated = (file: unknown, key: string): string | undefined => {
  let node: string | Tree | undefined = (file as { errors: Tree }).errors;
  for (const part of key.split(".")) {
    if (typeof node !== "object") return undefined;
    node = node[part];
  }
  return typeof node === "string" ? node : undefined;
};
const flatKeys = (tree: Tree, prefix = ""): string[] =>
  Object.entries(tree).flatMap(([k, v]) =>
    typeof v === "string" ? [prefix + k] : flatKeys(v, `${prefix}${k}.`),
  );

describe("error keys", () => {
  const keys = backendKeys();

  it("finds the keys of the backend", () => {
    expect(keys.length).toBeGreaterThan(20);
    expect(keys).toContain("billing.credit_note.not_paid");
    expect(keys).toContain("common.internal");
  });

  it("lists the common.* defaults in docs/error-keys.md, not only in the source", () => {
    const doc = readFileSync(repo("docs/error-keys.md"), "utf8");
    const source = readFileSync(repo("pkg/errkey/errkey.go"), "utf8");
    const defaults = [...source.matchAll(/return "(common\.[a-z0-9_]+)"/g)].map(
      (m) => m[1] as string,
    );
    expect(defaults.length).toBeGreaterThan(5);
    expect(defaults.filter((k) => !doc.includes(`- \`${k}\``))).toEqual([]);
  });

  describe.each([
    ["en", en],
    ["tr", tr],
    ["de", de],
    ["es", es],
    ["fr", fr],
    ["ar", ar],
  ])("locale %s", (_name, file) => {
    it("translates every key the backend can send", () => {
      const missing = keys.filter((k) => !translated(file, k));
      expect(
        missing,
        `add these to "errors" in src/locales/${_name}.json`,
      ).toEqual([]);
    });

    it("uses the placeholders of the English text, no more and no fewer", () => {
      const holes = (text: string) =>
        [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
      const off = keys.filter(
        (k) =>
          translated(file, k) &&
          translated(en, k) &&
          holes(translated(file, k) as string).join() !==
            holes(translated(en, k) as string).join(),
      );
      expect(off, "the placeholders differ from en.json").toEqual([]);
    });

    it("has no translation for a key the backend no longer has", () => {
      const stale = flatKeys((file as { errors: Tree }).errors).filter(
        (k) => !keys.includes(k),
      );
      expect(stale, "remove these from the locale files").toEqual([]);
    });
  });
});

describe("errorText", () => {
  const i18n = createI18n({
    legacy: false,
    locale: "tr",
    fallbackLocale: "en",
    messages: { en, tr },
  });
  const { t, te } = i18n.global;
  const text = (e: unknown) => errorText(t, te, e);

  it("translates the key of an API error and ignores the server's English message", () => {
    const e = new ApiError(
      "only a paid invoice can be credited",
      "FAILED_PRECONDITION",
      "billing.credit_note.not_paid",
    );
    expect(text(e)).toBe(tr.errors.billing.credit_note.not_paid);
    expect(text(e)).not.toBe(e.message);
  });

  it("works on the raw envelope the gateway sends", () => {
    const body = {
      error: {
        code: "ALREADY_EXISTS",
        key: "plans.app.slug_taken",
        message: "this account already has an app with that slug",
        request_id: "r1",
      },
    };
    expect(text(body)).toBe(tr.errors.plans.app.slug_taken);
  });

  it("falls back to the server's message for a key the console does not know", () => {
    expect(
      text(new ApiError("something new", "INTERNAL", "billing.brand_new.key")),
    ).toBe("something new");
  });

  it("falls back to the message for an error without a key, and for a network failure", () => {
    expect(text(new ApiError("plain", "ERROR"))).toBe("plain");
    expect(text(new TypeError("Failed to fetch"))).toBe("Failed to fetch");
  });

  it("falls back to English when the active locale lacks the entry", () => {
    const partial = createI18n({
      legacy: false,
      locale: "tr",
      fallbackLocale: "en",
      messages: { en, tr: { ...tr, errors: {} } },
    });
    expect(
      errorText(
        partial.global.t,
        partial.global.te,
        new ApiError("x", "INTERNAL", "common.internal"),
      ),
    ).toBe(en.errors.common.internal);
  });

  it("passes the error's params to the message", () => {
    const withParam = createI18n({
      legacy: false,
      locale: "en",
      messages: {
        en: { errors: { demo: { limit: "Too long: {field} ({detail})" } } },
      },
    });
    const e = toApiError({
      error: {
        code: "INVALID_ARGUMENT",
        key: "demo.limit",
        message: "m",
        details: [{ field: "address.line1", description: "too long" }],
      },
    });
    expect(e.params).toEqual({ field: "address.line1", detail: "too long" });
    expect(errorText(withParam.global.t, withParam.global.te, e)).toBe(
      "Too long: address.line1 (too long)",
    );
  });
});
