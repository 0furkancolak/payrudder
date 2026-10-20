import { checkIban, type IbanProblem } from "./iban";

/** What the operator types in the bank transfer account form. */
export interface BankForm {
  bankName: string;
  accountHolder: string;
  iban: string;
  bic: string;
  referenceTemplate: string;
  /** Empty: every currency the tenant bills in. */
  currencies: string[];
  /** Days as typed; empty or 0 means the deployment's default. */
  windowDays: string;
}

export type BankField = "bankName" | "accountHolder" | "iban" | "bic" | "referenceTemplate" | "windowDays";
/** Why a field is refused; the form shows a translated message per code. */
export type BankProblem = "required" | IbanProblem | "badBic" | "badTemplate" | "badWindow";
export type BankErrors = Partial<Record<BankField, BankProblem>>;

/** The settings the PUT takes (docs/phases/phase-29-bank-transfer.md, decision 1). */
export interface BankSettings {
  bank_name: string;
  account_holder: string;
  iban: string;
  bic?: string;
  reference_template?: string;
  currencies?: string[];
  payment_window_days?: number;
}

export const emptyBankForm = (): BankForm => ({ bankName: "", accountHolder: "", iban: "", bic: "", referenceTemplate: "", currencies: [], windowDays: "" });

/** The placeholders a reference template may use. */
export const REFERENCE_PLACEHOLDERS = ["invoice_number", "invoice_id"] as const;

/** Whether every {…} in the template is a known placeholder and no brace is left over. */
export function templateOk(template: string): boolean {
  const rest = template.replace(/\{(\w+)\}/g, (whole, name: string) => ((REFERENCE_PLACEHOLDERS as readonly string[]).includes(name) ? "" : whole));
  return !/[{}]/.test(rest);
}

/**
 * Checks the form and builds the settings object: the IBAN normalised (spaces gone, capitals), the BIC upper-cased, optional fields left out when blank.
 * The server validates again; this is so a typo is caught before the round trip.
 */
export function buildBankSettings(form: BankForm): { ok: true; settings: BankSettings } | { ok: false; errors: BankErrors } {
  const errors: BankErrors = {};
  const bankName = form.bankName.trim();
  const accountHolder = form.accountHolder.trim();
  const bic = form.bic.replace(/\s/g, "").toUpperCase();
  const template = form.referenceTemplate.trim();
  const days = form.windowDays.trim();

  if (bankName === "") errors.bankName = "required";
  if (accountHolder === "") errors.accountHolder = "required";
  const iban = checkIban(form.iban);
  if (!iban.ok) errors.iban = iban.problem;
  if (bic !== "" && !/^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$/.test(bic)) errors.bic = "badBic";
  if (template !== "" && !templateOk(template)) errors.referenceTemplate = "badTemplate";
  let windowDays: number | undefined;
  if (days !== "") {
    windowDays = /^\d{1,3}$/.test(days) ? Number(days) : NaN;
    if (!(windowDays >= 0 && windowDays <= 90)) errors.windowDays = "badWindow";
  }
  if (Object.keys(errors).length > 0 || !iban.ok) return { ok: false, errors };

  const settings: BankSettings = { bank_name: bankName, account_holder: accountHolder, iban: iban.iban };
  if (bic !== "") settings.bic = bic;
  if (template !== "") settings.reference_template = template;
  if (form.currencies.length > 0) settings.currencies = [...form.currencies];
  if (windowDays !== undefined) settings.payment_window_days = windowDays;
  return { ok: true, settings };
}
