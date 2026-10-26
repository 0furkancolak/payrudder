import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";

/**
 * The GeoNames lists the gateway serves (GET /app/v1/geo/...). They change only with a new gateway release, so a page asks for each once and keeps it; the
 * browser's HTTP cache (a day, revalidated by ETag) covers the next visit.
 */
export const geoKeys = {
  countries: ["geo", "countries"] as const,
  regions: (iso2: string) => ["geo", "regions", iso2] as const,
  districts: (region: string) => ["geo", "districts", region] as const,
};

const forever = { staleTime: Infinity, gcTime: 60 * 60_000, retry: 1 } as const;

export function useGeoCountries() {
  return useQuery({ queryKey: geoKeys.countries, queryFn: () => unwrap(api.GET("/app/v1/geo/countries")), ...forever });
}

/** The regions of a country; nothing is asked until the code is two letters. An unknown country answers 404: the form then takes the region as text. */
export function useGeoRegions(iso2: MaybeRefOrGetter<string>) {
  const code = computed(() => toValue(iso2).trim().toUpperCase());
  return useQuery({
    queryKey: computed(() => geoKeys.regions(code.value)),
    queryFn: () => unwrap(api.GET("/app/v1/geo/countries/{iso2}/regions", { params: { path: { iso2: code.value } } })),
    enabled: computed(() => /^[A-Z]{2}$/.test(code.value)),
    ...forever,
  });
}

/** The districts of a region (by its id, "TR.34"); nothing is asked without one. */
export function useGeoDistricts(regionId: MaybeRefOrGetter<string>) {
  const id = computed(() => toValue(regionId));
  return useQuery({
    queryKey: computed(() => geoKeys.districts(id.value)),
    queryFn: () => unwrap(api.GET("/app/v1/geo/regions/{id}/districts", { params: { path: { id: id.value } } })),
    enabled: computed(() => id.value !== ""),
    ...forever,
  });
}
