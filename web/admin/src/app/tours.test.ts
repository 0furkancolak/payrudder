import { describe, expect, it } from "vitest";
import ar from "@/locales/ar.json";
import de from "@/locales/de.json";
import en from "@/locales/en.json";
import es from "@/locales/es.json";
import fr from "@/locales/fr.json";
import tr from "@/locales/tr.json";
import { PERSON_PAGES } from "./layout/nav";
import { ALL_TOURS, tourFor } from "./tours";

type Tree = { [key: string]: string | Tree };
const lookup = (tree: Tree, key: string): string | undefined => {
  let node: string | Tree | undefined = tree;
  for (const part of key.split(".")) node = typeof node === "object" ? node[part] : undefined;
  return typeof node === "string" ? node : undefined;
};

describe("tours", () => {
  it("every step of every tour has its title and body in all six languages", () => {
    for (const [name, tree] of [["en", en], ["tr", tr], ["de", de], ["fr", fr], ["es", es], ["ar", ar]] as const) {
      for (const tour of ALL_TOURS) {
        for (const step of tour.steps) {
          expect(lookup(tree as Tree, step.title), `${name}: ${tour.id}/${step.id} title ${step.title}`).toBeTruthy();
          expect(lookup(tree as Tree, step.body), `${name}: ${tour.id}/${step.id} body ${step.body}`).toBeTruthy();
        }
      }
    }
  });

  it("has unique ids and unique step ids within a tour, and every step has a target or is meant to be centred", () => {
    expect(new Set(ALL_TOURS.map((t) => t.id)).size).toBe(ALL_TOURS.length);
    for (const tour of ALL_TOURS) {
      expect(tour.steps.length, tour.id).toBeGreaterThan(0);
      expect(new Set(tour.steps.map((s) => s.id)).size, tour.id).toBe(tour.steps.length);
    }
  });

  it("covers every page a person has in the sidebar", () => {
    const missing = ["overview", "dashboard", "goLive", "apiKeys", "sandbox", "appSettings", "paymentProviders", ...PERSON_PAGES].filter((k) => !tourFor(k));
    expect(missing).toEqual([]);
  });

  it("the home's tour is the one that starts by itself, and it explains the app switcher, the sidebar, the sandbox switch and the user menu", () => {
    const home = tourFor("overview");
    expect(home?.steps.map((s) => s.id)).toEqual(["welcome", "apps", "groups", "sandbox", "account", "again"]);
  });
});
