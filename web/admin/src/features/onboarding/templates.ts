import { Boxes, Cpu, HardDrive, Users } from "@lucide/vue";
import type { Component } from "vue";

/** A meter a new person can start from. Its words are under `onboarding.meter.templates.<key>`. */
export interface MeterTemplate {
  key: "api_calls" | "storage" | "seats" | "tokens";
  /** The meter as the API takes it. */
  meter: { code: string; aggregation: "AGGREGATION_SUM" | "AGGREGATION_COUNT" | "AGGREGATION_MAX"; value_property?: string; quantity_scale: number; unit_label: string; category: string };
  /** What the test event of the wizard sends for it (a count meter counts events, so its quantity is one). */
  sample: string;
  /** The price per unit the package step starts with, in major units of the currency. */
  unitPrice: string;
  icon: Component;
}

export const METER_TEMPLATES: readonly MeterTemplate[] = [
  { key: "api_calls", meter: { code: "api_calls", aggregation: "AGGREGATION_COUNT", quantity_scale: 0, unit_label: "calls", category: "API" }, sample: "1", unitPrice: "0.002", icon: Boxes },
  { key: "storage", meter: { code: "storage_gb", aggregation: "AGGREGATION_MAX", value_property: "gb", quantity_scale: 2, unit_label: "GB", category: "Storage" }, sample: "5", unitPrice: "0.1", icon: HardDrive },
  { key: "seats", meter: { code: "seats", aggregation: "AGGREGATION_MAX", value_property: "seats", quantity_scale: 0, unit_label: "seats", category: "Team" }, sample: "3", unitPrice: "5", icon: Users },
  { key: "tokens", meter: { code: "ai_tokens", aggregation: "AGGREGATION_SUM", value_property: "tokens", quantity_scale: 0, unit_label: "tokens", category: "AI" }, sample: "1000", unitPrice: "0.0004", icon: Cpu },
];

export const templateByCode = (code: string | undefined) => METER_TEMPLATES.find((t) => t.meter.code === code);

/** The customer the wizard's test event is for. */
export const TEST_CUSTOMER = "cus_onboarding";
