import { describe, expect, it } from "vitest";
import { aggregationKey, emptyMeterForm, formOfMeter, toMeterBody } from "./meter";

const form = (over: Partial<ReturnType<typeof emptyMeterForm>> = {}) => ({ ...emptyMeterForm(), code: "api_calls", unit: "calls", property: "count", ...over });

describe("toMeterBody", () => {
  it("makes the request of a sum meter with a scale and a filter", () => {
    const r = toMeterBody(form({ scale: "3", filters: [{ property: "region", values: "eu, us ,eu" }] }));
    expect(r).toEqual({ body: { code: "api_calls", aggregation: "AGGREGATION_SUM", unit_label: "calls", quantity_scale: 3, value_property: "count", filter: { region: { values: ["eu", "us"] } } } });
  });

  it("leaves the value property out of a count meter, and needs it for the others", () => {
    expect(toMeterBody(form({ aggregation: "AGGREGATION_COUNT", property: "" }))).toEqual({ body: { code: "api_calls", aggregation: "AGGREGATION_COUNT", unit_label: "calls", quantity_scale: 0 } });
    expect(toMeterBody(form({ property: "" }))).toEqual({ error: "property" });
    expect(toMeterBody(form({ aggregation: "AGGREGATION_MAX", property: "9x" }))).toEqual({ error: "property" });
  });

  it("names the field that is wrong", () => {
    expect(toMeterBody(form({ code: "Api Calls" }))).toEqual({ error: "code" });
    expect(toMeterBody(form({ code: "1calls" }))).toEqual({ error: "code" });
    expect(toMeterBody(form({ unit: "  " }))).toEqual({ error: "unit" });
    expect(toMeterBody(form({ unit: "x".repeat(33) }))).toEqual({ error: "unit" });
    expect(toMeterBody(form({ scale: "7" }))).toEqual({ error: "scale" });
    expect(toMeterBody(form({ scale: "1.5" }))).toEqual({ error: "scale" });
    expect(toMeterBody(form({ filters: [{ property: "region", values: "" }] }))).toEqual({ error: "filter" });
    expect(toMeterBody(form({ filters: [{ property: "a", values: "1" }, { property: "a", values: "2" }] }))).toEqual({ error: "filter" });
  });

  it("ignores a filter row nobody filled in", () => {
    expect(toMeterBody(form({ filters: [{ property: "", values: "" }] }))).toMatchObject({ body: { code: "api_calls" } });
  });
});

describe("formOfMeter", () => {
  it("fills the form of the next version from the meter", () => {
    const f = formOfMeter({ code: "gb", unit_label: "GB", aggregation: "AGGREGATION_MAX", value_property: "size", quantity_scale: 2, filter: { tier: { values: ["a", "b"] } } });
    expect(f).toEqual({ code: "gb", unit: "GB", aggregation: "AGGREGATION_MAX", property: "size", scale: "2", filters: [{ property: "tier", values: "a, b" }] });
  });
  it("names an aggregation by its short word", () => {
    expect(aggregationKey("AGGREGATION_COUNT")).toBe("count");
  });
});
