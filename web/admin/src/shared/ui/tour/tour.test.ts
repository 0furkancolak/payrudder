import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { activeStep, activeTour, endTour, forgetTours, markTourSeen, nextStep, pageTour, placeCard, prevStep, setTourScope, setTourStore, skipStep, startTour, startTourOnce, tourReady, tourSeen, type TourStore } from "./tour";

let store: Map<string, string>;
const steps = (...ids: string[]) => ids.map((id) => ({ id }));
const demo = pageTour("demo", "shell.items.customers", steps("a", "b", "c"));

beforeEach(() => {
  store = new Map<string, string>();
  vi.stubGlobal("window", {
    localStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      get length() {
        return store.size;
      },
      key: (i: number) => [...store.keys()][i] ?? null,
    },
  });
  endTour(false);
  setTourStore(null);
  setTourScope("u1");
});
afterEach(() => vi.unstubAllGlobals());

describe("pageTour", () => {
  it("names the keys of a page's steps, and gives the first the page's own title", () => {
    expect(demo.steps.map((s) => s.body)).toEqual(["tour.pages.demo.a", "tour.pages.demo.b", "tour.pages.demo.c"]);
    expect(demo.steps.map((s) => s.title)).toEqual(["shell.items.customers", "tour.pages.demo.bTitle", "tour.pages.demo.cTitle"]);
  });
});

describe("running a tour", () => {
  it("walks forward and back, and finishing marks it seen", () => {
    startTour(demo);
    expect(activeStep.value?.id).toBe("a");
    prevStep();
    expect(activeStep.value?.id).toBe("a");
    nextStep();
    nextStep();
    expect(activeStep.value?.id).toBe("c");
    prevStep();
    expect(activeStep.value?.id).toBe("b");
    nextStep();
    nextStep();
    expect(activeTour.value).toBeNull();
    expect(tourSeen("demo")).toBe(true);
  });

  it("skipping marks it seen; leaving the page does not", () => {
    startTour(demo);
    endTour();
    expect(tourSeen("demo")).toBe(true);
    startTour(pageTour("other", "x", steps("a")));
    endTour(false);
    expect(tourSeen("other")).toBe(false);
  });

  it("starts by itself once, only for a known person, and never over another tour", () => {
    setTourScope("");
    expect(startTourOnce(demo)).toBe(false);
    setTourScope("u1");
    expect(startTourOnce(demo)).toBe(true);
    expect(startTourOnce(pageTour("second", "x", steps("a")))).toBe(false);
    endTour();
    expect(startTourOnce(demo)).toBe(false);
  });

  it("remembers per person", () => {
    markTourSeen("demo");
    expect(tourSeen("demo")).toBe(true);
    setTourScope("u2");
    expect(tourSeen("demo")).toBe(false);
    setTourScope("u1");
    expect(tourSeen("demo")).toBe(true);
  });

  it("passes over a step that cannot be shown, in the direction of travel", () => {
    startTour(demo);
    nextStep();
    skipStep();
    expect(activeStep.value?.id).toBe("c");
    prevStep();
    skipStep();
    expect(activeStep.value?.id).toBe("a");
    skipStep();
    expect(activeStep.value?.id).toBe("b");
  });

  it("does nothing for a tour without steps", () => {
    startTour({ id: "empty", steps: [] });
    expect(activeTour.value).toBeNull();
  });
});

describe("placeCard", () => {
  const view = { w: 1000, h: 700 };
  const card = { w: 340, h: 200 };

  it("centres a step without an element", () => {
    expect(placeCard(null, card, view)).toEqual({ x: 330, y: 250, side: "center" });
  });

  it("puts the card under the element when it fits, above when it does not", () => {
    expect(placeCard({ x: 100, y: 100, w: 200, h: 50 }, card, view).side).toBe("below");
    expect(placeCard({ x: 100, y: 560, w: 200, h: 50 }, card, view).side).toBe("above");
  });

  it("puts it beside an element as tall as the screen, and keeps it on the screen", () => {
    const side = placeCard({ x: 16, y: 16, w: 260, h: 668 }, card, view);
    expect(side.side).toBe("end");
    expect(side.x).toBe(290);
    const edge = placeCard({ x: 900, y: 100, w: 90, h: 40 }, card, view);
    expect(edge.x + card.w).toBeLessThanOrEqual(view.w - 12);
  });
});

