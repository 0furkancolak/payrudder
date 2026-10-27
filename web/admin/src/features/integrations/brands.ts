import { Banknote, FlaskConical, Mail, Webhook } from "@lucide/vue";
import type { Component } from "vue";

/**
 * Who the integrations are and how each one is drawn. A provider's logo is shown only when a licensed or official mark is in `public/brand/providers/` (see
 * LICENSES.md there); a provider without one is shown by its plain name and nothing is drawn in its place. A generic method (bank transfer, a simulator, a
 * custom integrator) has no company behind it and gets a general-purpose icon.
 */
export type IntegrationId = "iyzico" | "paddle" | "banktransfer" | "simulator" | "nilvera" | "webhook" | "einvoiceSimulator" | "payrudderMail" | "nafru";
export type GroupId = "payments" | "einvoice" | "email";

export interface Brand {
  group: GroupId;
  /** The company's own name, shown as it is written; absent for a generic method, whose name is translated (`product.integrations.names.<id>`). */
  name?: string;
  /** Files under /brand/providers/, one per theme, or one that serves both. */
  logo?: { light: string; dark: string };
  /** A general-purpose icon, for a method with no company behind it. */
  icon?: Component;
  /** Where the provider is found on the web, for the "open" link. */
  url?: string;
}

/** The Nafru Email product (the Resend-compatible e-mail platform of the same owner). */
export const NAFRU_EMAIL_URL = "https://nafru.com";

export const BRANDS: Record<IntegrationId, Brand> = {
  iyzico: { group: "payments", name: "iyzico", url: "https://www.iyzico.com" },
  paddle: { group: "payments", name: "Paddle", logo: { light: "/brand/providers/paddle-light.svg", dark: "/brand/providers/paddle-dark.svg" }, url: "https://www.paddle.com" },
  banktransfer: { group: "payments", icon: Banknote },
  simulator: { group: "payments", icon: FlaskConical },
  nilvera: { group: "einvoice", name: "Nilvera", url: "https://nilvera.com" },
  webhook: { group: "einvoice", icon: Webhook },
  einvoiceSimulator: { group: "einvoice", icon: FlaskConical },
  payrudderMail: { group: "email", icon: Mail },
  nafru: { group: "email", name: "Nafru Email", logo: { light: "/brand/providers/nafru-email.svg", dark: "/brand/providers/nafru-email.svg" }, url: NAFRU_EMAIL_URL },
};

export const GROUPS: readonly GroupId[] = ["payments", "einvoice", "email"];

/** The integrations of a group, in the order they are shown. */
export const IN_GROUP: Record<GroupId, readonly IntegrationId[]> = {
  payments: ["iyzico", "paddle", "banktransfer", "simulator"],
  einvoice: ["nilvera", "webhook", "einvoiceSimulator"],
  email: ["payrudderMail", "nafru"],
};

/** Whether a card can be connected by the person (the others are always on and only explain themselves). */
export const CONNECTABLE: readonly IntegrationId[] = ["iyzico", "paddle", "banktransfer", "nilvera", "webhook", "nafru"];

/** Providers shown by name only, for the report to the owner: no licensed or official mark is in the repository for them. */
export const WITHOUT_LOGO = (Object.entries(BRANDS) as [IntegrationId, Brand][]).filter(([, b]) => b.name && !b.logo).map(([id]) => id);
