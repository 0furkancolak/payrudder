import { watch } from "vue";
import { accountingTour } from "@/features/accounting/tour";
import { affiliatesTour } from "@/features/affiliates/tour";
import { creditsTour } from "@/features/credits/tour";
import { customersTour } from "@/features/customers/tour";
import { deadLettersTour } from "@/features/dead-letters/tour";
import { mailTour } from "@/features/mail/tour";
import { metersTour } from "@/features/meters/tour";
import { packagesTour } from "@/features/packages/tour";
import { partnerTour } from "@/features/partner/tour";
import { paymentLinksTour } from "@/features/paymentlinks/tour";
import { playgroundTour } from "@/features/playground/tour";
import { couponsTour, pricingTour } from "@/features/pricing/tour";
import { useMe } from "@/features/product/api";
import { apiKeysTour, appSettingsTour, dashboardTour, goLiveTour, homeTour, invoicesTour, membersTour, paymentsTour, sandboxTour, settingsTour } from "@/features/product/tour";
import { providersTour } from "@/features/providers/tour";
import { reportsTour } from "@/features/reports/tour";
import { subscriptionsTour } from "@/features/subscriptions/tour";
import { tenantsTour } from "@/features/tenants/tour";
import { usageTour } from "@/features/usage/tour";
import { webhooksTour } from "@/features/webhooks/tour";
import { setTourScope, type Tour } from "@/shared/ui/tour";

/**
 * Every page's tour, by the key the navigation gives the page (see nav.ts): the page's header shows the tour icon when its key is here. The steps are
 * defined next to each page (features/<page>/tour.ts); this file only lists them, so a new page's tour is one import and one line.
 */
const TOURS: Readonly<Record<string, Tour>> = {
  overview: homeTour,
  dashboard: dashboardTour,
  goLive: goLiveTour,
  apiKeys: apiKeysTour,
  sandbox: sandboxTour,
  appSettings: appSettingsTour,
  members: membersTour,
  settings: settingsTour,
  partner: partnerTour,
  integrations: providersTour,
  providers: providersTour,
  customers: customersTour,
  subscriptions: subscriptionsTour,
  usage: usageTour,
  credits: creditsTour,
  meters: metersTour,
  packages: packagesTour,
  pricing: pricingTour,
  coupons: couponsTour,
  invoices: invoicesTour,
  payments: paymentsTour,
  paymentLinks: paymentLinksTour,
  accounting: accountingTour,
  reports: reportsTour,
  affiliates: affiliatesTour,
  webhooks: webhooksTour,
  mail: mailTour,
  tenants: tenantsTour,
  playground: playgroundTour,
  deadLetters: deadLettersTour,
};

/** The tour of the page whose navigation key this is, if it has one. */
export function tourFor(key: string | undefined): Tour | undefined {
  return key ? TOURS[key] : undefined;
}

/** Every tour there is, once each (a page two keys share counts once). */
export const ALL_TOURS: readonly Tour[] = [...new Set(Object.values(TOURS))];

/** Tells the tour engine whose "seen" list to use: the signed-in person's. Called once, by the frame. */
export function useTourScope(): void {
  const me = useMe();
  watch(() => me.data.value?.user?.id ?? "", setTourScope, { immediate: true });
}