/** A server that keeps the seen tours of a person, and answers when the test lets it. */
function fakeServer(initial: string[] | null) {
  let answer: (v: string[] | null) => void = () => undefined;
  const added: string[][] = [];
  const forgotten: string[][] = [];
  const server: TourStore = {
    load: () => new Promise((r) => (answer = r)),
    add: (ids) => void added.push([...ids]),
    forget: (ids) => void forgotten.push([...ids]),
  };
  return { server, added, forgotten, answer: () => answer(initial) };
}
const settle = () => new Promise((r) => setTimeout(r, 0));

describe("tours kept on the server", () => {
  it("what the server says is seen is seen here, and a tour does not start by itself before it has said", async () => {
    const f = fakeServer(["demo"]);
    setTourStore(f.server);
    setTourScope("u1");
    expect(tourReady.value).toBe(false);
    expect(startTourOnce(demo)).toBe(false);
    expect(tourSeen("demo")).toBe(false);
    f.answer();
    await settle();
    expect(tourReady.value).toBe(true);
    expect(tourSeen("demo")).toBe(true);
    expect(startTourOnce(demo)).toBe(false);
    expect(startTourOnce(pageTour("fresh", "x", steps("a")))).toBe(true);
  });

  it("sends up what only this browser has seen, and nothing else", async () => {
    store.set("payrudder.tour.u1.old", "1");
    store.set("payrudder.tour.u1.shared", "1");
    store.set("payrudder.tour.u2.other", "1");
    const f = fakeServer(["shared", "theirs"]);
    setTourStore(f.server);
    setTourScope("u1");
    f.answer();
    await settle();
    expect(f.added).toEqual([["old"]]);
    expect(tourSeen("theirs")).toBe(true);
  });

  it("goes on with this browser's copy when the server cannot say", async () => {
    store.set("payrudder.tour.u1.old", "1");
    const f = fakeServer(null);
    setTourStore(f.server);
    setTourScope("u1");
    expect(tourReady.value).toBe(false);
    f.answer();
    await settle();
    expect(tourReady.value).toBe(true);
    expect(tourSeen("old")).toBe(true);
    expect(f.added).toEqual([]);
    expect(startTourOnce(pageTour("fresh", "x", steps("a")))).toBe(true);
  });

  it("an answer for the person who was there before is ignored", async () => {
    const f = fakeServer(["demo"]);
    setTourStore(f.server);
    setTourScope("u1");
    const first = f.answer;
    const g = fakeServer([]);
    setTourStore(g.server);
    setTourScope("u2");
    first();
    await settle();
    expect(tourReady.value).toBe(false);
    expect(tourSeen("demo")).toBe(false);
    g.answer();
    await settle();
    expect(tourReady.value).toBe(true);
  });

  it("writes through: a tour marked seen or forgotten is told to the server, and nobody's tours are before the scope is set", () => {
    const f = fakeServer([]);
    setTourStore(f.server);
    setTourScope("");
    markTourSeen("demo");
    expect(f.added).toEqual([]);
    setTourScope("u1");
    markTourSeen("demo");
    expect(f.added).toEqual([["demo"]]);
    forgetTours(["demo"]);
    expect(f.forgotten).toEqual([["demo"]]);
    expect(tourSeen("demo")).toBe(false);
  });

  it("is quiet without a store: the browser alone, as before", () => {
    markTourSeen("demo");
    forgetTours(["demo"]);
    expect(tourReady.value).toBe(true);
  });
});
