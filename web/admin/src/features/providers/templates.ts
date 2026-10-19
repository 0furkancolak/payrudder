/** What a provider account's settings look like. The secrets are blank: they are typed in, sealed by the server and never shown again. */
export const SETTINGS_TEMPLATES: Record<string, Record<string, unknown>> = {
  iyzico: {
    api_key: "",
    secret_key: "",
    base_url: "https://sandbox-api.iyzipay.com",
    buyer: { name: "", surname: "", identity_number: "", address: "", city: "", country: "", zip_code: "", ip: "", email: "" },
  },
  paddle: {
    api_key: "",
    webhook_secret: "",
    usage_product_id: "pro_",
    base_url: "https://sandbox-api.paddle.com",
  },
};

export function templateText(provider: string): string {
  return JSON.stringify(SETTINGS_TEMPLATES[provider] ?? {}, null, 2);
}
