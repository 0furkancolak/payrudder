import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { activeStep, activeTour, endTour, markTourSeen, nextStep, pageTour, placeCard, prevStep, setTourScope, skipStep, startTour, startTourOnce, tourSeen } from "./tour";

const steps = (...ids: string[]) => ids.map((id) => ({ id }));
const demo = pageTour("demo", "shell.items.customers", steps("a", "b", "c"));

beforeEach(() => {
  const store = new Map<string, string>();
  vi.stubGlobal("window", { localStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) } });
  endTour(false);
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
